package convert

import (
	"context"
	"fmt"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"strings"

	"lom-dx2metal/internal/dxbc"
	"lom-dx2metal/internal/hlslcc"
	"lom-dx2metal/internal/metal"
	"lom-dx2metal/internal/params"
	"lom-dx2metal/internal/relayout"
	"lom-dx2metal/internal/serialized"
	"lom-dx2metal/internal/shaderblob"
)

// 平台号。
const (
	platformD3D11 = 4
	platformMetal = 14
)

// 处理的阶段与对应的 Metal 程序类型（23 顶点、24 片元）。
var stageKeys = []string{"progVertex", "progFragment"}
var stageType = map[string]uint32{"progVertex": 23, "progFragment": 24}

// 不支持的阶段：其变体记入 audit 并删除。
var otherStages = []string{"progGeometry", "progHull", "progDomain", "progRayTracing"}

// D3D11 程序类型：15/16 顶点，17/18 片元。
var vsTypes = map[int64]bool{15: true, 16: true}
var fsTypes = map[int64]bool{17: true, 18: true}

// 读取 pad_* 成员说明引擎从不写入的常量（永远是 0），是丢成员的迹象；全部程序里必须为 0。
var padReadRE = regexp.MustCompile(`\b[\w$]+\s*\.\s*pad_`)

// shaderReport 是单个 shader 的 audit 记录（字段与 Python 版 audit 相同）。
type shaderReport struct {
	Shader               string            `json:"shader"`
	Programs             int               `json:"programs"`
	Converted            int               `json:"converted"`
	RemovedSubprograms   int               `json:"removed_subprograms"`
	PadReads             int               `json:"pad_reads"`
	Failures             map[string]string `json:"failures"`
	Error                string            `json:"error,omitempty"`
	Asset                string            `json:"asset"`
	PathID               int64             `json:"path_id"`
	Seconds              float64           `json:"seconds"`
	Platforms            []int             `json:"platforms,omitempty"`
	TailReadbackMismatch []int             `json:"tail_readback_mismatch"`
}

// translation 是第 1 步（DXBC→MSL）的结果。
type translation struct {
	name       string
	programs   []*shaderblob.Program
	passes     []*serialized.Struct
	msl        map[int]string
	failures   map[int]string
	stageOf    map[int]string
	inline     map[int][]params.SamplerParam
	structured map[int]map[string]uint32
}

func (t *translation) fail(index int, msg string) {
	if _, ok := t.failures[index]; !ok {
		t.failures[index] = msg
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func shaderPasses(tree *serialized.Struct) []*serialized.Struct {
	var out []*serialized.Struct
	for _, ss := range tree.S("m_ParsedForm").A("m_SubShaders") {
		for _, p := range ss.(*serialized.Struct).A("m_Passes") {
			out = append(out, p.(*serialized.Struct))
		}
	}
	return out
}

func subPrograms(stage *serialized.Struct) []*serialized.Struct {
	if stage == nil {
		return nil
	}
	var out []*serialized.Struct
	for _, sp := range stage.A("m_SubPrograms") {
		out = append(out, sp.(*serialized.Struct))
	}
	return out
}

func samplerParams(s *serialized.Struct) []params.SamplerParam {
	if s == nil {
		return nil
	}
	return params.FromStruct(s).Samplers
}

// translateProgram 把一个 D3D11 子程序翻译成重排后的 MSL。任何 panic 都转成错误：单个程序失败只记 audit。
func translateProgram(passNames params.NameIndices, stageD, sp *serialized.Struct, program *shaderblob.Program,
	samplers []params.SamplerParam, tag string) (msl string, d3dTail *shaderblob.Tail, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("内部错误：%v\n%s", r, debug.Stack())
		}
	}()
	d3dTail, err = shaderblob.ParseTail(program.Parameters)
	if err != nil {
		return "", nil, err
	}
	names, offsets, err := params.NamesOffsets(passNames, params.FromStruct(stageD.S("m_CommonParameters")),
		params.FromStruct(sp.S("m_Parameters")), d3dTail)
	if err != nil {
		return "", nil, err
	}
	for _, s := range samplers {
		// 根因三：内联采样器以 sampler_<过滤><寻址> 进入 RDEF，HLSLcc 才会生成 constexpr sampler。
		if inline := params.InlineSamplerName(s.Sampler); inline != "" {
			names.Samplers[s.BindPoint] = inline
		}
	}
	for _, b := range d3dTail.Bindings {
		if b.Kind == shaderblob.KindStructured {
			names.Structured[int(b.Index)] = b.Name
		}
	}
	full, err := dxbc.FullDXBC(program.Code, names)
	if err != nil {
		return "", nil, err
	}
	msl, err = hlslcc.ToMetal(full, hlslcc.UnityFlags)
	if err != nil {
		return "", nil, err
	}
	if !fsTypes[sp.I("m_GpuProgramType")] {
		msl = postprocessVS(msl)
	}
	cbo := map[int]relayout.CBOffsets{}
	for slot, o := range offsets {
		cbo[slot] = relayout.CBOffsets{Members: o.Members, Size: o.Size}
	}
	msl, err = relayout.CBStructs(msl, cbo, tag)
	return msl, d3dTail, err
}

