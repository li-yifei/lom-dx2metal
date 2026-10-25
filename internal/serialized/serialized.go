// Package serialized 读写 Unity SerializedFile（格式版本 22，Unity 2020.3）与 UnityFS bundle，
// 定位 Shader(ClassID 48) 对象并按内嵌类型树解析/重写。
//
// 写出策略：元数据里对象表之前（类型表等）与之后（脚本、外部引用、引用类型、用户信息）的字节原样保留，
// 只重编码对象表；数据区按原文件中的对象顺序重新排布，对象之间保留原有的对齐方式。
// 没有改动时输出与输入逐字节相等（验收 2）。
package serialized

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// ClassShader 是 Unity Shader 的 ClassID。
const ClassShader = 48

// Type 是元数据类型表的一项。
type Type struct {
	ClassID int32
	Tree    *Node // 文件内嵌类型树；Player 构建为 nil
}

// Object 是对象表的一项。
type Object struct {
	PathID  int64
	TypeID  int32
	ClassID int32
	start   int64  // 原文件中相对数据区的起点
	extra   []byte // 原文件中本对象之前、对齐填充之外多出的字节（Unity 会留空洞），原样保留
	data    []byte
	changed bool
}

// Data 返回对象当前的字节。
func (o *Object) Data() []byte { return o.data }

// File 是一个解析后的 SerializedFile。
type File struct {
	Version        uint32
	UnityVersion   string
	Platform       int32
	EnableTypeTree bool
	Types          []Type
	Objects        []*Object // 对象表顺序

	order      binary.ByteOrder
	app        binary.AppendByteOrder
	headerTail []byte // 端序字节 + 3 保留字节
	unknown    int64
	dataOffset int64
	metaPrefix []byte // 元数据里对象数之前的部分
	metaSuffix []byte // 对象表之后的部分
	gapAfter   []byte // 元数据结束到数据区起点之间的填充
	trailing   []byte // 最后一个对象之后的尾部字节
	align      int64  // 对象起点对齐（由原文件推断）
}

const headerSize22 = 48

// IsSerializedFile 粗判一段字节是否为 v22 SerializedFile。
func IsSerializedFile(b []byte) bool {
	return len(b) >= headerSize22 && binary.BigEndian.Uint32(b[8:]) == 22
}

