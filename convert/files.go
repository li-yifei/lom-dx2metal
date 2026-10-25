package convert

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// safeName 与 bulk.safe 相同（工作目录 / 测试比对用）。
func safeName(name string) string { return unsafeName.ReplaceAllString(name, "_") }

// shaderFiles 列出 Data 目录下可能含 Shader 的文件，顺序与 bulk.shader_files 相同：
// Resources/unity_builtin_extra、*.assets、level*（无扩展名）、StreamingAssets/aa/**/*.bundle（再加 StreamingAssets 下其他 *.bundle）。
func shaderFiles(data string) []string {
	var files []string
	add := func(p string) {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			files = append(files, p)
		}
	}
	add(filepath.Join(data, "Resources/unity_builtin_extra"))
	assets, _ := filepath.Glob(filepath.Join(data, "*.assets"))
	sort.Strings(assets)
	for _, a := range assets {
		add(a)
	}
	levels, _ := filepath.Glob(filepath.Join(data, "level*"))
	sort.Strings(levels)
	for _, l := range levels {
		if filepath.Ext(l) == "" {
			add(l)
		}
	}
	var bundles []string
	filepath.WalkDir(filepath.Join(data, "StreamingAssets"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".bundle") {
			bundles = append(bundles, p)
		}
		return nil
	})
	// Python 版只看 StreamingAssets/aa/**；aa 下的排在前面并按路径排序，其余随后。
	aa := filepath.Join(data, "StreamingAssets", "aa") + string(filepath.Separator)
	sort.SliceStable(bundles, func(i, j int) bool {
		ai, aj := strings.HasPrefix(bundles[i], aa), strings.HasPrefix(bundles[j], aa)
		if ai != aj {
			return ai
		}
		return bundles[i] < bundles[j]
	})
	for _, b := range bundles {
		add(b)
	}
	return files
}
