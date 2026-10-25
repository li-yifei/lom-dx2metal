// Package params 是 Unity 2020.3 SerializedProgramParameters（常量/纹理/采样器参数表）的 Go 模型，
// 以及从参数表拼出 RDEF 所需命名的逻辑（移植 names.py 的 merge_parameters/names_offsets、
// hlslcc_bridge.py 的 build_names/inline_sampler_name、shader_blob.py 的 legacy_constant_parameters）。
package params

import (
	"fmt"

	"github.com/li-yifei/lom-dx2metal/internal/serialized"
	"github.com/li-yifei/lom-dx2metal/internal/shaderblob"
)

// VectorParam 是向量/标量参数（m_VectorParams）。
type VectorParam struct {
	NameIndex, Index, ArraySize, Type, Dim int
}

// MatrixParam 是矩阵参数（m_MatrixParams）。
type MatrixParam struct {
	NameIndex, Index, ArraySize, Type, RowCount int
}

// TextureParam 是纹理参数（m_TextureParams）。
type TextureParam struct {
	NameIndex, Index, SamplerIndex int
	MultiSampled                   bool
	Dim                            int
}

// BufferBinding 是 m_BufferParams / m_ConstantBufferBindings 的元素。
type BufferBinding struct {
	NameIndex, Index, ArraySize int
}

// UAVParam 是 m_UAVParams 的元素。
type UAVParam struct {
	NameIndex, Index, OriginalIndex int
}

// SamplerParam 是 m_Samplers 的元素：sampler 为打包的内联采样器状态，bindPoint 为 D3D 槽位。
type SamplerParam struct {
	Sampler   uint32
	BindPoint int
}

// ConstantBuffer 是 m_ConstantBuffers 的元素。StructParams 不参与命名，原样保留序列化值。
type ConstantBuffer struct {
	NameIndex    int
	Matrix       []MatrixParam
	Vector       []VectorParam
	StructParams []any
	Size         int
	IsPartialCB  bool
}

// Params 是一份完整的参数表。
type Params struct {
	Vector     []VectorParam
	Matrix     []MatrixParam
	Texture    []TextureParam
	Buffer     []BufferBinding
	CBs        []ConstantBuffer
	CBBindings []BufferBinding
	UAV        []UAVParam
	Samplers   []SamplerParam
}

// Clone 深拷贝。
func (p *Params) Clone() *Params {
	c := &Params{
		Vector:     append([]VectorParam(nil), p.Vector...),
		Matrix:     append([]MatrixParam(nil), p.Matrix...),
		Texture:    append([]TextureParam(nil), p.Texture...),
		Buffer:     append([]BufferBinding(nil), p.Buffer...),
		CBBindings: append([]BufferBinding(nil), p.CBBindings...),
		UAV:        append([]UAVParam(nil), p.UAV...),
		Samplers:   append([]SamplerParam(nil), p.Samplers...),
	}
	for _, cb := range p.CBs {
		c.CBs = append(c.CBs, cb.clone())
	}
	return c
}

func (cb ConstantBuffer) clone() ConstantBuffer {
	cb.Matrix = append([]MatrixParam(nil), cb.Matrix...)
	cb.Vector = append([]VectorParam(nil), cb.Vector...)
	cb.StructParams = append([]any(nil), cb.StructParams...)
	return cb
}

// ---------- 与序列化值互转 ----------

func ints(s *serialized.Struct, names ...string) []int {
	out := make([]int, len(names))
	for i, n := range names {
		out[i] = int(s.I(n))
	}
	return out
}

func structs(v []any) []*serialized.Struct {
	out := make([]*serialized.Struct, 0, len(v))
	for _, e := range v {
		if s, ok := e.(*serialized.Struct); ok {
			out = append(out, s)
		}
	}
	return out
}

func vectors(v []any) []VectorParam {
	var out []VectorParam
	for _, s := range structs(v) {
		x := ints(s, "m_NameIndex", "m_Index", "m_ArraySize", "m_Type", "m_Dim")
		out = append(out, VectorParam{x[0], x[1], x[2], x[3], x[4]})
	}
	return out
}

