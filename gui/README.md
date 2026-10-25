# gui：dx2metal 的拖放图形界面

这是 [dx2metal](../README.md) 的拖放图形界面（基于 [egoist/mygo](https://github.com/egoist/mygo) v0.6.3，原生 macOS 应用，仅支持 arm64）。
面向喜欢自己动手的技术向玩家：把游戏的 `*_Data` 文件夹拖进窗口，点一下“开始转换”，就能拿到完成 D3D11 → Metal 转换后的资产目录。
界面提供简体中文、繁體中文、日本語三语，可在右上角随时切换；默认跟随系统语言，其他语言环境则自动回落到简体。

> 本工具为非官方工具，与《活侠传》及 Unity 的权利人无关。转出来的包里包含游戏本体的文件，既不是我们的也不是你的，只能留着自己在自己电脑上玩。千万别打包发给别人、传网盘或上传到任何地方；要是朋友也想玩，请让他自己买游戏、自己动手转。
> 这两条提示固定显示在窗口底部。

## 使用

1. 把 `Mortal_Data`（或任意 Unity 2020.3 游戏的 `*_Data`）拖进窗口，或者点击“选择文件夹…”。
   程序会即时检查目录里有没有 `globalgamemanagers` 和 `Resources/unity_builtin_extra`，如果不符合要求，会直接说明缺了什么文件。
   要是直接拖入了游戏根目录，只要里面恰好只有一个 `*_Data` 子目录，程序也会自动进入它。
2. 输出目录默认设在输入目录旁边的 `<名字>_Data.metal`，也可以按需修改。界面上会显示输入目录的大小（转换后的输出占用与之相近），当输出盘剩余空间不够时会给出警告；
   输出目录不能与输入目录相同，也不能放在输入目录里面。如果输出目录里已经有内容，开始转换前会先确认。
3. 点击“开始转换”后，界面会显示总进度条（扫描 10% / 转换 80% / 写出 10%）、当前正在处理的 shader 名、已处理程序数与失败数、预计时间，以及可展开查看的日志。转换过程中可随时取消。
4. 转换结束后：
   - 成功：展示成功转换的程序数、删除的变体数、失败 shader 数以及总用时。
   - **部分成功**（`Run` 本身无错误，但有部分 shader 转换失败）：其余正常内容均已输出，界面会列出失败的 shader（最多展示 30 条，其余详见 audit JSON）。
   - 失败：显示具体错误原因和日志末尾内容，方便直接复制日志或重试。
   - 按钮：支持直接打开输出目录、打开 audit JSON，或者继续转换另一个。

## 与转换包的接口

GUI 与 CLI 同属根 module，GUI 进程内直接调用 `core.Run`（详见 [DESIGN.md](../DESIGN.md)「Go API」），不额外拉起子进程，也不经过 JSON 中转：

```go
res, err := core.Run(ctx, core.Options{In: …, Out: …, Log: …}, func(ev core.Event) { … })
```

- `Run` 在独立的 goroutine 里运行；`onEvent` 与 `Options.Log` 在工作 goroutine 中触发回调，再经由 `execute` 的 `post`（即窗口的 `Update`）切回 UI 线程更新界面状态。
- 点击取消即直接取消 `ctx`；直接关窗时也会触发同样的取消逻辑。
- 结局判定：取消操作优先级最高；若 `err != nil` 则判为失败（遇到 `*core.InputError` 时会给出“输入有误”的友好提示，其余情况显示 `err` 文本，并将出错步骤标红）；若 `err == nil` 且 `Result.FailedShaders > 0` 则判为部分成功（与 CLI 退出码 3 保持一致）；其余情况均为成功。
- 部分成功时，失败列表取自 `Result.Failed`（格式为“文件:path_id 名字”，包含只删了变体的 shader），若为空则退回使用 `error` 事件（将 `Detail` 当作 shader 名）；完全成功时则不列出。
- 转换进度取自 `convert` 步的 `Current/Total`，并将 `Detail` 当作当前 shader 名；各步骤的 id 依次为 `scan`、`convert`、`write`。
- 核心调用通过 `runner` 接口隔离（见 `runner.go`）：正式运行使用 `realRunner`，跑测试与生成截图时则用 `fakeRunner`（会吐出逼真的事件序列，支持注入失败或错误，也支持中途取消）。
- 运行时不写日志文件；界面日志区直接展示 `Options.Log` 输出的每一行，点击“复制日志”即可整体复制。

## 构建

```bash
./build.sh        # 生成 build/dx2metal GUI.app（arm64，ad-hoc 签名，自包含）；先跑 go vet 与 go test
```

根目录的 `go.mod` 已经设为 `go 1.27.1`（满足 mygo v0.6.3 的要求），配合 `GOTOOLCHAIN=auto` 会按需自动下载工具链；由于 GUI 与 `core`、`cmd/dx2metal` 同属一个 module，
这里不再需要单独的 go.mod。旁边的 `env.sh` 只负责设置 `CGO_ENABLED=1`（因为 mygo build 默认会将其置为 0，而 core 包需要用到 cgo）。

## 脚本化检查（用假 runner 截图）

```bash
LOM_GUI_FAKE=partial FAKE_DELAY=100ms FAKE_PROGRAMS=200 \
LOM_GUI_LANG=zh-Hans LOM_GUI_INPUT=/path/to/Mortal_Data LOM_GUI_OUT=/tmp/out LOM_GUI_AUTOSTART=1 \
"build/dx2metal GUI.app/Contents/MacOS/dx2metal GUI"
```

- `LOM_GUI_FAKE=ok|partial|fail`：启用假 runner 模拟运行；如果不设置或设为 `real`，则调用真正的 `core.Run`。可配合调节 `FAKE_DELAY`（Go 时长格式，默认 20ms）与 `FAKE_PROGRAMS`（模拟的总程序数）。
- `LOM_GUI_INPUT` 用于预选输入目录、`LOM_GUI_OUT` 指定输出目录、`LOM_GUI_AUTOSTART=1` 让程序在校验通过后立即开始转换、`LOM_GUI_LANG` 则用于指定界面语言。

## 测试

```bash
source env.sh && go vet . && go test .
LOM_GUI_SNAPSHOTS=$PWD/screenshots/tester go test .   # 同时把各状态渲染成 PNG（三语）
```

`gui_test.go` 基于 `ui.Tester` 在无窗口模式下运行，测试范围覆盖：输入目录校验、输出位置规则、事件回放与步骤状态机、
各种结局（成功/部分成功/失败/取消）、失败列表、ETA 估算、经由 `execute` 用假 runner 跑完完整流程（含 Log 回调与中途取消），以及三语环境下的各状态视图。

## 目录

```
gui/
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
| `01-idle.png` | 空闲状态 |
| `02-ready.png` | 输入校验通过，显示目录大小与默认输出路径 |
| `03-running.png` | 转换进行中 |
| `04-done.png` | 转换成功 |
| `04b-partial.png` | 部分成功，列出失败的 shader |
| `04c-partial-ja.png` | 同上，切换为日语界面 |
| `05-failed.png` | 转换失败：展示错误原因与日志末尾内容 |
| `06-invalid-zh-Hant.png` | 拖入不合格目录时的提示（繁體） |
| `10-real-done.png` | 真实运行 `core.Run`（只读读取 MortalARM64.app 的 Data，输出到临时目录） |

## 已知限制

- 已用真实的 `core.Run` 在 MortalARM64.app 的 Data（只读）上实测跑通：耗时 4 秒、2438 个程序转换成功、0 个失败 shader（窗口在后台时不重绘，切回前台激活后显示最终结果），效果见 `screenshots/10-real-done.png`。
- 当 `Result.Audit` 指向的文件不存在时，“打开 audit JSON”按钮会置灰。
- 中途取消后，输出目录里可能留有尚未写完的不完整文件，界面上会给出相应提示。
- 仅提供 arm64 版本；由于采用 ad-hoc 签名且不做公证，首次打开时可能需要在“隐私与安全性”里手动放行。