// Parse 解析 v22 SerializedFile。
func Parse(b []byte) (*File, error) {
	if len(b) < headerSize22 {
		return nil, errors.New("文件过短")
	}
	be := binary.BigEndian
	f := &File{Version: be.Uint32(b[8:])}
	if f.Version != 22 {
		return nil, fmt.Errorf("只支持 SerializedFile 版本 22，得到 %d", f.Version)
	}
	f.headerTail = append([]byte(nil), b[16:20]...)
	metaSize := int64(be.Uint32(b[20:]))
	fileSize := int64(be.Uint64(b[24:]))
	f.dataOffset = int64(be.Uint64(b[32:]))
	f.unknown = int64(be.Uint64(b[40:]))
	if fileSize != int64(len(b)) || headerSize22+metaSize > f.dataOffset || f.dataOffset > fileSize {
		return nil, fmt.Errorf("文件头长度不一致（file_size=%d 实际=%d）", fileSize, len(b))
	}
	if b[16] != 0 {
		f.order, f.app = binary.BigEndian, binary.BigEndian
	} else {
		f.order, f.app = binary.LittleEndian, binary.LittleEndian
	}
	meta := b[headerSize22 : headerSize22+metaSize]
	p := 0
	need := func(n int) error {
		if p+n > len(meta) {
			return errors.New("元数据截断")
		}
		return nil
	}
	cstr := func() (string, error) {
		end := bytes.IndexByte(meta[p:], 0)
		if end < 0 {
			return "", errors.New("元数据字符串未结束")
		}
		s := string(meta[p : p+end])
		p += end + 1
		return s, nil
	}
	u32 := func() (uint32, error) {
		if err := need(4); err != nil {
			return 0, err
		}
		v := f.order.Uint32(meta[p:])
		p += 4
		return v, nil
	}
	var err error
	if f.UnityVersion, err = cstr(); err != nil {
		return nil, err
	}
	pl, err := u32()
	if err != nil {
		return nil, err
	}
	f.Platform = int32(pl)
	if err := need(1); err != nil {
		return nil, err
	}
	f.EnableTypeTree = meta[p] != 0
	p++
	typeCount, err := u32()
	if err != nil {
		return nil, err
	}
	for range typeCount {
		if err := need(7); err != nil {
			return nil, err
		}
		t := Type{ClassID: int32(f.order.Uint32(meta[p:]))}
		p += 7 // class_id + is_stripped + script_type_index
		if t.ClassID == 114 {
			p += 16 // script_id
		}
		p += 16 // old_type_hash
		if err := need(0); err != nil {
			return nil, err
		}
		if f.EnableTypeTree {
			root, n, err := parseTypeTreeBlob(meta[p:], f.order, f.Version)
			if err != nil {
				return nil, err
			}
			t.Tree = root
			p += n
			deps, err := u32() // type_dependencies
			if err != nil {
				return nil, err
			}
			p += int(deps) * 4
		}
		f.Types = append(f.Types, t)
	}
	f.metaPrefix = append([]byte(nil), meta[:p]...)
	objCount, err := u32()
	if err != nil {
		return nil, err
	}
	for range objCount {
		p = alignAbs(p, headerSize22)
		if err := need(24); err != nil {
			return nil, err
		}
		o := &Object{
			PathID: int64(f.order.Uint64(meta[p:])),
			start:  int64(f.order.Uint64(meta[p+8:])),
			TypeID: int32(f.order.Uint32(meta[p+20:])),
		}
		size := int64(f.order.Uint32(meta[p+16:]))
		p += 24
		if o.TypeID < 0 || int(o.TypeID) >= len(f.Types) {
			return nil, fmt.Errorf("对象 %d 类型索引越界", o.PathID)
		}
		o.ClassID = f.Types[o.TypeID].ClassID
		lo := f.dataOffset + o.start
		if o.start < 0 || lo+size > int64(len(b)) {
			return nil, fmt.Errorf("对象 %d 数据越界", o.PathID)
		}
		o.data = b[lo : lo+size]
		f.Objects = append(f.Objects, o)
	}
	f.metaSuffix = append([]byte(nil), meta[p:]...)
	f.gapAfter = append([]byte(nil), b[headerSize22+metaSize:f.dataOffset]...)
	// 推断对象起点对齐：所有对象起点都是 8 的倍数则按 8，否则按 4，再不行不做对齐。
	f.align = 8
	for _, o := range f.Objects {
		for f.align > 1 && o.start%f.align != 0 {
			f.align /= 2
		}
	}
	ordered := append([]*Object(nil), f.Objects...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].start < ordered[j].start })
	var prevEnd int64
	for _, o := range ordered {
		if o.start < prevEnd {
			return nil, fmt.Errorf("对象 %d 与前一个对象重叠", o.PathID)
		}
		aligned := (prevEnd + f.align - 1) / f.align * f.align
		if o.start > aligned {
			o.extra = b[f.dataOffset+aligned : f.dataOffset+o.start]
		}
		prevEnd = o.start + int64(len(o.data))
	}
	end := f.dataOffset
	for _, o := range f.Objects {
		if e := f.dataOffset + o.start + int64(len(o.data)); e > end {
			end = e
		}
	}
	f.trailing = append([]byte(nil), b[end:]...)
	return f, nil
}

// alignAbs 让 meta 内偏移 p 在整个文件中的绝对位置（base+p）4 字节对齐。
func alignAbs(p, base int) int {
	return (base+p+3)&^3 - base
}

// Order 返回对象数据的端序。
func (f *File) Order() binary.ByteOrder { return f.order }