func matrices(v []any) []MatrixParam {
	var out []MatrixParam
	for _, s := range structs(v) {
		x := ints(s, "m_NameIndex", "m_Index", "m_ArraySize", "m_Type", "m_RowCount")
		out = append(out, MatrixParam{x[0], x[1], x[2], x[3], x[4]})
	}
	return out
}

func bindings(v []any) []BufferBinding {
	var out []BufferBinding
	for _, s := range structs(v) {
		x := ints(s, "m_NameIndex", "m_Index", "m_ArraySize")
		out = append(out, BufferBinding{x[0], x[1], x[2]})
	}
	return out
}

// FromStruct 把序列化的 SerializedProgramParameters 转成 Params。
func FromStruct(s *serialized.Struct) *Params {
	p := &Params{}
	if s == nil {
		return p
	}
	p.Vector = vectors(s.A("m_VectorParams"))
	p.Matrix = matrices(s.A("m_MatrixParams"))
	for _, t := range structs(s.A("m_TextureParams")) {
		x := ints(t, "m_NameIndex", "m_Index", "m_SamplerIndex", "m_Dim")
		p.Texture = append(p.Texture, TextureParam{x[0], x[1], x[2], t.I("m_MultiSampled") != 0, x[3]})
	}
	p.Buffer = bindings(s.A("m_BufferParams"))
	for _, c := range structs(s.A("m_ConstantBuffers")) {
		p.CBs = append(p.CBs, ConstantBuffer{
			NameIndex:    int(c.I("m_NameIndex")),
			Matrix:       matrices(c.A("m_MatrixParams")),
			Vector:       vectors(c.A("m_VectorParams")),
			StructParams: c.A("m_StructParams"),
			Size:         int(c.I("m_Size")),
			IsPartialCB:  c.I("m_IsPartialCB") != 0,
		})
	}
	p.CBBindings = bindings(s.A("m_ConstantBufferBindings"))
	for _, u := range structs(s.A("m_UAVParams")) {
		x := ints(u, "m_NameIndex", "m_Index", "m_OriginalIndex")
		p.UAV = append(p.UAV, UAVParam{x[0], x[1], x[2]})
	}
	for _, sm := range structs(s.A("m_Samplers")) {
		p.Samplers = append(p.Samplers, SamplerParam{uint32(sm.I("sampler")), int(sm.I("bindPoint"))})
	}
	return p
}

// fieldKeys 是 SerializedProgramParameters 的全部数组字段（metal_parameters.PARAMETER_FIELDS）。
var fieldKeys = []string{"m_VectorParams", "m_MatrixParams", "m_TextureParams", "m_BufferParams",
	"m_ConstantBuffers", "m_ConstantBufferBindings", "m_UAVParams", "m_Samplers"}

// ClearStruct 把一个序列化参数表清空（empty_parameters）：Metal 版的反射全部写在 legacy tail 里。
func ClearStruct(s *serialized.Struct) {
	for _, k := range fieldKeys {
		s.Set(k, []any{})
	}
}

// ---------- 名字表 ----------

// NameIndices 是 pass 的 m_NameIndices（名字 → 索引，保持顺序，允许重复名）。
type NameIndices []NamePair

// NamePair 是一项 (名字, 索引)。
type NamePair struct {
	Name  string
	Index int
}

// NameIndicesOf 从 pass 读 m_NameIndices。
func NameIndicesOf(pass *serialized.Struct) NameIndices {
	var out NameIndices
	for _, e := range pass.A("m_NameIndices") {
		p, ok := e.(serialized.Pair)
		if !ok {
			continue
		}
		name, _ := p.First.(string)
		idx, _ := p.Second.(int64)
		out = append(out, NamePair{name, int(idx)})
	}
	return out
}

