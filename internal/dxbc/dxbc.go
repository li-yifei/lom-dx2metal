// Package dxbc 处理 Unity 去掉反射的 DXBC：容器解析、SHDR 的 dcl_* 声明解析、按 Unity 参数表合成 RDEF、
// 拼回 HLSLcc 能吃的完整容器（移植 hlslcc_bridge.py）。
//
// Unity 构建会剥掉 D3D 反射（它自己有参数表），HLSLcc 却需要 RDEF。资源槽位/维度取自 SHDR 的 dcl_* 指令，
// 名字与常量缓冲区布局取自 Shader 的 m_ParsedForm 与 legacy tail。
package dxbc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

var le = binary.LittleEndian

// D3D 着色器变量类/类型（D3D_SHADER_VARIABLE_CLASS / _TYPE）。
const (
	SVCScalar        = 0
	SVCVector        = 1
	SVCMatrixColumns = 3
	SVCStruct        = 5

	SVTBool  = 1
	SVTInt   = 2
	SVTFloat = 3
	SVTUint  = 19
)

// Member 是 RDEF 常量缓冲区变量：(名字, 偏移, 类, 类型, 行, 列, 元素数)。
type Member struct {
	Name     string
	Offset   int
	Class    int
	Type     int
	Rows     int
	Cols     int
	Elements int
}

// CB 是一个常量缓冲区的命名与布局。
type CB struct {
	Name    string
	Size    int
	Members []Member
}

// Names 是合成 RDEF 所需的全部命名；缺的项用回退名。
type Names struct {
	CBs        map[int]*CB // 槽位 → CB
	Textures   map[int]string
	Samplers   map[int]string
	Structured map[int]string
}

// Dcls 是 SHDR 声明解析结果。
type Dcls struct {
	Stage      int         // 0 像素，1 顶点
	Major      int         // shader model 主版本
	CBs        map[int]int // 槽位 → float4 行数
	Textures   map[int]int // 槽位 → SRV 维度
	Samplers   []int       // 声明顺序（可能重复）
	Structured map[int]int // 槽位 → stride
}

// dcl_resource 维度 → D3D_SRV_DIMENSION。
// 0 unknown 1 buffer 2 texture1d 3 texture2d 4 texture2dms 5 texture3d 6 texturecube
// 7 texture1darray 8 texture2darray 9 texture2dmsarray 10 texturecubearray
var dclDimToSRV = map[int]int{1: 1, 2: 2, 3: 4, 4: 6, 5: 8, 6: 9, 7: 3, 8: 5, 9: 7, 10: 10}

// Chunk 是一个容器块。
type Chunk struct {
	FourCC string
	Data   []byte
}

// ParseContainer 解析 DXBC 容器，返回 fourcc → 数据（同名取最后一个）。
func ParseContainer(b []byte) (map[string][]byte, error) {
	if len(b) < 32 || string(b[:4]) != "DXBC" {
		return nil, errors.New("不是 DXBC 容器")
	}
	n := int(le.Uint32(b[28:]))
	if 32+4*n > len(b) {
		return nil, errors.New("DXBC 块表截断")
	}
	chunks := map[string][]byte{}
	for i := range n {
		o := int(le.Uint32(b[32+4*i:]))
		if o+8 > len(b) {
			return nil, errors.New("DXBC 块越界")
		}
		size := int(le.Uint32(b[o+4:]))
		end := min(o+8+size, len(b))
		chunks[string(b[o:o+4])] = b[o+8 : end]
	}
	return chunks, nil
}