// postprocessVS 给顶点位置加 invariant（与 Unity 自己的 Metal 输出一致）。
func postprocessVS(msl string) string {
	return strings.ReplaceAll(msl, "[[ position ]]", "[[ position, invariant ]]")
}

// translateShader 是第 1 步：解包 D3D11 程序，把每个被引用的顶点/片元 blob 翻译成 MSL。
func translateShader(ctx context.Context, tree *serialized.Struct) (*translation, error) {
	t := &translation{
		name:       tree.S("m_ParsedForm").Str("m_Name"),
		msl:        map[int]string{},
		failures:   map[int]string{},
		stageOf:    map[int]string{},
		inline:     map[int][]params.SamplerParam{},
		structured: map[int]map[string]uint32{},
	}
	programs, err := shaderblob.UnpackShader(tree, platformD3D11)
	if err != nil {
		return nil, err
	}
	t.programs = programs
	t.passes = shaderPasses(tree)
	for _, pass := range t.passes {
		passNames := params.NameIndicesOf(pass)
		for _, stage := range otherStages {
			for _, sp := range subPrograms(pass.S(stage)) {
				t.fail(int(sp.I("m_BlobIndex")), "unsupported stage "+stage)
			}
		}
		for _, stage := range stageKeys {
			stageD := pass.S(stage)
			for _, sp := range subPrograms(stageD) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				index := int(sp.I("m_BlobIndex"))
				if _, ok := t.msl[index]; ok {
					continue
				}
				if _, ok := t.failures[index]; ok {
					continue
				}
				t.stageOf[index] = stage
				gpu := sp.I("m_GpuProgramType")
				if !vsTypes[gpu] && !fsTypes[gpu] {
					t.failures[index] = fmt.Sprintf("gpu program type %d", gpu)
					continue
				}
				if index >= len(programs) {
					t.failures[index] = "convert: blob 索引越界"
					continue
				}
				samplers := append(samplerParams(stageD.S("m_CommonParameters")), samplerParams(sp.S("m_Parameters"))...)
				msl, d3dTail, err := translateProgram(passNames, stageD, sp, programs[index], samplers,
					fmt.Sprintf("%s blob[%d]", t.name, index))
				if err != nil {
					t.failures[index] = "convert: " + truncate(err.Error(), 300)
					continue
				}
				t.msl[index] = msl
				t.inline[index] = samplers
				st := map[string]uint32{}
				for _, b := range d3dTail.Bindings {
					if b.Kind == shaderblob.KindStructured {
						st[b.Name] = b.Aux
					}
				}
				t.structured[index] = st
			}
		}
	}
	return t, nil
}

// keywordKey 与 bulk.keyword_key 相同。注意 Python 版读的是 m_KeywordIndices，2020.3 的类型树里没有这个字段
// （叫 m_GlobalKeywordIndices），所以全局关键字部分永远为空、只比较局部关键字；为了与 oracle 逐字节一致保留这一行为。
func keywordKey(sp *serialized.Struct) string {
	var local []int
	for _, v := range sp.A("m_LocalKeywordIndices") {
		local = append(local, int(v.(int64)))
	}
	sort.Ints(local)
	return fmt.Sprint(local)
}

type pair struct{ v, f int }

type reflected struct {
	kind   string // vertex | fragment
	result *metal.Reflection
}

