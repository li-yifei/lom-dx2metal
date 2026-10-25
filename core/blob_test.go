package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/li-yifei/lom-dx2metal/internal/metal"
	"github.com/li-yifei/lom-dx2metal/internal/oracle"
	"github.com/li-yifei/lom-dx2metal/internal/serialized"
	"github.com/li-yifei/lom-dx2metal/internal/shaderblob"
)

// stripBlob 去掉压缩数据（LZ4 压缩器不同，压缩字节允许不同），用于比较 Shader 对象其余部分。
func stripBlob(f *serialized.File, o *serialized.Object, tree *serialized.Struct) []byte {
	c := serialized.Clone(tree).(*serialized.Struct)
	c.Set("compressedBlob", []byte{})
	c.Set("compressedLengths", []any{})
	b, err := serialized.WriteTree(mustTree(f, o), c, f.App())
	if err != nil {
		panic(err)
	}
	return b
}

func mustTree(f *serialized.File, o *serialized.Object) *serialized.Node {
	n, err := f.TreeFor(o)
	if err != nil {
		panic(err)
	}
	return n
}

type shaderKey struct {
	rel    string
	pathID int64
}

// loadConverted 读出一个 Data 目录里所有 Metal Shader：key → (file, object, tree)。
func loadMetalShaders(t *testing.T, dir string) map[shaderKey][3]any {
	out := map[shaderKey][3]any{}
	for _, file := range shaderFiles(dir) {
		rel, _ := filepath.Rel(dir, file)
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
					t.Fatal(err)
				}
				out[shaderKey{rel, o.PathID}] = [3]any{f, o, tree}
			}
		}
	}
	return out
}

// 验收 4 与 5：在内存里转换 Python oracle 的输入（原版游戏文件的克隆），每个 Shader 的 platform 14 程序 blob
// 与 Python 版输出逐字节相等；Shader 对象除压缩字节外（ParsedForm 等）也逐字节相等；数量与 Python 版 audit 一致。
func TestBlobsMatchPython(t *testing.T) {
	py := oracle.Py(t)
	dev, err := metal.NewDevice()
	if err != nil {
		t.Skip(err)
	}
	want := loadMetalShaders(t, filepath.Join(py, "out"))
	in := filepath.Join(py, "in")
	var pyAudit struct {
		Totals map[string]int `json:"totals"`
	}
	if b, err := os.ReadFile(filepath.Join(py, "audit.json")); err == nil {
		json.Unmarshal(b, &pyAudit)
	}
	shaders, programs, converted, removed, equalPrograms := 0, 0, 0, 0, 0
	for _, file := range shaderFiles(in) {
		rel, _ := filepath.Rel(in, file)
		b, _ := os.ReadFile(file)
		a, err := loadAsset(b)
		if err != nil {
			continue
		}
		for _, f := range a.files {
			for _, o := range f.Shaders() {
				tree, err := f.ReadObject(o)
				if err != nil {
					t.Fatal(err)
				}
				if p := shaderblob.ShaderPlatforms(tree); len(p) != 1 || p[0] != platformD3D11 {
					continue
				}
				rep, _, err := convertShader(context.Background(), dev, tree)
				if err != nil {
					t.Fatalf("%s:%d: %v", rel, o.PathID, err)
				}
				shaders++
				programs += rep.Programs
				converted += rep.Converted
				removed += rep.RemovedSubprograms
				w, ok := want[shaderKey{rel, o.PathID}]
				if !ok {
					t.Fatalf("Python 输出里没有 %s:%d", rel, o.PathID)
				}
				wf, wo, wt := w[0].(*serialized.File), w[1].(*serialized.Object), w[2].(*serialized.Struct)
				got, err := shaderblob.UnpackShader(tree, platformMetal)
				if err != nil {
					t.Fatal(err)
				}
				exp, err := shaderblob.UnpackShader(wt, platformMetal)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(exp) {
					t.Fatalf("%s %s：程序数 %d != %d", rel, rep.Shader, len(got), len(exp))
				}
				for i := range got {
					if !bytes.Equal(got[i].Bytes(), exp[i].Bytes()) {
						t.Errorf("%s %s 程序 %d 与 Python 不同", rel, rep.Shader, i)
						continue
					}
					equalPrograms++
				}
				if !bytes.Equal(stripBlob(f, o, tree), stripBlob(wf, wo, wt)) {
					t.Errorf("%s %s：Shader 对象（除压缩字节）与 Python 不同", rel, rep.Shader)
				}
			}
		}
	}
	t.Logf("%d 个 shader，%d 个程序逐字节相等；程序 %d，转换 %d，移除变体 %d", shaders, equalPrograms, programs, converted, removed)
	if programs != 2550 || converted < 2546 {
		t.Errorf("数量不达标：%d 个程序转换 %d（要求 2550 / ≥2546）", programs, converted)
	}
	if pt := pyAudit.Totals; pt != nil && (pt["converted"] != converted || pt["removed"] != removed || pt["programs"] != programs) {
		t.Errorf("与 Python audit 不一致：%v", pt)
	}
	_ = fmt.Sprint
}
