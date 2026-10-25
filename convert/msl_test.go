package convert

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/li-yifei/lom-dx2metal/internal/oracle"
	"github.com/li-yifei/lom-dx2metal/internal/shaderblob"
)

// 验收 3：对 Python 版（tools/gen_oracle.py，即当前 lom-arm64-converter）产出的每个 .metal，
// Go 版生成的 MSL 文本逐字节相等。Python 的工作目录按 shader 名分目录、处理同名 shader 时不清空，
// 后处理的覆盖先处理的同名文件；这里按同样的文件顺序生成并覆盖，再逐个比较。
func TestMSLMatchesPython(t *testing.T) {
	py := oracle.Py(t)
	in := filepath.Join(py, "in")
	work := filepath.Join(py, "work")
	got := map[string]string{}
	failures := map[string]string{}
	shaders := 0
	for _, file := range shaderFiles(in) {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		a, err := loadAsset(b)
		if err != nil {
			continue
		}
		for _, f := range a.files {
			for _, o := range f.Shaders() {
				tree, err := f.ReadObject(o)
				if err != nil {
					t.Fatalf("%s:%d: %v", file, o.PathID, err)
				}
				if p := shaderblob.ShaderPlatforms(tree); len(p) != 1 || p[0] != platformD3D11 {
					continue
				}
				tr, err := translateShader(context.Background(), tree)
				if err != nil {
					t.Fatalf("%s:%d: %v", file, o.PathID, err)
				}
				shaders++
				dir := filepath.Join(work, safeName(tr.name))
				for index, msl := range tr.msl {
					got[filepath.Join(dir, fmt.Sprintf("%s-%d.metal", tr.stageOf[index], index))] = msl
				}
				for index, msg := range tr.failures {
					failures[filepath.Join(dir, fmt.Sprint(index))] = msg
				}
			}
		}
	}
	want, _ := filepath.Glob(filepath.Join(work, "*", "*.metal"))
	equal := 0
	var mismatches []string
	for _, w := range want {
		wb, _ := os.ReadFile(w)
		g, ok := got[w]
		switch {
		case !ok:
			mismatches = append(mismatches, w+"（Go 未生成："+failures[filepath.Join(filepath.Dir(w), fmt.Sprint(indexOf(w)))]+"）")
		case g != string(wb):
			mismatches = append(mismatches, w)
		default:
			equal++
		}
		delete(got, w)
	}
	for extra := range got {
		mismatches = append(mismatches, extra+"（Python 没有）")
	}
	sort.Strings(mismatches)
	t.Logf("%d 个 shader，%d/%d 个 .metal 逐字节相等", shaders, equal, len(want))
	if len(mismatches) > 0 {
		t.Fatalf("%d 处不一致，前几个：\n%s", len(mismatches), strings.Join(mismatches[:min(10, len(mismatches))], "\n"))
	}
}

func indexOf(path string) int {
	base := strings.TrimSuffix(filepath.Base(path), ".metal")
	var i int
	fmt.Sscanf(base[strings.LastIndex(base, "-")+1:], "%d", &i)
	return i
}