// ParseDcls 解析 SHDR/SHEX 的 dcl_* 声明。
func ParseDcls(shdr []byte) (*Dcls, error) {
	if len(shdr) < 8 {
		return nil, errors.New("SHDR 截断")
	}
	ver := le.Uint32(shdr)
	d := &Dcls{
		Stage: int(ver>>16) & 0xFFFF, Major: int(ver>>4) & 0xF,
		CBs: map[int]int{}, Textures: map[int]int{}, Structured: map[int]int{},
	}
	length := int(le.Uint32(shdr[4:]))
	end := min(length*4, len(shdr))
	u := func(p int) int {
		if p+4 > len(shdr) {
			return 0
		}
		return int(le.Uint32(shdr[p:]))
	}
	for p := 8; p+4 <= end; {
		tok := le.Uint32(shdr[p:])
		op := tok & 0x7FF
		olen := int(tok>>24) & 0x7F
		if op == 0x35 { // customdata（例如立即常量缓冲区）：长度在下一个 dword
			olen = u(p + 4)
		}
		if olen == 0 {
			break
		}
		switch op {
		case 0x59: // dcl_constantbuffer cbN[size]
			slot, size := u(p+8), u(p+12)
			d.CBs[slot] = max(d.CBs[slot], size)
		case 0x58: // dcl_resource
			dim := int(tok>>11) & 0x1F
			srv, ok := dclDimToSRV[dim]
			if !ok {
				srv = 4
			}
			d.Textures[u(p+8)] = srv
		case 0xA2: // dcl_resource_structured tN, stride
			d.Structured[u(p+8)] = u(p + 12)
		case 0x5A: // dcl_sampler
			d.Samplers = append(d.Samplers, u(p+8))
		}
		p += olen * 4
	}
	return d, nil
}

func sortedKeys(m map[int]int) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

type binding struct {
	name                          string
	typ, ret, dim, samples, point uint32
}

