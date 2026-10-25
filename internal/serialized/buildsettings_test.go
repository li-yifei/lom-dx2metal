package serialized

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/li-yifei/lom-dx2metal/internal/oracle"
)

// 原版 globalgamemanagers：BuildSettings 按内嵌类型树读→写逐字节相等、文件往返逐字节相等；
// 改成 [16] 后与 Python 版 assemble.patch_settings 的产物（MortalARM64.app 里的 globalgamemanagers）比较。
func TestGraphicsAPIs(t *testing.T) {
	in, err := os.ReadFile(filepath.Join(oracle.GameData(t), "globalgamemanagers"))
	if err != nil {
		t.Skip(err)
	}
	f, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	o, err := f.BuildSettings()
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.ReadObject(o)
	if err != nil {
		t.Fatal(err)
	}
	orig := o.Data()
	if err := f.WriteObject(o, v); err != nil || !bytes.Equal(orig, o.Data()) {
		t.Fatalf("BuildSettings 类型树往返失败：%v", err)
	}
	if !bytes.Equal(f.Bytes(), in) {
		t.Fatal("globalgamemanagers 往返不相等")
	}
	before, changed, err := f.SetGraphicsAPIs([]int{GraphicsMetal})
	if err != nil || !changed {
		t.Fatalf("改写失败：%v changed=%v", err, changed)
	}
	out := f.Bytes()
	t.Logf("m_GraphicsAPIs %v → [16]，%d → %d 字节", before, len(in), len(out))
	g, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if apis, _ := g.GraphicsAPIs(); !slices.Equal(apis, []int{GraphicsMetal}) {
		t.Fatalf("读回 %v", apis)
	}
	if _, changed, _ := g.SetGraphicsAPIs([]int{GraphicsMetal}); changed {
		t.Fatal("已是 [16] 时不应改动")
	}
	// 1. Python 版 lom-arm64-converter 的 assemble.patch_settings（tools/gen_gfx_oracle.py 生成，只改 BuildSettings）。
	py := filepath.Join(oracle.Py(t), "gfx/globalgamemanagers")
	compareWithPython(t, g, out, py, -1)
	// 2. MortalARM64.app 里的 globalgamemanagers：ports/arm64/build_app.py 产物，它另外改了 PlayerSettings（公司名、产品名等）。
	compareWithPython(t, g, out, filepath.Join(oracle.Root(t), "build/MortalARM64.app/Contents/Resources/Data/globalgamemanagers"), 129)
}

// compareWithPython 比较 Go 版改写结果与 UnityPy 保存的文件：文件级相等最好；否则要求每个对象（allowClass 除外）
// 与元数据逐字节相同，差异只允许来自 UnityPy 重写数据区（丢掉 Unity 留在对象之间的空洞、重新排布）。
func compareWithPython(t *testing.T, g *File, out []byte, path string, allowClass int32) {
	t.Helper()
	pyb, err := os.ReadFile(path)
	if err != nil {
		t.Skip(err)
	}
	if bytes.Equal(out, pyb) {
		t.Logf("%s：逐字节相等", path)
		return
	}
	py, err := Parse(pyb)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]*Object{}
	for _, x := range py.Objects {
		byID[x.PathID] = x
	}
	if len(py.Objects) != len(g.Objects) {
		t.Fatalf("对象数 %d != %d", len(py.Objects), len(g.Objects))
	}
	allowed := 0
	for _, x := range g.Objects {
		y := byID[x.PathID]
		if y == nil || x.TypeID != y.TypeID {
			t.Fatalf("path_id %d 在 %s 里不存在或类型不同", x.PathID, path)
		}
		if !bytes.Equal(x.Data(), y.Data()) {
			if x.ClassID == allowClass {
				allowed++
				continue
			}
			t.Fatalf("path_id %d（ClassID %d）与 %s 不同", x.PathID, x.ClassID, path)
		}
	}
	if !bytes.Equal(g.metaPrefix, py.metaPrefix) || !bytes.Equal(g.metaSuffix, py.metaSuffix) {
		t.Fatalf("元数据（类型表/外部引用）与 %s 不同", path)
	}
	// 数据区：对象起点相同时，两边数据区（去掉 UnityPy 末尾补的对齐字节）应逐字节相同。
	gd := out[g.dataOffset:]
	pd := pyb[py.dataOffset : int64(len(pyb))-int64(len(py.trailing))]
	sameData := bytes.Equal(gd, pd)
	if allowClass < 0 && !sameData {
		t.Fatalf("数据区与 %s 不同", path)
	}
	t.Logf("%s：文件 %d vs %d 字节；%d 个对象中 %d 个逐字节相同（%d 个是预期的 PlayerSettings 改动），元数据相同，数据区相同=%v。"+
		"文件级差异：Unity 把数据区起点对齐到 %d，UnityPy 重新按 16 对齐到 %d 并在末尾补 %d 字节；本工具保留 Unity 原布局",
		filepath.Base(filepath.Dir(filepath.Dir(path))), len(out), len(pyb), len(g.Objects), len(g.Objects)-allowed, allowed, sameData,
		g.dataOffset, py.dataOffset, len(py.trailing))
	if allowClass < 0 && g.dataOffset-py.dataOffset-int64(len(py.trailing)) != int64(len(out)-len(pyb)) {
		t.Fatal("文件长度差不能完全由数据区起点与末尾对齐解释")
	}
}
