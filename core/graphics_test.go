package core

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/li-yifei/lom-dx2metal/internal/oracle"
	"github.com/li-yifei/lom-dx2metal/internal/serialized"
)

// globalgamemanagers 的图形 API 改写：原版 [2] → 只改 BuildSettings 那 4 字节、写出；已是 [16] 时不写；--keep-graphics-api 时不碰。
func TestPatchGraphicsAPIs(t *testing.T) {
	game := oracle.GameData(t)
	orig, err := os.ReadFile(filepath.Join(game, "globalgamemanagers"))
	if err != nil {
		t.Skip(err)
	}
	au, res := &audit{}, &Result{}
	data, err := patchGraphicsAPIs(game, true, false, au, res)
	if err != nil || data == nil {
		t.Fatalf("应改写：%v", err)
	}
	if !slices.Equal(au.GraphicsAPIs.Before, []int{2}) || !slices.Equal(au.GraphicsAPIs.After, []int{16}) || !res.GraphicsPatched {
		t.Fatalf("audit 记录错误：%+v", au.GraphicsAPIs)
	}
	if len(data) != len(orig) {
		t.Fatalf("长度变了：%d → %d", len(orig), len(data))
	}
	var diff []int
	for i := range data {
		if data[i] != orig[i] {
			diff = append(diff, i)
		}
	}
	if len(diff) != 1 {
		t.Fatalf("应只有 1 个字节不同（2→16），实际 %d 处", len(diff))
	}
	f, _ := serialized.Parse(data)
	if apis, _ := f.GraphicsAPIs(); !slices.Equal(apis, []int{16}) {
		t.Fatalf("读回 %v", apis)
	}
	// --keep-graphics-api
	au2 := &audit{}
	if d, err := patchGraphicsAPIs(game, true, true, au2, &Result{}); d != nil || err != nil || au2.GraphicsAPIs != nil {
		t.Fatal("keep 时不应处理")
	}
	// 已经是 [16]（ports/arm64 的 MortalARM64.app）：记录但不写。
	app := filepath.Join(oracle.Root(t), "build/MortalARM64.app/Contents/Resources/Data")
	au3, res3 := &audit{}, &Result{}
	if d, err := patchGraphicsAPIs(app, true, false, au3, res3); d != nil || err != nil {
		t.Fatalf("已是 [16] 时不应写：%v", err)
	}
	if !slices.Equal(au3.GraphicsAPIs.Before, []int{16}) || res3.GraphicsPatched {
		t.Fatalf("记录错误：%+v", au3.GraphicsAPIs)
	}
	// 单文件模式：只认名为 globalgamemanagers 的文件。
	if d, _ := patchGraphicsAPIs(filepath.Join(game, "globalgamemanagers"), false, false, &audit{}, &Result{}); !bytes.Equal(d, data) {
		t.Fatal("单文件模式结果不同")
	}
	if d, _ := patchGraphicsAPIs(filepath.Join(game, "resources.assets"), false, false, &audit{}, &Result{}); d != nil {
		t.Fatal("非 globalgamemanagers 不应处理")
	}
}