// BuildRDEF 合成 RDEF 块，逐字节与 hlslcc_bridge.build_rdef 相同。
func BuildRDEF(d *Dcls, names *Names) []byte {
	varDwords := 6
	if d.Major >= 5 {
		varDwords = 10 // SM5 的变量记录多 4 个 dword
	}
	cbName := func(slot int) string {
		if cb := names.CBs[slot]; cb != nil {
			return cb.Name
		}
		return fmt.Sprintf("cb%d", slot)
	}
	var binds []binding
	for _, slot := range sortedKeys(d.CBs) {
		binds = append(binds, binding{cbName(slot), 0, 0, 0, 0, uint32(slot)})
	}
	for _, slot := range sortedKeys(d.Textures) {
		nm, ok := names.Textures[slot]
		if !ok {
			nm = fmt.Sprintf("tex%d", slot)
		}
		binds = append(binds, binding{nm, 2, 5, uint32(d.Textures[slot]), 0xFFFFFFFF, uint32(slot)})
	}
	// StructuredBuffer<T>：绑定 + 一个 RESOURCE_BIND_INFO 类型的 cbuffer，内含由 float 组成的 $Element 结构。
	type sbuf struct {
		name   string
		stride int
	}
	var sbufs []sbuf
	for _, slot := range sortedKeys(d.Structured) {
		nm, ok := names.Structured[slot]
		if !ok {
			nm = fmt.Sprintf("sbuf%d", slot)
		}
		binds = append(binds, binding{nm, 5, 6, 1, uint32(d.Structured[slot]), uint32(slot)})
		sbufs = append(sbufs, sbuf{nm, d.Structured[slot]})
	}
	samplers := append([]int(nil), d.Samplers...)
	sort.Ints(samplers)
	for _, slot := range samplers {
		nm, ok := names.Samplers[slot]
		if !ok {
			nm = fmt.Sprintf("sampler%d", slot)
		}
		binds = append(binds, binding{nm, 3, 0, 0, 0, uint32(slot)})
	}

	type cbOut struct {
		name    string
		size    int
		members []Member
	}
	var cbs []cbOut
	for _, slot := range sortedKeys(d.CBs) {
		info := names.CBs[slot]
		nm := cbName(slot)
		size := d.CBs[slot] * 16
		var members []Member
		if info != nil {
			size = max(info.Size, d.CBs[slot]*16)
			members = append(members, info.Members...)
		}
		// 未覆盖的范围补 pad，让 SHDR 的每次读取都能解析到变量：整行空闲用 float4（数组），
		// 部分占用的行按 dword 补 float 标量（只放了 .zw 成员的行仍可能被按 .x 读）。
		dwords := (size + 3) / 4
		covered := make([]bool, dwords)
		for _, m := range members {
			var bytecount int
			switch {
			case m.Rows > 1:
				bytecount = max(1, m.Elements) * m.Rows * 16
			case m.Elements > 1:
				bytecount = (m.Elements-1)*16 + m.Cols*4
			default:
				bytecount = m.Cols * 4
			}
			for i := m.Offset / 4; i < min(dwords, (m.Offset+bytecount+3)/4); i++ {
				if i >= 0 {
					covered[i] = true
				}
			}
		}
		rowsTotal := (dwords + 3) / 4
		run := -1
		for r := 0; r <= rowsTotal; r++ {
			free := r < rowsTotal
			if free {
				for dw := r * 4; dw < min(dwords, r*4+4); dw++ {
					if covered[dw] {
						free = false
					}
				}
			}
			if free && run < 0 {
				run = r
			} else if !free && run >= 0 {
				elems := 0
				if r-run > 1 {
					elems = r - run
				}
				members = append(members, Member{fmt.Sprintf("pad_%s_%d", nm, run), run * 16, SVCVector, SVTFloat, 1, 4, elems})
				run = -1
			}
			if r < rowsTotal && !free {
				for dw := r * 4; dw < min(dwords, r*4+4); dw++ {
					if !covered[dw] {
						members = append(members, Member{fmt.Sprintf("pad_%s_d%d", nm, dw), dw * 4, SVCScalar, SVTFloat, 1, 1, 0})
					}
				}
			}
		}
		sort.SliceStable(members, func(i, j int) bool { return members[i].Offset < members[j].Offset })
		cbs = append(cbs, cbOut{nm, size, members})
	}

	const hdrSize = 6 * 4
	bindOff := hdrSize
	bindSize := len(binds) * 8 * 4
	cbOff := bindOff + bindSize
	cbSize := (len(cbs) + len(sbufs)) * 6 * 4
	varOff := cbOff + cbSize
	nvars := 0
	for _, c := range cbs {
		nvars += len(c.members)
	}
	varSize := (nvars + len(sbufs)) * varDwords * 4
	typeOff := varOff + varSize
	typeSize := nvars * 16
	sbOff := typeOff + typeSize
	sbSize := 0
	for _, s := range sbufs {
		sbSize += 16 + 12*(s.stride/4)
	}
	scalarTypeOff := sbOff + sbSize
	strOff := scalarTypeOff
	if len(sbufs) > 0 {
		strOff += 16
	}

	var strblob []byte
	stroffs := map[string]int{}
	intern := func(name string) uint32 {
		if o, ok := stroffs[name]; ok {
			return uint32(o)
		}
		o := strOff + len(strblob)
		stroffs[name] = o
		strblob = append(strblob, name...)
		strblob = append(strblob, 0)
		return uint32(o)
	}
	var out []byte
	u32 := func(vs ...uint32) {
		for _, v := range vs {
			out = le.AppendUint32(out, v)
		}
	}
	u16 := func(vs ...uint16) {
		for _, v := range vs {
			out = le.AppendUint16(out, v)
		}
	}
	target := uint32(0xFFFF)
	if d.Stage != 0 {
		target = 0x0400
	}
	u32(uint32(len(cbs)+len(sbufs)), uint32(cbOff), uint32(len(binds)), uint32(bindOff), target, 0)
	for _, b := range binds {
		u32(intern(b.name), b.typ, b.ret, b.dim, b.samples, b.point, 1, 0)
	}
	vi := 0
	for _, c := range cbs {
		u32(intern(c.name), uint32(len(c.members)), uint32(varOff+vi*varDwords*4), uint32(c.size), 0, 0)
		vi += len(c.members)
	}
	for j, s := range sbufs {
		u32(intern(s.name), 1, uint32(varOff+(nvars+j)*varDwords*4), uint32(s.stride), 0, 3)
	}
	ti := 0
	for _, c := range cbs {
		for _, m := range c.members {
			per := m.Cols * 4
			if m.Rows > 1 {
				per = m.Rows * 16
			}
			bytecount := max(1, m.Elements) * per
			u32(intern(m.Name), uint32(m.Offset), uint32(bytecount), 2, uint32(typeOff+ti*16), 0)
			if varDwords == 10 {
				u32(0xFFFFFFFF, 0, 0xFFFFFFFF, 0)
			}
			ti++
		}
	}
	var sbTypes []int
	cursor := sbOff
	for _, s := range sbufs {
		sbTypes = append(sbTypes, cursor)
		cursor += 16 + 12*(s.stride/4)
	}
	for i, s := range sbufs {
		u32(intern("$Element"), 0, uint32(s.stride), 2, uint32(sbTypes[i]), 0)
		if varDwords == 10 {
			u32(0xFFFFFFFF, 0, 0xFFFFFFFF, 0)
		}
	}
	for _, c := range cbs {
		for _, m := range c.members {
			u16(uint16(m.Class), uint16(m.Type), uint16(m.Rows), uint16(m.Cols), uint16(m.Elements), 0)
			u32(0)
		}
	}
	for i, s := range sbufs {
		n := s.stride / 4
		u16(SVCStruct, 0, 1, uint16(n), 0, uint16(n))
		u32(uint32(sbTypes[i] + 16))
		for k := range n {
			u32(intern(fmt.Sprintf("f%d", k)), uint32(scalarTypeOff), uint32(4*k))
		}
	}
	if len(sbufs) > 0 {
		u16(SVCScalar, SVTFloat, 1, 1, 0, 0)
		u32(0)
	}
	if len(out) != strOff {
		panic(fmt.Sprintf("RDEF 布局计算错误：%d != %d", len(out), strOff))
	}
	return append(out, strblob...)
}

