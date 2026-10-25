package serialized

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/li-yifei/lom-dx2metal/internal/oracle"
)

// 验收 2：不改任何对象时，读→写与输入逐字节相等；每个 Shader 对象按类型树读→写也逐字节相等。
func TestRoundTrip(t *testing.T) {
	root := oracle.Root(t)
	game := oracle.GameData(t)
	files := []string{"Resources/unity_builtin_extra", "globalgamemanagers.assets", "resources.assets", "sharedassets0.assets"}
	dirs := map[string]string{
		"原版":        game,
		"titlediag": filepath.Join(root, "build/MortalARM64-titlediag.app/Contents/Resources/Data"),
	}
	for label, dir := range dirs {
		for _, name := range files {
			t.Run(label+"/"+name, func(t *testing.T) {
				in, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Skip(err)
				}
				f, err := Parse(in)
				if err != nil {
					t.Fatal(err)
				}
				shaders := 0
				for _, o := range f.Shaders() {
					v, err := f.ReadObject(o)
					if err != nil {
						t.Fatalf("path_id %d: %v", o.PathID, err)
					}
					orig := o.Data()
					if err := f.WriteObject(o, v); err != nil {
						t.Fatalf("path_id %d: %v", o.PathID, err)
					}
					if !bytes.Equal(orig, o.Data()) {
						t.Fatalf("path_id %d: 类型树往返不相等", o.PathID)
					}
					shaders++
				}
				out := f.Bytes()
				if !bytes.Equal(in, out) {
					n := 0
					for n < len(in) && n < len(out) && in[n] == out[n] {
						n++
					}
					t.Fatalf("文件往返不相等：长度 %d→%d，首个差异 @%d", len(in), len(out), n)
				}
				t.Logf("%d 字节、%d 个对象、%d 个 Shader 往返相等", len(in), len(f.Objects), shaders)
			})
		}
	}
}
