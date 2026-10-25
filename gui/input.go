package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/li-yifei/lom-dx2metal/gui/locales"
)

// inputDir 是对拖入文件夹的检查结果。
type inputDir struct {
	Path string // 要转换的 *_Data 目录
	// Problem 说明为什么不能转换（当前界面语言）；可以转换时为 nil。
	Problem func() string
}

// checkInput 检查拖入的文件夹：必须有 globalgamemanagers 与
// Resources/unity_builtin_extra。如果拖入的是游戏根目录，而其中恰有一个
// *_Data 子目录，就自动进入它。
func checkInput(dropped string) inputDir {
	path := filepath.Clean(dropped)
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return inputDir{Path: path, Problem: func() string { return locales.GameUnreadable(err.Error()) }}
	case !info.IsDir():
		return inputDir{Path: path, Problem: func() string { return locales.GameNotFolder() }}
	}
	if !exists(filepath.Join(path, "globalgamemanagers")) {
		if subs, _ := filepath.Glob(filepath.Join(path, "*_Data")); len(subs) == 1 && exists(filepath.Join(subs[0], "globalgamemanagers")) {
			path = subs[0]
		}
	}
	switch {
	case !exists(filepath.Join(path, "globalgamemanagers")):
		return inputDir{Path: path, Problem: func() string { return locales.GameNoGGM() }}
	case !exists(filepath.Join(path, "Resources", "unity_builtin_extra")):
		return inputDir{Path: path, Problem: func() string { return locales.GameNoBuiltin() }}
	}
	return inputDir{Path: path}
}

// defaultOutput 是输入旁边的 <名字>_Data.metal。
func defaultOutput(input string) string { return filepath.Clean(input) + ".metal" }

// outputProblem 检查输出目录的位置，返回 nil 或说明。
func outputProblem(input, out string) func() string {
	in, o := filepath.Clean(input), filepath.Clean(out)
	switch {
	case o == in:
		return func() string { return locales.OutputSame() }
	case strings.HasPrefix(o+string(filepath.Separator), in+string(filepath.Separator)):
		return func() string { return locales.OutputInside() }
	}
	return nil
}

// dirSize 是目录下所有普通文件的字节数之和；stop 返回 true 时提前结束。
func dirSize(root string, stop func() bool) int64 {
	var n int64
	filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if stop() {
			return filepath.SkipAll
		}
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// freeBytes 是 path（或其最近的已存在上级目录）所在磁盘的可用字节数，未知为 -1。
func freeBytes(path string) int64 {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		var st syscall.Statfs_t
		if syscall.Statfs(p, &st) == nil {
			return int64(st.Bavail) * int64(st.Bsize)
		}
		if p == filepath.Dir(p) {
			return -1
		}
	}
}

// formatBytes 按 1000 进制显示，与访达一致。
func formatBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f, i := float64(n), 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	return strconv.FormatFloat(f, 'f', 1, 64) + " " + units[i]
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	return path
}

// sortedFailures 让失败列表顺序稳定（audit 里是映射时顺序不定）。
func sortedFailures(list []failure) []failure {
	out := append([]failure(nil), list...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
