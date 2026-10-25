# lom-dx2metal

把 Unity 2020.3 Windows 构建的 D3D11 着色器离线转成 Metal 并注入资产文件，让 Unity 官方 macOS ARM64 Player 直接运行 Windows 版数据。
面向《活侠传》做的，对其他只带 D3D11 着色器的 Unity 2020.3.x 游戏同样适用。

仓库只含转换器源码。游戏资产、Unity Player、Steam 与 Wwise 原生库都不在这里；把游戏整体跑起来的步骤见 `docs/`。

这是玩家自制的非官方工具，和《活侠传》的开发商、Unity 都没有关系。转出来的包里有游戏本体的文件，不是我们的也不是你的，只能你自己在自己电脑上玩。千万别打包发给别人、传网盘或上传到任何地方；朋友想玩，请让他自己买游戏、自己转。

设计与验收标准见 [DESIGN.md](DESIGN.md)。

## 两种用法：图形界面或命令行

**图形界面**（推荐给大多数玩家）：`cmd/dx2metal-gui/`，原生 macOS 窗口，把游戏的 `*_Data` 文件夹拖进去、点“开始转换”即可。
简体中文、繁體中文、日本語三语，进度、失败列表、打开输出目录和 audit 都在界面里。详见 [cmd/dx2metal-gui/README.md](cmd/dx2metal-gui/README.md)。

![图形界面：转换完成](cmd/dx2metal-gui/screenshots/10-real-done.png)

```bash
cd cmd/dx2metal-gui && ./build.sh      # 生成 build/dx2metal GUI.app（arm64，ad-hoc 签名）
```

**命令行** `dx2metal`：适合脚本化和给 agent 用，下面详述。两者都只调用同一个 Go 包 `convert`，结果完全一致。

## 构建命令行工具

需要 Apple Silicon Mac、Xcode 命令行工具（`xcode-select --install`，cgo 要用 clang 与 Metal.framework）和 Go 1.26+。
HLSLcc 源码在 `third_party/HLSLcc/`，由 cgo 静态编进二进制；不需要 Python、不调用任何子进程。

```bash
go build -o bin/dx2metal ./cmd/dx2metal
```

首次构建要编译 HLSLcc，约半分钟。产物 `bin/dx2metal` 是单个可执行文件。

## 用法

```bash
# 转换整个 Data 目录（只读），把改过的资产文件写到输出目录（保持相对路径），并写 audit
bin/dx2metal convert --in "<游戏>/Mortal_Data" --out out/Data

# 只转某几个 shader；指定 audit 路径；给前端用的 JSON 进度
bin/dx2metal convert --in "<游戏>/Mortal_Data" --out out/Data --only "Hidden/Universal Render Pipeline/UberPost" --audit out/audit.json --progress-json

# 查看一个资产文件里的 Shader、各平台程序数与 Metal 程序的 tail 摘要
bin/dx2metal inspect out/Data/globalgamemanagers.assets

# 检查每个 Metal 程序的 legacy tail 能否逐字节往返
bin/dx2metal verify out/Data/globalgamemanagers.assets
```

- `--in` 永远只读；`--out` 必须给，不做原地改写。输出目录里只有**改过的**资产文件（与 `--in` 下相对路径相同）和
  `dx2metal-audit.json`；把它们覆盖到游戏 Data 的副本里即可。
- 默认扫描 `Resources/unity_builtin_extra`、`*.assets`、`level*`、`StreamingAssets/**/*.bundle`，只处理只带 D3D11 程序的 Shader。
  bundle 以 LZ4 重新打包。
- 退出码：0 成功；2 输入错误；3 有 shader 完全没能转换（其余已输出，audit 列明）；1 其他错误。
- 《活侠传》上的结果：102 个 shader / 2550 个程序 → 转换 2546 个（与 Python 版逐字节一致），约 8 秒。
  剩下 4 个是 VR/XR 用的程序，游戏不用。

## Go API

GUI 与 CLI 都只调用公开包 `convert`：

```go
res, err := convert.Run(ctx, convert.Options{In: in, Out: out}, func(e convert.Event) { /* 进度 */ })
```

可在 goroutine 里运行、`ctx` 取消即中止；单个程序失败只记入 audit；HLSLcc 崩溃（段错误）会被隔离成该变体失败，不会拖垮进程。

## 测试与验收

```bash
go test ./convert ./cmd/dx2metal ./internal/...
```

需要 oracle 数据（Python 版产出、原版游戏文件）的测试在数据不存在时自动跳过。六条验收的命令与结果见
[docs/ACCEPTANCE.md](docs/ACCEPTANCE.md)。

## 三条根因

工具存在的理由，代码里都有注释：

1. 常量/纹理反射在程序 blob 末尾的 legacy tail 里，不在 ParsedForm。tail 写错会黑屏（`internal/shaderblob`、`convert/tail.go`）。
2. 同名常量缓冲区要跨 common/variant 参数表按成员合并，否则成员变成永远是 0 的 pad，角色贴图发暗（`internal/params.Merge`）。
3. 内联采样器要以 `sampler_LinearClamp` 这类名字进 RDEF，保留为 constexpr sampler，否则 LUT 用 point 采样，Story 场景出现色带
   （`internal/params.InlineSamplerName`）。
