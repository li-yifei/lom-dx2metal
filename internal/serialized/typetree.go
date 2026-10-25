package serialized

import (
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
)

// 类型树元标志：节点读完后按 4 字节对齐。
const kAlignBytes = 0x4000

// Node 是类型树的一个节点（Unity TypeTreeNode）。
type Node struct {
	Level    int
	Type     string
	Name     string
	ByteSize int32
	MetaFlag uint32
	Children []*Node
}

func (n *Node) aligned() bool { return n.MetaFlag&kAlignBytes != 0 }

// Child 按名字取直接子节点。
func (n *Node) Child(name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// flatNode 是 JSON 里的扁平节点（按深度优先顺序）。
type flatNode struct {
	Level    int    `json:"level"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	ByteSize int32  `json:"byte_size"`
	MetaFlag uint32 `json:"meta_flag"`
}

// buildTree 把深度优先的扁平节点列表还原成树。
func buildTree(flat []flatNode) (*Node, error) {
	if len(flat) == 0 {
		return nil, errors.New("空类型树")
	}
	var stack []*Node
	var root *Node
	for _, f := range flat {
		n := &Node{Level: f.Level, Type: f.Type, Name: f.Name, ByteSize: f.ByteSize, MetaFlag: f.MetaFlag}
		for len(stack) > 0 && stack[len(stack)-1].Level >= n.Level {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			if root != nil {
				return nil, errors.New("类型树有多个根")
			}
			root = n
		} else {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, n)
		}
		stack = append(stack, n)
	}
	return root, nil
}

// Unity 2020.3 Player 构建不带类型树，Shader 的类型树从 UnityPy 对 2020.3.49f1 的 TPK 导出一次后内嵌
// （生成脚本见 gen/export_shader_typetree.py）。
//
//go:embed typetree_shader_2020_3.json
var shaderTreeJSON []byte

//go:embed common_strings.json
var commonStringsJSON []byte

var (
	shaderTreeOnce sync.Once
	shaderTree     *Node
	shaderTreeErr  error

	commonOnce    sync.Once
	commonStrings map[uint32]string
)

// ShaderTypeTree 返回内嵌的 Unity 2020.3 Shader(ClassID 48) 类型树。
func ShaderTypeTree() (*Node, error) {
	shaderTreeOnce.Do(func() {
		var doc struct {
			Nodes []flatNode `json:"nodes"`
		}
		if err := json.Unmarshal(shaderTreeJSON, &doc); err != nil {
			shaderTreeErr = err
			return
		}
		shaderTree, shaderTreeErr = buildTree(doc.Nodes)
	})
	return shaderTree, shaderTreeErr
}

func commonString(offset uint32) string {
	commonOnce.Do(func() {
		var m map[string]string
		_ = json.Unmarshal(commonStringsJSON, &m)
		commonStrings = make(map[uint32]string, len(m))
		for k, v := range m {
			o, _ := strconv.ParseUint(k, 10, 32)
			commonStrings[uint32(o)] = v
		}
	})
	if s, ok := commonStrings[offset]; ok {
		return s
	}
	return strconv.FormatUint(uint64(offset), 10)
}

// parseTypeTreeBlob 解析 SerializedFile v12+ 的类型树 blob（节点表 + 字符串缓冲），返回根节点与消耗的字节数。
func parseTypeTreeBlob(b []byte, order binary.ByteOrder, version uint32) (*Node, int, error) {
	if len(b) < 8 {
		return nil, 0, errors.New("类型树 blob 截断")
	}
	count := int(int32(order.Uint32(b)))
	strSize := int(int32(order.Uint32(b[4:])))
	nodeSize := 24
	if version >= 19 {
		nodeSize = 32
	}
	if count < 0 || strSize < 0 || 8+count*nodeSize+strSize > len(b) {
		return nil, 0, errors.New("类型树 blob 长度非法")
	}
	strbuf := b[8+count*nodeSize : 8+count*nodeSize+strSize]
	str := func(v uint32) string {
		if v&0x80000000 != 0 {
			return commonString(v & 0x7FFFFFFF)
		}
		if int(v) >= len(strbuf) {
			return ""
		}
		end := int(v)
		for end < len(strbuf) && strbuf[end] != 0 {
			end++
		}
		return string(strbuf[v:end])
	}
	flat := make([]flatNode, count)
	for i := range count {
		p := b[8+i*nodeSize:]
		flat[i] = flatNode{
			Level:    int(p[2]),
			Type:     str(order.Uint32(p[4:])),
			Name:     str(order.Uint32(p[8:])),
			ByteSize: int32(order.Uint32(p[12:])),
			MetaFlag: order.Uint32(p[20:]),
		}
	}
	root, err := buildTree(flat)
	return root, 8 + count*nodeSize + strSize, err
}

// ---------- 值模型 ----------

// Struct 是类型树里一个结构体的值，字段保持序列化顺序。
type Struct struct {
	Fields []Field
}

// Field 是结构体的一个字段。
type Field struct {
	Name  string
	Value any
}

// Pair 是 map 元素（first/second）。
type Pair struct {
	First, Second any
}

// Get 取字段值；不存在时返回 nil。
func (s *Struct) Get(name string) any {
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			return s.Fields[i].Value
		}
	}
	return nil
}

// Set 改已有字段的值；字段不存在时报错（不允许改变结构）。
func (s *Struct) Set(name string, v any) {
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			s.Fields[i].Value = v
			return
		}
	}
	panic("serialized: 字段不存在 " + name)
}

// S 取结构体字段。
func (s *Struct) S(name string) *Struct { v, _ := s.Get(name).(*Struct); return v }

// A 取数组字段。
func (s *Struct) A(name string) []any { v, _ := s.Get(name).([]any); return v }

// I 取整数字段。
func (s *Struct) I(name string) int64 {
	switch v := s.Get(name).(type) {
	case int64:
		return v
	case uint64:
		return int64(v)
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

// Str 取字符串字段。
func (s *Struct) Str(name string) string { v, _ := s.Get(name).(string); return v }

// Clone 深拷贝一个值。
func Clone(v any) any {
	switch x := v.(type) {
	case *Struct:
		c := &Struct{Fields: make([]Field, len(x.Fields))}
		for i, f := range x.Fields {
			c.Fields[i] = Field{f.Name, Clone(f.Value)}
		}
		return c
	case []any:
		c := make([]any, len(x))
		for i, e := range x {
			c[i] = Clone(e)
		}
		return c
	case []byte:
		return append([]byte(nil), x...)
	case Pair:
		return Pair{Clone(x.First), Clone(x.Second)}
	}
	return v
}

// ---------- 读 ----------

type ttReader struct {
	b     []byte
	pos   int
	order binary.ByteOrder
}

var errTrunc = errors.New("对象数据截断")

func (r *ttReader) need(n int) error {
	if n < 0 || r.pos+n > len(r.b) {
		return errTrunc
	}
	return nil
}

func (r *ttReader) align() { r.pos = (r.pos + 3) &^ 3 }

func isByteType(t string) bool { return t == "UInt8" || t == "SInt8" || t == "char" }

func (r *ttReader) prim(t string) (any, bool, error) {
	size := 0
	switch t {
	case "SInt8", "UInt8", "char", "bool":
		size = 1
	case "short", "SInt16", "unsigned short", "UInt16":
		size = 2
	case "int", "SInt32", "unsigned int", "UInt32", "Type*", "float":
		size = 4
	case "long long", "SInt64", "unsigned long long", "UInt64", "FileSize", "double":
		size = 8
	default:
		return nil, false, nil
	}
	if err := r.need(size); err != nil {
		return nil, true, err
	}
	p := r.b[r.pos:]
	r.pos += size
	switch t {
	case "SInt8":
		return int64(int8(p[0])), true, nil
	case "UInt8", "char":
		return int64(p[0]), true, nil
	case "bool":
		return p[0] != 0, true, nil
	case "short", "SInt16":
		return int64(int16(r.order.Uint16(p))), true, nil
	case "unsigned short", "UInt16":
		return int64(r.order.Uint16(p)), true, nil
	case "int", "SInt32":
		return int64(int32(r.order.Uint32(p))), true, nil
	case "unsigned int", "UInt32", "Type*":
		return int64(r.order.Uint32(p)), true, nil
	case "float":
		return math.Float32frombits(r.order.Uint32(p)), true, nil
	case "long long", "SInt64":
		return int64(r.order.Uint64(p)), true, nil
	case "unsigned long long", "UInt64", "FileSize":
		return r.order.Uint64(p), true, nil
	case "double":
		return math.Float64frombits(r.order.Uint64(p)), true, nil
	}
	return nil, false, nil
}

func (r *ttReader) count() (int, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	n := int(int32(r.order.Uint32(r.b[r.pos:])))
	r.pos += 4
	if n < 0 || n > len(r.b) {
		return 0, fmt.Errorf("数组长度非法 %d", n)
	}
	return n, nil
}

func (r *ttReader) value(n *Node) (any, error) {
	align := n.aligned()
	var v any
	if pv, ok, err := r.prim(n.Type); ok {
		if err != nil {
			return nil, err
		}
		v = pv
	} else if n.Type == "string" || n.Type == "TypelessData" {
		size, err := r.count()
		if err != nil {
			return nil, err
		}
		if err := r.need(size); err != nil {
			return nil, err
		}
		raw := r.b[r.pos : r.pos+size]
		r.pos += size
		if n.Type == "string" {
			v = string(raw)
			if len(n.Children) > 0 && n.Children[0].aligned() {
				align = true
			}
		} else {
			v = append([]byte(nil), raw...)
		}
	} else if n.Type == "pair" {
		a, err := r.value(n.Children[0])
		if err != nil {
			return nil, err
		}
		b, err := r.value(n.Children[1])
		if err != nil {
			return nil, err
		}
		v = Pair{a, b}
	} else if len(n.Children) > 0 && n.Children[0].Type == "Array" {
		arr := n.Children[0]
		if arr.aligned() {
			align = true
		}
		size, err := r.count()
		if err != nil {
			return nil, err
		}
		elem := arr.Children[1]
		if isByteType(elem.Type) && !elem.aligned() {
			if err := r.need(size); err != nil {
				return nil, err
			}
			v = append([]byte(nil), r.b[r.pos:r.pos+size]...)
			r.pos += size
		} else {
			list := make([]any, size)
			for i := range size {
				if list[i], err = r.value(elem); err != nil {
					return nil, err
				}
			}
			v = list
		}
	} else {
		s := &Struct{Fields: make([]Field, len(n.Children))}
		for i, c := range n.Children {
			cv, err := r.value(c)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", c.Name, err)
			}
			s.Fields[i] = Field{c.Name, cv}
		}
		v = s
	}
	if align {
		r.align()
	}
	return v, nil
}

// ReadTree 按类型树解析一个对象的字节；要求恰好读完。
func ReadTree(root *Node, data []byte, order binary.ByteOrder) (*Struct, error) {
	r := &ttReader{b: data, order: order}
	v, err := r.value(root)
	if err != nil {
		return nil, err
	}
	if r.pos != len(data) {
		return nil, fmt.Errorf("类型树只读了 %d/%d 字节", r.pos, len(data))
	}
	s, ok := v.(*Struct)
	if !ok {
		return nil, errors.New("根节点不是结构体")
	}
	return s, nil
}

// ---------- 写 ----------

type ttWriter struct {
	b     []byte
	order binary.AppendByteOrder
}

func (w *ttWriter) align() {
	for len(w.b)%4 != 0 {
		w.b = append(w.b, 0)
	}
}

func asInt(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case uint64:
		return int64(x), nil
	case int:
		return int64(x), nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("期望整数，得到 %T", v)
}

func (w *ttWriter) prim(t string, v any) (bool, error) {
	switch t {
	case "SInt8", "UInt8", "char":
		i, err := asInt(v)
		w.b = append(w.b, byte(i))
		return true, err
	case "bool":
		b, _ := v.(bool)
		if i, ok := v.(int64); ok {
			b = i != 0
		}
		if b {
			w.b = append(w.b, 1)
		} else {
			w.b = append(w.b, 0)
		}
		return true, nil
	case "short", "SInt16", "unsigned short", "UInt16":
		i, err := asInt(v)
		w.b = w.order.AppendUint16(w.b, uint16(i))
		return true, err
	case "int", "SInt32", "unsigned int", "UInt32", "Type*":
		i, err := asInt(v)
		w.b = w.order.AppendUint32(w.b, uint32(i))
		return true, err
	case "float":
		f, ok := v.(float32)
		if !ok {
			return true, fmt.Errorf("期望 float32，得到 %T", v)
		}
		w.b = w.order.AppendUint32(w.b, math.Float32bits(f))
		return true, nil
	case "long long", "SInt64", "unsigned long long", "UInt64", "FileSize":
		i, err := asInt(v)
		w.b = w.order.AppendUint64(w.b, uint64(i))
		return true, err
	case "double":
		f, ok := v.(float64)
		if !ok {
			return true, fmt.Errorf("期望 float64，得到 %T", v)
		}
		w.b = w.order.AppendUint64(w.b, math.Float64bits(f))
		return true, nil
	}
	return false, nil
}

func (w *ttWriter) value(n *Node, v any) error {
	align := n.aligned()
	if ok, err := w.prim(n.Type, v); ok {
		if err != nil {
			return fmt.Errorf("%s: %w", n.Name, err)
		}
	} else if n.Type == "string" {
		s, _ := v.(string)
		w.b = w.order.AppendUint32(w.b, uint32(len(s)))
		w.b = append(w.b, s...)
		if len(n.Children) > 0 && n.Children[0].aligned() {
			align = true
		}
	} else if n.Type == "TypelessData" {
		b, _ := v.([]byte)
		w.b = w.order.AppendUint32(w.b, uint32(len(b)))
		w.b = append(w.b, b...)
	} else if n.Type == "pair" {
		p, ok := v.(Pair)
		if !ok {
			return fmt.Errorf("%s: 期望 Pair", n.Name)
		}
		if err := w.value(n.Children[0], p.First); err != nil {
			return err
		}
		if err := w.value(n.Children[1], p.Second); err != nil {
			return err
		}
	} else if len(n.Children) > 0 && n.Children[0].Type == "Array" {
		arr := n.Children[0]
		if arr.aligned() {
			align = true
		}
		elem := arr.Children[1]
		switch list := v.(type) {
		case []byte:
			w.b = w.order.AppendUint32(w.b, uint32(len(list)))
			w.b = append(w.b, list...)
		case []any:
			w.b = w.order.AppendUint32(w.b, uint32(len(list)))
			for _, e := range list {
				if err := w.value(elem, e); err != nil {
					return err
				}
			}
		case nil:
			w.b = w.order.AppendUint32(w.b, 0)
		default:
			return fmt.Errorf("%s: 期望数组，得到 %T", n.Name, v)
		}
	} else {
		s, ok := v.(*Struct)
		if !ok {
			return fmt.Errorf("%s: 期望结构体，得到 %T", n.Name, v)
		}
		if len(s.Fields) != len(n.Children) {
			return fmt.Errorf("%s: 字段数 %d 与类型树 %d 不符", n.Name, len(s.Fields), len(n.Children))
		}
		for i, c := range n.Children {
			if s.Fields[i].Name != c.Name {
				return fmt.Errorf("%s: 字段 %s 与类型树 %s 不符", n.Name, s.Fields[i].Name, c.Name)
			}
			if err := w.value(c, s.Fields[i].Value); err != nil {
				return err
			}
		}
	}
	if align {
		w.align()
	}
	return nil
}

// WriteTree 按类型树把值编码回字节。
func WriteTree(root *Node, v *Struct, order binary.AppendByteOrder) ([]byte, error) {
	w := &ttWriter{order: order}
	if err := w.value(root, v); err != nil {
		return nil, err
	}
	return w.b, nil
}