// linkShader 是第 2 步：按 pass 给顶点/片元变体配对，交给 Metal 驱动编译链接并取反射。
// 每个程序得到一个按"同关键字优先"排序的候选伙伴列表；第 r 轮给每个还没链接成功的程序试第 r 个候选。
func linkShader(ctx context.Context, dev *metal.Device, t *translation) (map[int]reflected, error) {
	candidates := map[int][]pair{}
	var order []int // candidates 的插入顺序
	for _, pass := range t.passes {
		var vertices, fragments []*serialized.Struct
		for _, sp := range subPrograms(pass.S("progVertex")) {
			if _, ok := t.msl[int(sp.I("m_BlobIndex"))]; ok {
				vertices = append(vertices, sp)
			}
		}
		for _, sp := range subPrograms(pass.S("progFragment")) {
			if _, ok := t.msl[int(sp.I("m_BlobIndex"))]; ok {
				fragments = append(fragments, sp)
			}
		}
		for _, side := range []struct {
			own, others []*serialized.Struct
			ownIsVertex bool
		}{{vertices, fragments, true}, {fragments, vertices, false}} {
			for _, sp := range side.own {
				key := keywordKey(sp)
				ordered := slices.Clone(side.others)
				sort.SliceStable(ordered, func(i, j int) bool {
					return keywordKey(ordered[i]) == key && keywordKey(ordered[j]) != key
				})
				index := int(sp.I("m_BlobIndex"))
				if _, ok := candidates[index]; !ok {
					order = append(order, index)
					candidates[index] = nil
				}
				for _, o := range ordered {
					p := pair{index, int(o.I("m_BlobIndex"))}
					if !side.ownIsVertex {
						p = pair{int(o.I("m_BlobIndex")), index}
					}
					if !slices.Contains(candidates[index], p) {
						candidates[index] = append(candidates[index], p)
					}
				}
			}
		}
	}
	libs := map[int]*metal.Library{}
	libErr := map[int]string{}
	defer func() {
		for _, l := range libs {
			l.Release()
		}
	}()
	library := func(index int) (*metal.Library, string) {
		if l, ok := libs[index]; ok {
			return l, ""
		}
		if e, ok := libErr[index]; ok {
			return nil, e
		}
		l, err := dev.Compile(t.msl[index])
		if err != nil {
			libErr[index] = err.Error()
			return nil, err.Error()
		}
		libs[index] = l
		return l, ""
	}
	reflections := map[int]reflected{}
	var reflOrder []int
	lastError := map[int]string{}
	for round := 0; round < 12; round++ {
		type entry struct {
			index int
			p     pair
		}
		var batch []entry
		for _, index := range order {
			if _, ok := reflections[index]; ok {
				continue
			}
			if round < len(candidates[index]) {
				batch = append(batch, entry{index, candidates[index][round]})
			}
		}
		if len(batch) == 0 {
			break
		}
		results := map[pair]*metal.Reflection{}
		for _, e := range batch {
			if _, ok := results[e.p]; ok {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			vl, verr := library(e.p.v)
			var fl *metal.Library
			ferr := ""
			if vl != nil {
				fl, ferr = library(e.p.f)
			}
			switch {
			case vl == nil:
				results[e.p] = &metal.Reflection{Error: verr}
			case fl == nil:
				results[e.p] = &metal.Reflection{Error: ferr}
			default:
				r, err := dev.ReflectPair(vl, fl)
				if err != nil {
					r = &metal.Reflection{Error: err.Error()}
				}
				results[e.p] = r
			}
		}
		for _, e := range batch {
			r := results[e.p]
			if r.Linked {
				if _, ok := reflections[e.p.v]; !ok {
					reflections[e.p.v] = reflected{"vertex", r}
					reflOrder = append(reflOrder, e.p.v)
				}
				if _, ok := reflections[e.p.f]; !ok {
					reflections[e.p.f] = reflected{"fragment", r}
					reflOrder = append(reflOrder, e.p.f)
				}
			} else {
				lastError[e.index] = truncate(r.Error, 300)
			}
		}
	}
	for index := range t.msl {
		if _, ok := reflections[index]; !ok {
			msg, ok := lastError[index]
			if !ok {
				msg = "no partner program in pass"
			}
			t.fail(index, "link: "+msg)
		}
	}
	return reflections, nil
}

// convertShader 转换一个 Shader 对象（原地改写 tree），返回 audit 记录与每个已转换程序的 tail。
func convertShader(ctx context.Context, dev *metal.Device, tree *serialized.Struct) (*shaderReport, map[int]*shaderblob.Tail, error) {
	t, err := translateShader(ctx, tree)
	if err != nil {
		return nil, nil, err
	}
	reflections, err := linkShader(ctx, dev, t)
	if err != nil {
		return nil, nil, err
	}
	// 3. 重写程序与 ParsedForm。
	tails := map[int]*shaderblob.Tail{}
	channelsOf := map[int]shaderblob.Channels{}
	padReads := 0
	indices := make([]int, 0, len(reflections))
	for index := range reflections {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		if _, failed := t.failures[index]; failed {
			continue
		}
		r := reflections[index]
		var channels shaderblob.Channels
		args := r.result.Fragment
		if r.kind == "vertex" {
			args = r.result.Vertex
			channels, err = vertexChannels(r.result.VertexInputs)
		}
		var tail *shaderblob.Tail
		if err == nil {
			tail, err = legacyTailFromDriver(args, channels, t.inline[index], t.structured[index])
		}
		if err != nil {
			t.failures[index] = "tail: " + truncate(err.Error(), 300)
			err = nil
			continue
		}
		program := t.programs[index]
		program.Header = [6]uint32{program.Header[0], stageType[t.stageOf[index]]}
		source := t.msl[index]
		// 程序代码换成 Unity 版本 4 的 Metal 源码头 + MSL；参数 tail 换成从驱动反射写出的完整 legacy tail（根因一）。
		program.Code = shaderblob.MetalCode(source)
		program.Parameters = tail.Bytes()
		tails[index] = tail
		channelsOf[index] = channels
		padReads += len(padReadRE.FindAllStringIndex(source, -1))
	}
	removed := 0
	for _, pass := range t.passes {
		for _, stage := range append(slices.Clone(stageKeys), otherStages...) {
			stageD := pass.S(stage)
			if stageD == nil {
				continue
			}
			// ParsedForm 的参数表全部留空：Metal 版的反射只在 legacy tail 里（根因一）。
			params.ClearStruct(stageD.S("m_CommonParameters"))
			var kept []any
			for _, sp := range subPrograms(stageD) {
				index := int(sp.I("m_BlobIndex"))
				if _, ok := tails[index]; !ok {
					removed++
					continue
				}
				if typ, ok := stageType[stage]; ok {
					sp.Set("m_GpuProgramType", int64(typ))
				}
				params.ClearStruct(sp.S("m_Parameters"))
				setChannels(sp.S("m_Channels"), channelsOf[index])
				kept = append(kept, sp)
			}
			if kept == nil {
				kept = []any{}
			}
			stageD.Set("m_SubPrograms", kept)
		}
		if pass.Get("m_Platforms") != nil {
			pass.Set("m_Platforms", []byte{platformMetal})
		}
	}
	for index, program := range t.programs {
		if _, ok := tails[index]; ok {
			continue
		}
		// 未引用（或已删除）的占位程序，永远不会被加载。
		typ := uint32(24)
		if st, ok := t.stageOf[index]; ok {
			typ = stageType[st]
		}
		program.Header = [6]uint32{program.Header[0], typ}
		program.Code = nil
		program.Parameters = shaderblob.EmptyTail().Bytes()
	}
	if err := shaderblob.ReplacePlatform(tree, platformMetal, t.programs); err != nil {
		return nil, nil, err
	}
	failures := map[string]string{}
	for k, v := range t.failures {
		failures[fmt.Sprint(k)] = v
	}
	return &shaderReport{
		Shader: t.name, Programs: len(t.programs), Converted: len(tails), RemovedSubprograms: removed,
		PadReads: padReads, Failures: failures,
	}, tails, nil
}

func setChannels(s *serialized.Struct, c shaderblob.Channels) {
	list := []any{}
	for _, ch := range c.Channels {
		list = append(list, &serialized.Struct{Fields: []serialized.Field{
			{Name: "source", Value: int64(ch.Source)}, {Name: "target", Value: int64(ch.Target)},
		}})
	}
	s.Set("m_Channels", list)
	s.Set("m_SourceMap", int64(c.SourceMap))
}
