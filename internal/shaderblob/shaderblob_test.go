package shaderblob

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/li-yifei/lom-dx2metal/internal/oracle"
	"github.com/li-yifei/lom-dx2metal/internal/serialized"
)

var update = flag.Bool("update", false, "从 Unity 官方 Player 的 unity default resources 重新导出 testdata")

// Unity 官方 macOS Player 自带的 unity default resources（公开可得，不是游戏资产）。
const defaultResources = "runtime/unity-mac-mono-2020.3.49f1/TargetSupport.pkg.tmp/Payload/Variations/" +
	"macos_arm64_nondevelopment_mono/UnityPlayer.app/Contents/Resources/unity default resources"

const guiTextDir = "../../testdata/gui_text_shader"

func loadShader(t *testing.T, path string, pathID int64) (*serialized.File, *serialized.Object, *serialized.Struct) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skip(err)
	}
	f, err := serialized.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range f.Objects {
		if o.PathID == pathID {
			v, err := f.ReadObject(o)
			if err != nil {
				t.Fatal(err)
			}
			return f, o, v
		}
	}
	t.Fatalf("%s 没有 path_id %d", path, pathID)
	return nil, nil, nil
}

// 导出 GUI/Text Shader 的 8 个 Metal 程序到 testdata（go test -run TestExportTestdata -update）。
func TestExportTestdata(t *testing.T) {
	if !*update {
		t.Skip("需要 -update")
	}
	_, _, v := loadShader(t, filepath.Join(oracle.Root(t), defaultResources), 10101)
	if name := v.S("m_ParsedForm").Str("m_Name"); name != "GUI/Text Shader" {
		t.Fatalf("path_id 10101 是 %q", name)
	}
	programs, err := UnpackShader(v, 14)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(guiTextDir, 0o755)
	for i, p := range programs {
		os.WriteFile(filepath.Join(guiTextDir, fmt.Sprintf("program-%d.bin", i)), p.Bytes(), 0o644)
	}
	t.Logf("导出 %d 个程序", len(programs))
}

func checkTails(t *testing.T, programs []*Program) {
	t.Helper()
	for i, p := range programs {
		if !bytes.Equal(p.Bytes(), mustProgramBytes(t, p)) {
			t.Fatalf("程序 %d 程序往返不相等", i)
		}
		tail, err := ParseTail(p.Parameters)
		if err != nil {
			t.Fatalf("程序 %d：%v", i, err)
		}
		if !bytes.Equal(tail.Bytes(), p.Parameters) {
			t.Fatalf("程序 %d：tail 往返不相等", i)
		}
	}
}

func mustProgramBytes(t *testing.T, p *Program) []byte {
	q, err := ParseProgram(p.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return q.Bytes()
}

// 验收 1（第一部分）：testdata 里 GUI/Text Shader 的 8 个程序 tail parse→write 逐字节相等。
func TestTailRoundTripGUIText(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(guiTextDir, "program-*.bin"))
	sort.Strings(files)
	if len(files) != 8 {
		t.Fatalf("testdata 应有 8 个程序，实际 %d", len(files))
	}
	var programs []*Program
	for _, f := range files {
		b, _ := os.ReadFile(f)
		p, err := ParseProgram(b)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(p.Bytes(), b) {
			t.Fatalf("%s 程序往返不相等", f)
		}
		programs = append(programs, p)
	}
	checkTails(t, programs)
}

// 验收 1（第二部分）：游戏 FinalPost 的 34 个 Metal 程序（titlediag 由 Python 版转换）tail 往返逐字节相等。
func TestTailRoundTripFinalPost(t *testing.T) {
	path := filepath.Join(oracle.Root(t), "build/MortalARM64-titlediag.app/Contents/Resources/Data/globalgamemanagers.assets")
	_, _, v := loadShader(t, path, 489)
	if name := v.S("m_ParsedForm").Str("m_Name"); name != "Hidden/Universal Render Pipeline/FinalPost" {
		t.Fatalf("path_id 489 是 %q", name)
	}
	programs, err := UnpackShader(v, 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(programs) != 34 {
		t.Fatalf("FinalPost 应有 34 个程序，实际 %d", len(programs))
	}
	checkTails(t, programs)
}

// LZ4 打包往返：解包→打包→解包，程序逐字节相等。
func TestPackRoundTrip(t *testing.T) {
	path := filepath.Join(oracle.Root(t), "build/MortalARM64-titlediag.app/Contents/Resources/Data/globalgamemanagers.assets")
	_, _, v := loadShader(t, path, 489)
	programs, err := UnpackShader(v, 14)
	if err != nil {
		t.Fatal(err)
	}
	c := serialized.Clone(v).(*serialized.Struct)
	if err := ReplacePlatform(c, 14, programs); err != nil {
		t.Fatal(err)
	}
	again, err := UnpackShader(c, 14)
	if err != nil {
		t.Fatal(err)
	}
	for i := range programs {
		if !bytes.Equal(programs[i].Bytes(), again[i].Bytes()) {
			t.Fatalf("程序 %d 打包往返不相等", i)
		}
	}
}
