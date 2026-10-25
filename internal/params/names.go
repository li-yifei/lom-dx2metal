package params

import (
	"fmt"
	"strings"

	"lom-dx2metal/internal/dxbc"
	"lom-dx2metal/internal/shaderblob"
)

// unityTypeToSVT：Unity 参数 m_Type（0 float、1 int、2 bool）→ D3D 变量类型。
var unityTypeToSVT = map[int]int{0: dxbc.SVTFloat, 1: dxbc.SVTInt, 2: dxbc.SVTBool}

func svt(t int) int {
	if v, ok := unityTypeToSVT[t]; ok {
		return v
	}
	return dxbc.SVTFloat
}

// BuildNames 从合并后的参数表得到 RDEF 命名（hlslcc_bridge.build_names）。
func BuildNames(names NameIndices, common *Params) *dxbc.Names {
	idx2name := names.ByIndex()
	nameOr := func(i int, fallback string) string {
		if n, ok := idx2name[i]; ok {
			return n
		}
		return fallback
	}
	out := &dxbc.Names{CBs: map[int]*dxbc.CB{}, Textures: map[int]string{}, Samplers: map[int]string{}, Structured: map[int]string{}}
	cbdefs := map[int]*dxbc.CB{}
	for _, cb := range common.CBs {
		var members []dxbc.Member
		for _, vp := range cb.Vector {
			dim := max(1, vp.Dim)
			class := dxbc.SVCVector
			if dim == 1 {
				class = dxbc.SVCScalar
			}
			members = append(members, dxbc.Member{Name: nameOr(vp.NameIndex, fmt.Sprintf("v%d", vp.NameIndex)),
				Offset: vp.Index, Class: class, Type: svt(vp.Type), Rows: 1, Cols: dim, Elements: vp.ArraySize})
		}
		for _, mp := range cb.Matrix {
			rows := mp.RowCount
			if rows == 0 {
				rows = 4
			}
			members = append(members, dxbc.Member{Name: nameOr(mp.NameIndex, fmt.Sprintf("m%d", mp.NameIndex)),
				Offset: mp.Index, Class: dxbc.SVCMatrixColumns, Type: svt(mp.Type), Rows: rows, Cols: 4, Elements: mp.ArraySize})
		}
		// Unity 把一个 cbuffer 拆在 m_CommonParameters（m_IsPartialCB）、变体 m_Parameters 与 legacy tail：取并集。
		if prev, ok := cbdefs[cb.NameIndex]; ok {
			seen := map[string]bool{}
			for _, m := range prev.Members {
				seen[m.Name] = true
			}
			for _, m := range members {
				if !seen[m.Name] {
					prev.Members = append(prev.Members, m)
				}
			}
			prev.Size = max(prev.Size, cb.Size)
			continue
		}
		cbdefs[cb.NameIndex] = &dxbc.CB{Name: nameOr(cb.NameIndex, fmt.Sprintf("cb_%d", cb.NameIndex)), Size: cb.Size, Members: members}
	}
	for _, b := range common.CBBindings {
		if cb, ok := cbdefs[b.NameIndex]; ok {
			out.CBs[b.Index] = cb
		}
	}
	for _, tp := range common.Texture {
		tex := nameOr(tp.NameIndex, fmt.Sprintf("tex%d", tp.Index))
		out.Textures[tp.Index] = tex
		if si := tp.SamplerIndex; si >= 0 && uint32(si) != 0xFFFFFFFF {
			if _, ok := out.Samplers[si]; !ok {
				out.Samplers[si] = "sampler" + tex
			}
		}
	}
	return out
}

// Offsets 是每个 CB 槽位的 成员名 → Unity 偏移，以及 CB 大小（relayout 用）。
type Offsets map[int]CBOffsets

// CBOffsets 是一个 CB 的偏移表。
type CBOffsets struct {
	Members map[string]int
	Size    int
}

// NamesOffsets 收集一个子程序完整的常量/纹理命名（names.names_offsets）：
// pass 级 m_CommonParameters、变体 m_Parameters 与程序 blob 里 legacy tail 三处合并后再合成 RDEF。
func NamesOffsets(passNames NameIndices, common, variant *Params, d3dTail *shaderblob.Tail) (*dxbc.Names, Offsets, error) {
	merged := Merge(common, variant)
	nameCopy := append(NameIndices(nil), passNames...)
	legacy, err := FromLegacyTail(&nameCopy, d3dTail)
	if err != nil {
		return nil, nil, err
	}
	merged = Merge(merged, legacy)
	names := BuildNames(nameCopy, merged)
	offsets := Offsets{}
	for slot, cb := range names.CBs {
		m := map[string]int{}
		for _, mem := range cb.Members {
			m[mem.Name] = mem.Offset
		}
		offsets[slot] = CBOffsets{m, cb.Size}
	}
	return names, offsets, nil
}

// Unity 把内联采样器状态（HLSL 里的 "sampler_LinearClamp"）打包进 SamplerParameter.sampler：
// bit0-1 过滤（0 point、1 linear、2 trilinear），之后 U、V、W 各 2 位寻址（0 repeat、1 clamp、2 mirror、3 mirror-once）。
// 本游戏用到 85（LinearClamp）与 84（PointClamp）。
var (
	filters = []string{"Point", "Linear", "Trilinear"}
	wraps   = []string{"Repeat", "Clamp", "Mirror", "MirrorOnce"}
)

// InlineSamplerName 返回内联采样器状态对应的 HLSLcc 名字；无法命名时返回 ""。
//
// 根因三（Story 场景色带）：HLSLcc（toMetalDeclaration.cpp 的 EmitInlineSampler）只有在 RDEF 里采样器名字写明了
// 过滤与寻址（如 sampler_LinearClamp）时，才会生成 `constexpr sampler name(filter::..., address::...)`，
// 与 Unity 自己的 Metal 产出一致。否则得到普通的 [[sampler(n)]]，Unity 不会给它填内联状态，于是按默认
// （point）过滤采样：URP 调色 LUT 每通道量化到 32 级，Story 场景出现色带、暗部发死。
// 所以内联采样器必须以这个名字进入 RDEF、保留为 constexpr sampler，并在 legacy tail 里写 kind 4。
func InlineSamplerName(state uint32) string {
	if state&^0xFF != 0 || state&3 == 3 {
		return ""
	}
	w := []string{wraps[(state>>2)&3], wraps[(state>>4)&3], wraps[(state>>6)&3]}
	var wrap string
	if w[0] == w[1] && w[1] == w[2] {
		wrap = w[0]
	} else {
		var b strings.Builder
		for i, axis := range []string{"U", "V", "W"} {
			b.WriteString(w[i] + axis)
		}
		wrap = b.String()
	}
	return "sampler_" + filters[state&3] + wrap
}
