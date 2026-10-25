// Package oracle 给各包测试提供 oracle 目录（Python 版产出与研究数据，不入库）的路径。
//
// 测试参数：-oracle <ports/arm64 目录>（默认 /Users/bob/lom-ws/ports/arm64），
// -pyoracle <gen_oracle.py 输出目录>（默认读 tools/lom-dx2metal/.runs-dir 里记录的本次 runs 目录下的 oracle/）。
package oracle

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var (
	dir   = flag.String("oracle", "/Users/bob/lom-ws/ports/arm64", "oracle 目录（ports/arm64）")
	pydir = flag.String("pyoracle", "", "tools/gen_oracle.py 的输出目录")
)

// Root 返回 oracle 目录；不存在时跳过测试。
func Root(t testing.TB) string {
	t.Helper()
	if _, err := os.Stat(*dir); err != nil {
		t.Skipf("oracle 目录不存在：%s", *dir)
	}
	return *dir
}

// RepoRoot 返回 lom-dx2metal 仓库根目录。
func RepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

// Py 返回 gen_oracle.py 的输出目录（含 in/ out/ work/ audit.json）；不存在时跳过测试。
func Py(t testing.TB) string {
	t.Helper()
	d := *pydir
	if d == "" {
		b, err := os.ReadFile(filepath.Join(RepoRoot(), ".runs-dir"))
		if err == nil {
			d = filepath.Join(strings.TrimSpace(string(b)), "oracle")
		}
	}
	if d == "" {
		t.Skip("未指定 -pyoracle")
	}
	if _, err := os.Stat(filepath.Join(d, "audit.json")); err != nil {
		t.Skipf("Python oracle 不存在：%s", d)
	}
	return d
}

// GameData 返回 build-receipt.json 记录的原版游戏 Data 目录（只读）。
func GameData(t testing.TB) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(Root(t), "build/build-receipt.json"))
	if err != nil {
		t.Skip("没有 build-receipt.json")
	}
	var r struct {
		SourceGame string `json:"source_game"`
	}
	if json.Unmarshal(b, &r) != nil || r.SourceGame == "" {
		t.Skip("build-receipt.json 无 source_game")
	}
	d := filepath.Join(r.SourceGame, "Mortal_Data")
	if _, err := os.Stat(d); err != nil {
		t.Skipf("原版游戏目录不存在：%s", d)
	}
	return d
}