// Index 返回名字的索引；不存在时追加 max+1（metal_parameters.name_index）。
func (n *NameIndices) Index(name string) int {
	found, have, maxIdx := 0, false, -1
	for _, p := range *n {
		if p.Name == name {
			found, have = p.Index, true // dict 语义：同名取最后一项
		}
		if p.Index > maxIdx {
			maxIdx = p.Index
		}
	}
	if have {
		return found
	}
	*n = append(*n, NamePair{name, maxIdx + 1})
	return maxIdx + 1
}

// ByIndex 返回 索引 → 名字（同索引取最后一项）。
func (n NameIndices) ByIndex() map[int]string {
	m := make(map[int]string, len(n))
	for _, p := range n {
		m[p.Index] = p.Name
	}
	return m
}

// ---------- legacy tail → 参数表 ----------

// FromLegacyTail 把 D3D11 程序 legacy tail 里已验证的常量/纹理/采样器映射成参数表
// （shader_blob.legacy_constant_parameters）。Unity 会把一部分变体常量只放在这里，即使 m_Parameters 为空。
func FromLegacyTail(names *NameIndices, tail *shaderblob.Tail) (*Params, error) {
	p := &Params{}
	for _, b := range tail.Buffers {
		if b.Name == "" {
			if len(b.Params) > 0 || b.Size != 0 {
				return nil, fmt.Errorf("无名非空 legacy 常量缓冲区需要验证布局")
			}
			continue
		}
		cb := ConstantBuffer{NameIndex: names.Index(b.Name), Size: int(b.Size), StructParams: []any{}}
		for _, m := range b.Params {
			// 0 = float，1 = int：StencilDeferred 的 _LightFlags（int）与原生 Mac Metal 参考的 unity_StereoEyeIndex（int）。
			if m.Type != 0 && m.Type != 1 {
				return nil, fmt.Errorf("legacy 标量类型不是 float/int，需要验证")
			}
			if m.Matrix != 0 {
				if m.Cols != 4 {
					return nil, fmt.Errorf("legacy 矩阵列数需要验证")
				}
				cb.Matrix = append(cb.Matrix, MatrixParam{names.Index(m.Name), int(m.Offset), int(m.ArraySize), int(m.Type), int(m.Rows)})
			} else {
				if m.Rows != 1 {
					return nil, fmt.Errorf("legacy 向量行数需要验证")
				}
				cb.Vector = append(cb.Vector, VectorParam{names.Index(m.Name), int(m.Offset), int(m.ArraySize), int(m.Type), int(m.Cols)})
			}
		}
		p.CBs = append(p.CBs, cb)
	}
	for _, b := range tail.Bindings {
		switch b.Kind {
		case shaderblob.KindConstBuffer:
			// BindCB(name, index) 忽略第三个字。
			p.CBBindings = append(p.CBBindings, BufferBinding{names.Index(b.Name), int(b.Index), 1})
		case shaderblob.KindTexture:
			if b.TextureFlags&^0xFF != 0 {
				return nil, fmt.Errorf("纹理标志高位未验证")
			}
			p.Texture = append(p.Texture, TextureParam{names.Index(b.Name), int(b.Index), int(int32(b.Aux)),
				b.TextureFlags&1 != 0, int((b.TextureFlags >> 1) & 127)})
		case shaderblob.KindStructured:
			p.Buffer = append(p.Buffer, BufferBinding{names.Index(b.Name), int(b.Index), 0})
		case shaderblob.KindInlineSampler:
			// AddSamplerParam(InlineSamplerType, bindPoint)，见 0x654550。
			if b.Name != "" {
				return nil, fmt.Errorf("内联采样器不应有名字")
			}
			p.Samplers = append(p.Samplers, SamplerParam{b.Aux, int(b.Index)})
		}
	}
	return p, nil
}

// ---------- 合并 ----------

// sig 是 merge_parameters 去重用的签名 (m_NameIndex, m_Index, bindPoint, sampler)；缺的字段为 nil。
type sig struct {
	nameIndex, index, bindPoint, sampler int64
	hasName, hasIndex, hasBind, hasSamp  bool
}

