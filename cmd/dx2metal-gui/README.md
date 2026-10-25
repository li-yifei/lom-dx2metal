# dx2metal-gui

[dx2metal](../../README.md) 的拖放图形界面（[egoist/mygo](https://github.com/egoist/mygo) v0.6.3，原生 macOS，仅 arm64）。
面向技术向玩家：拖入游戏的 `*_Data` 文件夹，点“开始转换”，得到 D3D11 → Metal 转换后的资产目录。
界面为简体中文、繁體中文、日本語三语，右上角切换；默认跟随系统语言，其他语言回落到简体。

> 非官方工具，与《活侠传》及 Unity 的权利人无关。转出来的包里有游戏本体的文件，不是我们的也不是你的，只能你自己在自己电脑上玩。千万别打包发给别人、传网盘或上传到任何地方；朋友想玩，请让他自己买游戏、自己转。
> 这两条提示固定显示在窗口底部。

## 使用

1. 把 `Mortal_Data`（或任意 Unity 2020.3 游戏的 `*_Data`）拖进窗口，或点“选择文件夹…”。
   即时检查里面有 `globalgamemanagers` 和 `Resources/unity_builtin_extra`，不是就提示缺什么。
   拖入游戏根目录时，若其中恰有一个 `*_Data` 子目录，会自动进入它。
2. 输出目录默认是输入旁边的 `<名字>_Data.metal`，可改。界面显示输入目录大小（输出占用相近），输出盘剩余空间不够时警告；
   输出不能与输入相同、也不能在输入之内。输出目录已有内容时开始前会确认。
3. “开始转换”：总进度条（扫描 10% / 转换 80% / 写出 10%）、当前 shader 名、已处理程序数与失败数、预计时间、可展开的日志。可随时取消。
4. 结束后：
   - 成功：显示成功转换的程序数、删除的变体数、失败 shader 数、用时。
   - **部分成功**（`Run` 无错误但有 shader 失败）：其余已输出，列出失败的 shader（最多 30 条，其余见 audit JSON）。
   - 失败：错误原因和日志末尾，可复制日志、重试。
   - 按钮：打开输出目录、打开 audit JSON、转换另一个。

## 与转换包的接口

GUI 与 CLI 同属根 module `lom-dx2metal`，GUI 进程内直接调用 `convert.Run`（见 [DESIGN.md](../../DESIGN.md)「Go API」），不起子进程、不经过 JSON：

```go
res, err := convert.Run(ctx, convert.Options{In: …, Out: …, Log: …}, func(ev convert.Event) { … })
```

- `Run` 在 goroutine 里跑；`onEvent` 与 `Options.Log` 在工作 goroutine 回调，经 `execute` 的 `post`（= 窗口的 `Update`）切回 UI 线程再改状态。
- 取消 = 取消 `ctx`；关窗时同样取消。
- 结局：取消优先；`err != nil` = 失败（`*convert.InputError` 给“输入有误”友好提示，其余显示 `err` 文本，出错步骤标红）；`err == nil` 且 `Result.FailedShaders > 0` = 部分成功（与 CLI 退出码 3 一致）；否则成功。
- 部分成功时的失败列表取 `Result.Failed`（“文件:path_id 名字”，含只删了变体的 shader），为空时退回 `error` 事件（`Detail` 当 shader 名）；成功时不列。
- 进度取 `convert` 步的 `Current/Total`，`Detail` 当作当前 shader 名；步骤 id 为 `scan`、`convert`、`write`。
- 调用被 `runner` 接口隔离（`runner.go`）：正式用 `realRunner`，测试与截图用 `fakeRunner`（吐逼真事件序列，可注入失败/错误、可取消）。
- 没有写日志文件；日志区显示 `Options.Log` 的行，“复制日志”可整体复制。

## 构建

```bash
./build.sh        # 生成 build/dx2metal GUI.app（arm64，ad-hoc 签名，自包含）；先跑 go vet 与 go test
```

`env.sh` 把 Go 的 module/build 缓存放进本目录的 `.gopath`、`.gocache`，不碰系统目录，并设 `GOTOOLCHAIN=go1.27.1`、`-modfile=go.gui.mod`。

**module 说明**：mygo v0.6.3 要求 `go >= 1.27.1`，根 `go.mod` 是 `go 1.26`（`go get` 在 1.26 下会直接拒绝）。
所以本目录是**独立 module** `lom-dx2metal/cmd/dx2metal-gui`（`go.mod`，`replace lom-dx2metal => ../..`），只用根 module 的公开 `convert` 包；
根 module 的 `go build ./...` 因此不会碰 GUI，根 `go.mod` 保持 go 1.26。若将来根 module 升到 go 1.27.1，可把 GUI 并回去（删本目录 `go.mod`/`go.sum`，根加 mygo 的 require 与 tool 行）。
`env.sh` 还设了 `CGO_ENABLED=1`（mygo build 默认置 0，而 convert 包用 cgo）；Go 的模块/构建缓存放在 `~/Library/Caches/dx2metal-gui/`，不在仓库里。

## 脚本化检查（用假 runner 截图）

```bash
LOM_GUI_FAKE=partial FAKE_DELAY=100ms FAKE_PROGRAMS=200 \
LOM_GUI_LANG=zh-Hans LOM_GUI_INPUT=/path/to/Mortal_Data LOM_GUI_OUT=/tmp/out LOM_GUI_AUTOSTART=1 \
"build/dx2metal GUI.app/Contents/MacOS/dx2metal GUI"
```

- `LOM_GUI_FAKE=ok|partial|fail`：用假 runner；不设或 `real` 用真的 `convert.Run`。`FAKE_DELAY`（Go 时长，默认 20ms）、`FAKE_PROGRAMS`（总程序数）。
- `LOM_GUI_INPUT` 预选输入、`LOM_GUI_OUT` 指定输出、`LOM_GUI_AUTOSTART=1` 校验通过即开始、`LOM_GUI_LANG` 指定界面语言。

## 测试

```bash
source env.sh && go vet . && go test .
LOM_GUI_SNAPSHOTS=$PWD/screenshots/tester go test .   # 同时把各状态渲染成 PNG（三语）
```

`gui_test.go`（无窗口，`ui.Tester`）覆盖：输入目录校验、输出位置规则、事件回放与步骤状态机、
各结局（成功/部分成功/失败/取消）、失败列表、ETA、经 `execute` 用假 runner 跑完整流程（含 Log 与取消）、三语各状态视图。

## 目录

```
cmd/dx2metal-gui/
  main.go        入口：窗口、环境变量开关
  runner.go      runner 接口、realRunner、execute（goroutine + 切回 UI 线程）、fakeRunner
  run.go         事件状态机（步骤、进度、失败列表、Run 返回 → 结局）
  input.go       输入/输出目录校验、目录大小、磁盘空间
  view.go        界面：空闲/进行中/结果/失败、拖放、对话框、语言切换、底部声明
  gui_test.go    测试
  locales/       zh-Hans.json（源语言）、zh-Hant.json、ja.json，catalog_gen.go 由 `mygo generate` 生成
  resources/icon.png   图标（scripts/make_icon.py 纯图形绘制，需 Pillow）
  screenshots/   真实窗口截图（假 runner 驱动）；tester/ 为测试渲染帧（不入库）
  build.sh env.sh mygo.json go.mod go.sum
```

## 截图（`screenshots/`，在打包好的 app 上用假 runner 截取）

| 文件 | 状态 |
| --- | --- |
| `01-idle.png` | 空闲 |
| `02-ready.png` | 输入校验通过，显示大小与默认输出 |
| `03-running.png` | 转换进行中 |
| `04-done.png` | 成功 |
| `04b-partial.png` | 部分成功，列出失败 shader |
| `04c-partial-ja.png` | 同上，日语界面 |
| `05-failed.png` | 失败：原因与日志末尾 |
| `06-invalid-zh-Hant.png` | 拖入不合格目录的提示（繁體） |
| `10-real-done.png` | 真实 `convert.Run`（只读 MortalARM64.app 的 Data，输出到临时目录） |

## 已知限制

- 已用真实 `convert.Run` 在 MortalARM64.app 的 Data（只读）上跑通：4 秒、2438 个程序转换成功、0 个失败 shader（窗口在后台时不重绘，激活后显示最终结果），见 `screenshots/10-real-done.png`。
- “打开 audit JSON”在 `Result.Audit` 指向的文件不存在时置灰。
- 取消后输出目录里可能留有不完整的文件，界面会提示。
- 只有 arm64；不做公证，首次打开可能需要在“隐私与安全性”里放行（ad-hoc 签名）。
