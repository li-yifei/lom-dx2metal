// 命令 dx2metal-gui 是 dx2metal 的拖放界面：拖入游戏的 *_Data 文件夹，
// 得到 D3D11 → Metal 转换后的资产目录。
//
// GUI 与 CLI 同属一个 module，进程内直接调用 convert.Run（在 goroutine 里），
// 进度事件经 onEvent 切回 UI 线程；取消 = 取消 ctx。不起子进程，不经过 JSON。
package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"lom-dx2metal/cmd/dx2metal-gui/locales"
)

func main() {
	g := &gui{rn: fakeFromEnv(), size: -1}
	mygo.App.WhenReady(func() {
		g.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     locales.AppTitle(),
			Width:     760,
			Height:    700,
			MinWidth:  620,
			MinHeight: 600,
			StateKey:  "main",
			Content:   ui.View(g.view),
		})
		if tag := os.Getenv("LOM_GUI_LANG"); tag != "" { // 脚本化检查用：指定界面语言
			mygo.App.SetLocale(tag)
		}
		if dir := os.Getenv("LOM_GUI_INPUT"); dir != "" { // 脚本化检查用：预选输入
			g.setInput(dir)
			if out := os.Getenv("LOM_GUI_OUT"); out != "" {
				g.outPath, g.outAuto = out, false
			}
			if os.Getenv("LOM_GUI_AUTOSTART") == "1" && g.ready() {
				g.start()
			}
		}
	})
	mygo.App.OnLocaleChanged(func(string) {
		if g.win != nil {
			g.win.SetTitle(locales.AppTitle())
		}
	})
	// 关窗即退出；进行中的转换随 ctx 取消停止。
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		if g.cancelRun != nil {
			g.cancelRun()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