func nameIndexSig(name, index int) sig {
	return sig{nameIndex: int64(name), index: int64(index), hasName: true, hasIndex: true}
}

func mergeList[T any](dst []T, src []T, key func(T) sig) []T {
	seen := make(map[sig]bool, len(dst))
	for _, x := range dst {
		seen[key(x)] = true
	}
	for _, x := range src {
		k := key(x)
		if !seen[k] {
			dst = append(dst, x)
			seen[k] = true
		}
	}
	return dst
}

// Merge 把 variant 合并进 common 的副本（names.merge_parameters）。
//
// 根因二（角色贴图发暗）：同一个 cbuffer 会被拆在 pass 级 m_CommonParameters（m_IsPartialCB）、变体的 m_Parameters
// 与 legacy tail 三处。同名 CB 必须**按成员合并**；若按签名去重，变体自己的成员会被丢掉，变成匿名 pad，
// 读到的永远是 0（例如 _MainLightPosition / _UnityShadowSharpness），角色贴图因此发暗。
func Merge(common, variant *Params) *Params {
	m := common.Clone()
	m.Vector = mergeList(m.Vector, variant.Vector, func(v VectorParam) sig { return nameIndexSig(v.NameIndex, v.Index) })
	m.Matrix = mergeList(m.Matrix, variant.Matrix, func(v MatrixParam) sig { return nameIndexSig(v.NameIndex, v.Index) })
	m.Texture = mergeList(m.Texture, variant.Texture, func(v TextureParam) sig { return nameIndexSig(v.NameIndex, v.Index) })
	m.Buffer = mergeList(m.Buffer, variant.Buffer, func(v BufferBinding) sig { return nameIndexSig(v.NameIndex, v.Index) })
	// m_ConstantBuffers：同名按成员合并。by_name 以最后一个同名项为准（Python dict 语义）。
	byName := map[int]int{}
	for i, cb := range m.CBs {
		byName[cb.NameIndex] = i
	}
	for _, cb := range variant.CBs {
		i, ok := byName[cb.NameIndex]
		if !ok {
			m.CBs = append(m.CBs, cb.clone())
			byName[cb.NameIndex] = len(m.CBs) - 1
			continue
		}
		have := &m.CBs[i]
		have.Size = max(have.Size, cb.Size)
		vnames := map[int]bool{}
		for _, x := range have.Vector {
			vnames[x.NameIndex] = true
		}
		for _, x := range cb.Vector {
			if !vnames[x.NameIndex] {
				have.Vector = append(have.Vector, x)
			}
		}
		mnames := map[int]bool{}
		for _, x := range have.Matrix {
			mnames[x.NameIndex] = true
		}
		for _, x := range cb.Matrix {
			if !mnames[x.NameIndex] {
				have.Matrix = append(have.Matrix, x)
			}
		}
		snames := map[int64]bool{}
		for _, x := range have.StructParams {
			if s, ok := x.(*serialized.Struct); ok {
				snames[s.I("m_NameIndex")] = true
			}
		}
		for _, x := range cb.StructParams {
			if s, ok := x.(*serialized.Struct); ok && !snames[s.I("m_NameIndex")] {
				have.StructParams = append(have.StructParams, x)
			}
		}
	}
	m.CBBindings = mergeList(m.CBBindings, variant.CBBindings, func(v BufferBinding) sig { return nameIndexSig(v.NameIndex, v.Index) })
	m.UAV = mergeList(m.UAV, variant.UAV, func(v UAVParam) sig { return nameIndexSig(v.NameIndex, v.Index) })
	m.Samplers = mergeList(m.Samplers, variant.Samplers, func(v SamplerParam) sig {
		return sig{bindPoint: int64(v.BindPoint), sampler: int64(v.Sampler), hasBind: true, hasSamp: true}
	})
	return m
}