// Assemble 按顺序拼 DXBC 容器（校验和置零，HLSLcc 不校验）。
func Assemble(chunks []Chunk) []byte {
	n := len(chunks)
	hdrLen := 32 + 4*n
	var offs []uint32
	var blob []byte
	for _, c := range chunks {
		offs = append(offs, uint32(hdrLen+len(blob)))
		blob = append(blob, c.FourCC...)
		blob = le.AppendUint32(blob, uint32(len(c.Data)))
		blob = append(blob, c.Data...)
	}
	total := hdrLen + len(blob)
	out := append([]byte("DXBC"), make([]byte, 16)...)
	out = le.AppendUint32(out, 1)
	out = le.AppendUint32(out, uint32(total))
	out = le.AppendUint32(out, uint32(n))
	for _, o := range offs {
		out = le.AppendUint32(out, o)
	}
	return append(out, blob...)
}

// FullDXBC 在 Unity 程序代码（前缀 + 去反射 DXBC）里找到 DXBC，合成 RDEF 后拼回 HLSLcc 用的完整容器。
func FullDXBC(payload []byte, names *Names) ([]byte, error) {
	idx := bytes.Index(payload, []byte("DXBC"))
	if idx < 0 {
		return nil, errors.New("程序代码里没有 DXBC")
	}
	chunks, err := ParseContainer(payload[idx:])
	if err != nil {
		return nil, err
	}
	code := "SHEX"
	if _, ok := chunks["SHDR"]; ok {
		code = "SHDR"
	}
	shdr, ok := chunks[code]
	if !ok {
		return nil, errors.New("DXBC 没有 SHDR/SHEX")
	}
	dcls, err := ParseDcls(shdr)
	if err != nil {
		return nil, err
	}
	order := []Chunk{{"RDEF", BuildRDEF(dcls, names)}}
	for _, f := range []string{"ISGN", "OSGN", "OSG5", "PCSG", code} {
		if c, ok := chunks[f]; ok {
			order = append(order, Chunk{f, c})
		}
	}
	return Assemble(order), nil
}