// SetData 替换对象字节。
func (f *File) SetData(o *Object, data []byte) {
	o.data = data
	o.changed = true
}

// Changed 报告是否有对象被改过。
func (f *File) Changed() bool {
	for _, o := range f.Objects {
		if o.changed {
			return true
		}
	}
	return false
}

// TreeFor 返回对象的类型树：文件内嵌的优先，Shader 回退到内嵌的 2020.3 类型树。
func (f *File) TreeFor(o *Object) (*Node, error) {
	if t := f.Types[o.TypeID].Tree; t != nil {
		return t, nil
	}
	if o.ClassID == ClassShader {
		return ShaderTypeTree()
	}
	return nil, fmt.Errorf("ClassID %d 没有类型树", o.ClassID)
}

// Bytes 把文件重新编码。
func (f *File) Bytes() []byte {
	// 数据区：按原起点顺序排布，未改动时与原布局相同。
	ordered := append([]*Object(nil), f.Objects...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].start < ordered[j].start })
	starts := make(map[*Object]int64, len(ordered))
	var data []byte
	for _, o := range ordered {
		for int64(len(data))%f.align != 0 {
			data = append(data, 0)
		}
		data = append(data, o.extra...)
		starts[o] = int64(len(data))
		data = append(data, o.data...)
	}
	meta := append([]byte(nil), f.metaPrefix...)
	meta = f.app.AppendUint32(meta, uint32(len(f.Objects)))
	for _, o := range f.Objects {
		for (headerSize22+len(meta))%4 != 0 {
			meta = append(meta, 0)
		}
		meta = f.app.AppendUint64(meta, uint64(o.PathID))
		meta = f.app.AppendUint64(meta, uint64(starts[o]))
		meta = f.app.AppendUint32(meta, uint32(len(o.data)))
		meta = f.app.AppendUint32(meta, uint32(o.TypeID))
	}
	meta = append(meta, f.metaSuffix...)
	gap := f.gapAfter
	if headerSize22+int64(len(meta))+int64(len(gap)) != f.dataOffset {
		// 元数据长度变了（不应发生：对象表项定长）：重新按 16 对齐。
		pad := (16 - (headerSize22+len(meta))%16) % 16
		gap = make([]byte, pad)
	}
	dataOffset := int64(headerSize22 + len(meta) + len(gap))
	total := dataOffset + int64(len(data)) + int64(len(f.trailing))
	be := binary.BigEndian
	out := make([]byte, 0, total)
	out = be.AppendUint32(out, 0)
	out = be.AppendUint32(out, 0)
	out = be.AppendUint32(out, f.Version)
	out = be.AppendUint32(out, 0)
	out = append(out, f.headerTail...)
	out = be.AppendUint32(out, uint32(len(meta)))
	out = be.AppendUint64(out, uint64(total))
	out = be.AppendUint64(out, uint64(dataOffset))
	out = be.AppendUint64(out, uint64(f.unknown))
	out = append(out, meta...)
	out = append(out, gap...)
	out = append(out, data...)
	out = append(out, f.trailing...)
	return out
}

// Shaders 返回所有 Shader 对象。
func (f *File) Shaders() []*Object {
	var out []*Object
	for _, o := range f.Objects {
		if o.ClassID == ClassShader {
			out = append(out, o)
		}
	}
	return out
}

// ReadObject 按类型树解析对象。
func (f *File) ReadObject(o *Object) (*Struct, error) {
	tree, err := f.TreeFor(o)
	if err != nil {
		return nil, err
	}
	return ReadTree(tree, o.data, f.order)
}

// WriteObject 按类型树编码对象并替换。
func (f *File) WriteObject(o *Object, v *Struct) error {
	tree, err := f.TreeFor(o)
	if err != nil {
		return err
	}
	b, err := WriteTree(tree, v, f.app)
	if err != nil {
		return err
	}
	f.SetData(o, b)
	return nil
}

// App 返回追加式编码用的端序。
func (f *File) App() binary.AppendByteOrder { return f.app }
