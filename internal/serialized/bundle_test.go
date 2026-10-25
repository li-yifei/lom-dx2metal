package serialized

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"lom-dx2metal/internal/oracle"
)

// bundle：解析→内部 SerializedFile 往返→重新打包→再解析，内容逐字节相等。
func TestBundleRoundTrip(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(oracle.GameData(t), "StreamingAssets/aa/StandaloneWindows64/particle_001_assets_all_*.bundle"))
	if len(files) == 0 {
		t.Skip("没有样本 bundle")
	}
	in, _ := os.ReadFile(files[0])
	bd, err := ParseBundle(in)
	if err != nil {
		t.Fatal(err)
	}
	shaders := 0
	for _, e := range bd.Entries {
		if !IsSerializedFile(e.Data) {
			continue
		}
		f, err := Parse(e.Data)
		if err != nil {
			t.Fatalf("%s: %v", e.Path, err)
		}
		for _, o := range f.Shaders() {
			v, err := f.ReadObject(o)
			if err != nil {
				t.Fatal(err)
			}
			orig := o.Data()
			if err := f.WriteObject(o, v); err != nil || !bytes.Equal(orig, o.Data()) {
				t.Fatalf("Shader %d 类型树往返失败：%v", o.PathID, err)
			}
			shaders++
		}
		if !bytes.Equal(f.Bytes(), e.Data) {
			t.Fatalf("%s 往返不相等", e.Path)
		}
	}
	out, err := bd.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseBundle(out)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range bd.Entries {
		if again.Entries[i].Path != e.Path || !bytes.Equal(again.Entries[i].Data, e.Data) {
			t.Fatalf("条目 %s 重打包后不同", e.Path)
		}
	}
	t.Logf("%d 个条目、%d 个 Shader；%d → %d 字节", len(bd.Entries), shaders, len(in), len(out))
}
