package convert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/li-yifei/lom-dx2metal/internal/metal"
	"github.com/li-yifei/lom-dx2metal/internal/oracle"
	"github.com/li-yifei/lom-dx2metal/internal/serialized"
	"github.com/li-yifei/lom-dx2metal/internal/shaderblob"
)

// 参考信息（不判失败）：与 DESIGN 最初指定的旧 oracle 比较——ports/arm64/build/metal-bulk/ 的 MSL 与
// MortalARM64-titlediag.app 里 convert_all_metal.py 转出的 blob。它们早于内联采样器修复（根因三）与
// GUIRoundedRect 修复，所以含内联采样器的 shader 预期不同。go test ./convert -run Legacy -v 查看明细。
func TestLegacyOracleReport(t *testing.T) {
	root := oracle.Root(t)
	py := oracle.Py(t)
	in := filepath.Join(py, "in")
	dev, err := metal.NewDevice()
	if err != nil {
		t.Skip(err)
	}
	var bulk struct {
		Shaders map[string]struct {
			Shader string `json:"shader"`
		} `json:"shaders"`
	}
	if b, err := os.ReadFile(filepath.Join(root, "investigation/assets/bulk-audit.json")); err == nil {
		json.Unmarshal(b, &bulk)
	}
	title := loadMetalShaders(t, filepath.Join(root, "build/MortalARM64-titlediag.app/Contents/Resources/Data"))
	mslEqual, mslTotal, blobEqual, blobTotal := 0, 0, 0, 0
	diffMSL, diffBlob := map[string]int{}, map[string]int{}
	inlineShaders := map[string]bool{}
	for _, file := range shaderFiles(in) {
		rel, _ := filepath.Rel(in, file)
		b, _ := os.ReadFile(file)
		a, err := loadAsset(b)
		if err != nil {
			continue
		}
		for _, f := range a.files {
			for _, o := range f.Shaders() {
				tree, _ := f.ReadObject(o)
				if p := shaderblob.ShaderPlatforms(tree); len(p) != 1 || p[0] != platformD3D11 {
					continue
				}
				tr, err := translateShader(context.Background(), tree)
				if err != nil {
					t.Fatal(err)
				}
				for index, s := range tr.inline {
					for _, sp := range s {
						if sp.Sampler != 0 {
							inlineShaders[tr.name] = true
						}
					}
					_ = index
				}
				for index, msl := range tr.msl {
					p := filepath.Join(root, "build/metal-bulk", safeName(tr.name), fmt.Sprintf("%s-%d.metal", tr.stageOf[index], index))
					wb, err := os.ReadFile(p)
					if err != nil {
						continue
					}
					mslTotal++
					if string(wb) == msl {
						mslEqual++
					} else {
						diffMSL[tr.name]++
					}
				}
				key := fmt.Sprintf("%s:%d", filepath.ToSlash(rel), o.PathID)
				if _, ok := bulk.Shaders[key]; !ok {
					continue // 走手工 manifest 的 shader 不在 bulk-audit 里
				}
				w, ok := title[shaderKey{rel, o.PathID}]
				if !ok {
					continue
				}
				c := serialized.Clone(tree).(*serialized.Struct)
				if _, _, err := convertShader(context.Background(), dev, c); err != nil {
					t.Fatal(err)
				}
				got, _ := shaderblob.UnpackShader(c, platformMetal)
				exp, err := shaderblob.UnpackShader(w[2].(*serialized.Struct), platformMetal)
				if err != nil || len(got) != len(exp) {
					diffBlob[tr.name] += len(got)
					blobTotal += len(got)
					continue
				}
				for i := range got {
					blobTotal++
					if bytes.Equal(got[i].Bytes(), exp[i].Bytes()) {
						blobEqual++
					} else {
						diffBlob[tr.name]++
					}
				}
			}
		}
	}
	list := func(m map[string]int) string {
		var s []string
		for k, v := range m {
			tag := ""
			if inlineShaders[k] {
				tag = "，含内联采样器"
			}
			s = append(s, fmt.Sprintf("%s ×%d%s", k, v, tag))
		}
		sort.Strings(s)
		return strings.Join(s, "\n  ")
	}
	t.Logf("metal-bulk MSL：%d/%d 相同；不同的 shader：\n  %s", mslEqual, mslTotal, list(diffMSL))
	t.Logf("titlediag blob（bulk-audit 范围）：%d/%d 相同；不同的 shader：\n  %s", blobEqual, blobTotal, list(diffBlob))
}
