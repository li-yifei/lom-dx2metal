// Package relayout 重排 HLSLcc 生成的常量缓冲区结构体，让每个具名成员落在 Unity 序列化表
// （也就是引擎上传常量时）期望的字节偏移上（移植 relayout.py）。
//
// HLSLcc 的 REMOVE_UNUSED_GLOBALS 会重新打包 $Globals，float4 粒度的填充也会挪动部分占用 CB 里的标量，
// 生成的 MSL 结构体因此与引擎写入的偏移不一致。成员按序列化偏移排序、空隙补 pad、被访问但没有名字的
// 槽位塞进剩余空隙。D3D 允许向量放在任意不跨 16 字节行的 4 字节偏移；MSL 的 float2/float3 做不到，
// 这时改用 packed_* 存储，读取处再转回对齐类型。
package relayout

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const mtxPrefix = "hlslcc_mtx4x4"

type typeInfo struct{ size, align int }

// MSL constant 地址空间的大小/对齐。
var types = map[string]typeInfo{
	"float": {4, 4}, "int": {4, 4}, "uint": {4, 4}, "bool": {4, 4},
	"float2": {8, 8}, "int2": {8, 8}, "uint2": {8, 8},
	"float3": {16, 16}, "int3": {16, 16}, "uint3": {16, 16},
	"float4": {16, 16}, "int4": {16, 16}, "uint4": {16, 16},
	"packed_float2": {8, 4}, "packed_int2": {8, 4}, "packed_uint2": {8, 4},
	"packed_float3": {12, 4}, "packed_int3": {12, 4}, "packed_uint3": {12, 4},
	"packed_float4": {16, 4}, "packed_int4": {16, 4}, "packed_uint4": {16, 4},
}

var packed = map[string]string{
	"float2": "packed_float2", "int2": "packed_int2", "uint2": "packed_uint2",
	"float3": "packed_float3", "int3": "packed_int3", "uint3": "packed_uint3",
	"float4": "packed_float4", "int4": "packed_int4", "uint4": "packed_uint4",
}

var (
	structRE = regexp.MustCompile(`(?s)(struct\s+([\w$]+)\s*\{)(.*?)(\};)`)
	memberRE = regexp.MustCompile(`([\w$]+)\s+([\w$]+)\s*(?:\[(\d+)\])?\s*;`)
	bufRE    = regexp.MustCompile(`constant\s+([\w$]+)\s*&\s*([\w$]+)\s*\[\[\s*buffer\((\d+)\)\s*\]\]`)
	padRE    = regexp.MustCompile(`^pad_.*_(d?)(\d+)$`)
)

// memberSize：MSL 数组元素按自然大小步进（float x[4] 占 16 字节，不是 D3D cbuffer 的 4×16）；
// Unity 按 Metal 反射报告的偏移写数组元素。
func memberSize(ty string, arr int) (int, int) {
	t := types[ty]
	stride := (t.size + t.align - 1) / t.align * t.align
	if arr != 0 {
		return stride * arr, t.align
	}
	return t.size, t.align
}

func emitPads(lines []string, cur, target int, tag string, n int) ([]string, int) {
	for cur < target {
		switch {
		case cur%16 == 0 && target-cur >= 16:
			k := (target - cur) / 16
			if k > 1 {
				lines = append(lines, fmt.Sprintf("    float4 pad_%s_%d[%d];", tag, n, k))
			} else {
				lines = append(lines, fmt.Sprintf("    float4 pad_%s_%d;", tag, n))
			}
			cur += 16 * k
		case cur%8 == 0 && target-cur >= 8:
			lines = append(lines, fmt.Sprintf("    float2 pad_%s_%d;", tag, n))
			cur += 8
		default:
			lines = append(lines, fmt.Sprintf("    float pad_%s_%d;", tag, n))
			cur += 4
		}
		n++
	}
	return lines, n
}

