package core

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/li-yifei/lom-dx2metal/internal/metal"
	"github.com/li-yifei/lom-dx2metal/internal/params"
	"github.com/li-yifei/lom-dx2metal/internal/shaderblob"
)

// 顶点输入语义 → Unity 顶点通道（metal_parameters.vertex_channels）。
var vertexSources = func() map[string]uint32 {
	m := map[string]uint32{"POSITION0": 0, "NORMAL0": 1, "TANGENT0": 2, "COLOR0": 3, "BLENDWEIGHT0": 12, "BLENDINDICES0": 13}
	for i := range 8 {
		m["TEXCOORD"+strconv.Itoa(i)] = uint32(4 + i)
	}
	return m
}()

func vertexChannels(inputs []metal.VertexInput) (shaderblob.Channels, error) {
	c := shaderblob.Channels{Channels: []shaderblob.Channel{}}
	for _, in := range inputs {
		source, ok := vertexSources[in.Name]
		if !ok || in.Index >= 16 {
			return c, fmt.Errorf("未验证的顶点输入 %s[%d]", in.Name, in.Index)
		}
		c.Channels = append(c.Channels, shaderblob.Channel{Source: source, Target: uint32(13 + in.Index)})
		c.SourceMap |= 1 << source
	}
	return c, nil
}

// Metal MTLTextureType → (Unity TextureDimension, 多重采样)。
var metalTextureDims = map[int]struct {
	dim uint32
	ms  bool
}{2: {2, false}, 3: {5, false}, 4: {2, true}, 5: {4, false}, 6: {6, false}, 7: {3, false}}

var (
	scalarTypes    = map[string]uint32{"float": 0, "int": 1}
	translatedRE   = regexp.MustCompile(`^hlslcc_mtx([234])x([234])(.+)$`)
	inlineSampleRE = regexp.MustCompile(`^(?:sampler_)?inlineSampler_(\d+)$`)
)

// legacyMember 把一个反射出的 Metal 结构体成员转成 legacy tail 成员。
func legacyMember(m metal.Member) (shaderblob.Member, error) {
	if _, ok := scalarTypes[m.Type]; m.HasMembers || !ok {
		return shaderblob.Member{}, fmt.Errorf("不支持的 Metal 常量成员 %s（%s）", m.Name, m.Type)
	}
	name, rows, cols := m.Name, m.Rows, m.Cols
	arraySize := 0
	if m.ArraySize != nil {
		arraySize = *m.ArraySize
	}
	if t := translatedRE.FindStringSubmatch(name); t != nil {
		rows, _ = strconv.Atoi(t[1])
		cols, _ = strconv.Atoi(t[2])
		name = t[3]
		arraySize /= rows
		if arraySize == 1 {
			arraySize = 0
		}
	}
	if rows > 1 && (rows != 4 || cols != 4) {
		return shaderblob.Member{}, fmt.Errorf("未验证的非 4x4 矩阵 %s", m.Name)
	}
	matrix := uint32(0)
	if rows > 1 {
		matrix = 1
	}
	return shaderblob.Member{Name: name, Type: scalarTypes[m.Type], Rows: uint32(rows), Cols: uint32(cols),
		Matrix: matrix, ArraySize: uint32(arraySize), Offset: uint32(m.Offset)}, nil
}

