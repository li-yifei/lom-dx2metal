package hlslcc

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"lom-dx2metal/internal/oracle"
)

// 与 Python 版使用的 hlslcc-cli（同一份源码编译）逐字节对比研究目录里留存的 DXBC 样本。
func TestMatchesCLI(t *testing.T) {
	cli := "/Users/bob/lom-ws/tools/lom-arm64-converter/build/bin/hlslcc-cli"
	if _, err := os.Stat(cli); err != nil {
		t.Skip("没有 hlslcc-cli")
	}
	files, _ := filepath.Glob(filepath.Join(oracle.Root(t), "investigation/runtime/*-rdef.dxbc"))
	if len(files) == 0 {
		t.Skip("没有样本")
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		want, err := exec.Command(cli, f, "b0000").Output()
		got, gerr := ToMetal(b, UnityFlags)
		if (err != nil) != (gerr != nil) {
			t.Fatalf("%s: cli err=%v go err=%v", f, err, gerr)
		}
		if err == nil && got != string(want) {
			t.Fatalf("%s: 输出不同", f)
		}
	}
	t.Logf("%d 个样本一致", len(files))
}