// item 对应 Python 的 (off, ty, nm, arr) 元组，排序时按元组逐项比较。
type item struct {
	off int
	ty  string
	nm  string
	arr int
}

func sortItems(xs []item) {
	sort.SliceStable(xs, func(i, j int) bool {
		a, b := xs[i], xs[j]
		if a.off != b.off {
			return a.off < b.off
		}
		if a.ty != b.ty {
			return a.ty < b.ty
		}
		if a.nm != b.nm {
			return a.nm < b.nm
		}
		return a.arr < b.arr
	})
}

type span struct {
	start, end int
	ty, nm     string
	arr        int
}

// CBOffsets 是一个 CB 的 成员名 → Unity 偏移 与 CB 大小。
type CBOffsets struct {
	Members map[string]int
	Size    int
}

func accessRE(inst, nm string, leadingB bool) *regexp.Regexp {
	p := regexp.QuoteMeta(inst) + `\s*\.\s*` + regexp.QuoteMeta(nm) + `\b`
	if leadingB {
		p = `\b` + p
	}
	return regexp.MustCompile(p)
}

// CBStructs 重建每个绑定到 buffer 的结构体，使具名成员位于序列化偏移；被访问的 pad 成员塞进剩余空隙。
func CBStructs(msl string, offsetsBySlot map[int]CBOffsets, tag string) (string, error) {
	orig := msl
	structs := map[string][]string{}
	for _, m := range structRE.FindAllStringSubmatch(orig, -1) {
		structs[m[2]] = m // 同名取最后一个
	}
	for _, bm := range bufRE.FindAllStringSubmatch(orig, -1) {
		sname, inst := bm[1], bm[2]
		slot, _ := strconv.Atoi(bm[3])
		cbo, ok := offsetsBySlot[slot]
		if !ok {
			return "", fmt.Errorf("%s: buffer(%d) %s 没有序列化的 CB", tag, slot, inst)
		}
		serial, cbsize := cbo.Members, cbo.Size
		sm, ok := structs[sname]
		if !ok {
			return "", fmt.Errorf("%s: 找不到结构体 %s", tag, sname)
		}
		var placed, pads []item
		origOff := 0
		for _, m := range memberRE.FindAllStringSubmatch(sm[3], -1) {
			ty, nm := m[1], m[2]
			arr := 0
			if m[3] != "" {
				arr, _ = strconv.Atoi(m[3])
			}
			if _, ok := types[ty]; !ok {
				return "", fmt.Errorf("%s: 不支持的 MSL 类型 %s", tag, ty)
			}
			size, align := memberSize(ty, arr)
			origOff = (origOff + align - 1) &^ (align - 1)
			key := strings.TrimPrefix(nm, mtxPrefix)
			if off, ok := serial[key]; ok {
				placed = append(placed, item{off, ty, nm, arr})
			} else if accessRE(inst, nm, false).MatchString(msl) {
				// 被访问但不在序列化表里：引擎从不写它（与 D3D11 一样读到 0）。RDEF pad 的名字编码了 DXBC 位置
				// pad_<cb>_<行> / pad_<cb>_d<dword>，尽量放回原处。
				want := origOff
				if pm := padRE.FindStringSubmatch(nm); pm != nil {
					v, _ := strconv.Atoi(pm[2])
					if pm[1] != "" {
						want = v * 4
					} else {
						want = v * 16
					}
				}
				pads = append(pads, item{want, ty, nm, arr})
			}
			origOff += size
		}
		sortItems(placed)
		var spans []span
		type rep struct{ nm, aty string }
		var repacked []rep
		for i, p := range placed {
			size, align := memberSize(p.ty, p.arr)
			if p.off%align != 0 {
				pty := ""
				if p.arr == 0 {
					pty = packed[p.ty]
				}
				if pty == "" || p.off%4 != 0 {
					return "", fmt.Errorf("%s: %s@%d 违反对齐 %d", tag, p.nm, p.off, align)
				}
				size = types[pty].size
				repacked = append(repacked, rep{p.nm, p.ty})
				placed[i].ty = pty
				p.ty = pty
			}
			spans = append(spans, span{p.off, p.off + size, p.ty, p.nm, p.arr})
		}
		for i := 0; i < len(spans)-1; i++ {
			a, b := spans[i], spans[i+1]
			pty := ""
			if a.arr == 0 {
				pty = packed[a.ty]
			}
			if a.end > b.start && pty != "" && a.start+types[pty].size <= b.start {
				// D3D 把一个 float3 和后面的标量打包进同一个 16 字节行。
				for k := range placed {
					if placed[k].nm == a.nm {
						placed[k] = item{a.start, pty, a.nm, a.arr}
						break
					}
				}
				repacked = append(repacked, rep{a.nm, a.ty})
				spans[i] = span{a.start, a.start + types[pty].size, pty, a.nm, a.arr}
			}
		}
		for i := 0; i+1 < len(spans); i++ {
			a, b := spans[i], spans[i+1]
			if a.end > b.start {
				return "", fmt.Errorf("%s: %s [%d,%d) overlaps %s@%d", tag, a.nm, a.start, a.end, b.nm, b.start)
			}
		}
		for _, s := range spans {
			cbsize = max(cbsize, s.end)
		}
		// DXBC 的 dcl_constantbuffer 可能声明比 Unity 序列化更多的行（末尾未序列化成员），这些 pad 保持在 DXBC 偏移。
		for _, p := range pads {
			size, _ := memberSize(p.ty, p.arr)
			cbsize = max(cbsize, p.off+size)
		}
		type gap struct{ lo, hi int }
		var gaps []gap
		cur := 0
		for _, s := range spans {
			if s.start > cur {
				gaps = append(gaps, gap{cur, s.start})
			}
			cur = max(cur, s.end)
		}
		if cur < cbsize {
			gaps = append(gaps, gap{cur, cbsize})
		}
		take := func(gi, o, size int) {
			g := gaps[gi]
			gaps[gi] = gap{o + size, g.hi}
			if o > g.lo {
				gaps = append(gaps[:gi], append([]gap{{g.lo, o}}, gaps[gi:]...)...)
			}
		}
		for _, p := range pads {
			size, align := memberSize(p.ty, p.arr)
			spot := -1
			for gi, g := range gaps {
				if g.lo <= p.off && p.off+size <= g.hi {
					spot = p.off
					take(gi, p.off, size)
					break
				}
			}
			if spot < 0 {
				for gi, g := range gaps {
					o := (g.lo + align - 1) &^ (align - 1)
					if o+size <= g.hi {
						spot = o
						take(gi, o, size)
						break
					}
				}
			}
			if spot < 0 {
				return "", fmt.Errorf("%s: no room for accessed pad %s (%dB) within CB size %d", tag, p.nm, size, cbsize)
			}
			placed = append(placed, item{spot, p.ty, p.nm, p.arr})
		}
		sortItems(placed)
		var lines []string
		cur, n := 0, 0
		for _, p := range placed {
			if p.off > cur {
				lines, n = emitPads(lines, cur, p.off, "fill", n)
			}
			if p.arr != 0 {
				lines = append(lines, fmt.Sprintf("    %s %s[%d];", p.ty, p.nm, p.arr))
			} else {
				lines = append(lines, fmt.Sprintf("    %s %s;", p.ty, p.nm))
			}
			size, _ := memberSize(p.ty, p.arr)
			cur = p.off + size
		}
		body := "\n" + strings.Join(lines, "\n") + "\n"
		msl = strings.Replace(msl, sm[0], sm[1]+body+sm[4], 1)
		for _, r := range repacked {
			msl = accessRE(inst, r.nm, true).ReplaceAllLiteralString(msl, fmt.Sprintf("%s(%s.%s)", r.aty, inst, r.nm))
		}
	}
	return msl, nil
}