// legacyTailFromDriver 从驱动反射写出 Unity 2020.3 Metal 程序的完整 legacy tail（metal_parameters.legacy_tail_from_driver）。
//
// 根因一：这就是 Unity Mac 版 Player 自己的输出格式（以 GUI/Text Shader 为参考）——开头一个无名空缓冲区，
// 每个反射出的常量缓冲区一项；绑定依次为常量缓冲区（kind 1）、纹理（kind 0，aux = 采样器槽位或 -1，
// flags = dim<<1 | 多重采样）、内联采样器（kind 4，index = 槽位，aux = 原内联采样器状态）。
// 常量缓冲区成员去掉 hlslcc_mtx4x4 前缀、矩阵 flag=1；StructuredBuffer 写 kind 2，aux 取自 D3D tail。
func legacyTailFromDriver(args []metal.Argument, channels shaderblob.Channels, inline []params.SamplerParam,
	structured map[string]uint32) (*shaderblob.Tail, error) {
	// samplers：名字 → 槽位，保持首次出现顺序、值取最后一次（Python dict 语义）。
	var samplerNames []string
	samplers := map[string]int{}
	for _, a := range args {
		if a.Kind == "sampler" {
			if _, ok := samplers[a.Name]; !ok {
				samplerNames = append(samplerNames, a.Name)
			}
			samplers[a.Name] = a.Index
		}
	}
	unknown, hasUnknown := samplers["samplerUnknownResource0"]
	originalInline := map[int]uint32{}
	for _, s := range inline {
		originalInline[s.BindPoint] = s.Sampler
	}
	texSamplerNames := map[string]bool{}
	for _, a := range args {
		if a.Kind == "texture" {
			texSamplerNames["sampler"+a.Name] = true
			texSamplerNames["sampler_"+a.Name] = true
		}
	}
	tail := &shaderblob.Tail{
		Channels: shaderblob.Channels{SourceMap: channels.SourceMap, Channels: channels.Channels},
		Buffers:  []shaderblob.Buffer{{}},
	}
	if tail.Channels.Channels == nil {
		tail.Channels.Channels = []shaderblob.Channel{}
	}
	var cbBindings, texBindings, samplerBindings []shaderblob.Binding
	for _, name := range samplerNames {
		index := samplers[name]
		if m := inlineSampleRE.FindStringSubmatch(name); m != nil {
			bp, _ := strconv.Atoi(m[1])
			state, ok := originalInline[bp]
			if !ok {
				return nil, fmt.Errorf("内联采样器 %s 缺少原始状态", name)
			}
			samplerBindings = append(samplerBindings, shaderblob.Binding{Kind: shaderblob.KindInlineSampler, Index: uint32(index), Aux: state})
		} else if state, ok := originalInline[index]; ok && !texSamplerNames[name] {
			// HLSLcc 按 D3D 寄存器给内联采样器命名（例如 sampler0）时，按槽位找回原状态写 kind 4。
			samplerBindings = append(samplerBindings, shaderblob.Binding{Kind: shaderblob.KindInlineSampler, Index: uint32(index), Aux: state})
		}
	}
	for _, a := range args {
		switch a.Kind {
		case "buffer":
			if strings.HasPrefix(a.Name, "vertexBuffer.") {
				continue
			}
			name := strings.TrimSuffix(a.Name, "_Type")
			if aux, ok := structured[name]; ok {
				cbBindings = append(cbBindings, shaderblob.Binding{Name: name, Kind: shaderblob.KindStructured, Index: uint32(a.Index), Aux: aux})
				continue
			}
			buf := shaderblob.Buffer{Name: name, Size: uint32(a.Size)}
			for _, m := range a.Members {
				if strings.HasPrefix(m.Name, "pad_fill") {
					continue
				}
				lm, err := legacyMember(m)
				if err != nil {
					return nil, err
				}
				buf.Params = append(buf.Params, lm)
			}
			tail.Buffers = append(tail.Buffers, buf)
			cbBindings = append(cbBindings, shaderblob.Binding{Name: name, Kind: shaderblob.KindConstBuffer, Index: uint32(a.Index)})
		case "texture":
			dims, ok := metalTextureDims[a.TextureType]
			if !ok {
				return nil, fmt.Errorf("未验证的纹理类型 %d（%s）", a.TextureType, a.Name)
			}
			sampler := -1
			if i, ok := samplers["sampler"+a.Name]; ok {
				sampler = i
			} else if i, ok := samplers["sampler_"+a.Name]; ok {
				sampler = i
			} else if hasUnknown {
				// 部分 URP 10 DXBC 的反射里缺采样器源名，HLSLcc 给这些 LUT pass 的曲线纹理共用一个采样器。
				sampler = unknown
			}
			flags := dims.dim << 1
			if dims.ms {
				flags |= 1
			}
			texBindings = append(texBindings, shaderblob.Binding{Name: a.Name, Kind: shaderblob.KindTexture,
				Index: uint32(a.Index), Aux: uint32(int32(sampler)), TextureFlags: flags})
		}
	}
	tail.Bindings = append(append(cbBindings, texBindings...), samplerBindings...)
	return tail, nil
}
