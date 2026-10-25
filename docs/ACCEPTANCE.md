# 验收记录（2026-10-25）

本机：Apple M5 Pro，macOS 27，Go 1.26.2，Xcode 命令行工具。所有命令在 `tools/lom-dx2metal/` 下执行。
oracle 目录默认 `/Users/bob/lom-ws/ports/arm64`（测试参数 `-oracle`），不入库。

## 0. 准备 oracle（只需一次）

DESIGN 最初指定的参考（`ports/arm64/build/metal-bulk/` 与 `MortalARM64-titlediag.app`）是 ports/arm64 的
`convert_all_metal.py` 在 2026-10-23 18:36–19:42 产出的，**早于**内联采样器修复（根因三）和 GUIRoundedRect 数组步长修复，
不再代表当前行为。所以验收 3/4/5 以当前 Python 版（`tools/lom-arm64-converter`）重新生成的产出为准：

```bash
R=/Users/bob/lom-ws/ports/arm64/runs/$(date -u +%Y%m%dT%H%M%SZ)-dx2metal
mkdir -p $R && echo $R > .runs-dir
/Users/bob/lom-ws/tools/lom-arm64-converter/build/venv/bin/python tools/gen_oracle.py \
  "<Steam 库>/LegendOfMortal/Mortal_Data" $R/oracle
```

`gen_oracle.py` 把原版游戏里含 Shader 的文件 APFS 克隆到 `$R/oracle/in`（游戏目录只读），用 Python 版
`bulk.convert_data_dir` 转换 `$R/oracle/out`，每个变体的 `.metal` 留在 `$R/oracle/work`，汇总在 `audit.json`。
本次：`ports/arm64/runs/20261025T040408Z-dx2metal/oracle`，Python 版结果 102 shader / 2550 程序 → 转换 2546，移除 4 个变体。

测试默认从 `.runs-dir` 找 Python oracle，也可以 `go test ./... -args -pyoracle <目录>` 指定。

## 构建与全部测试

```bash
go build ./cmd/dx2metal ./core ./internal/...
go test ./core ./cmd/dx2metal ./internal/...
```

结果（2026-10-25 13:3x）：

```
ok   lom-dx2metal/core            13.6s
ok   lom-dx2metal/internal/hlslcc
ok   lom-dx2metal/internal/metal
ok   lom-dx2metal/internal/params
ok   lom-dx2metal/internal/serialized
ok   lom-dx2metal/internal/shaderblob
```

注：2026-10-25 晚目录重排（`convert/`→`core/`、`cmd/dx2metal-gui/`→`gui/`）并合并为单 module（go 1.27.1）后，
根目录 `go build ./... && go vet ./... && go test ./...` 全部通过，GUI 一并构建。

## 验收 1：tail 往返

```bash
go test ./internal/shaderblob -run 'TestTailRoundTrip' -v
```

- `TestTailRoundTripGUIText`：`testdata/gui_text_shader/program-{0..7}.bin`（Unity 官方 macOS arm64 Player 自带
  `unity default resources` 里 GUI/Text Shader，path_id 10101 的 8 个 Metal 程序，用
  `go test ./internal/shaderblob -run TestExportTestdata -update` 导出）程序与 tail parse→write **8/8 逐字节相等**。
- `TestTailRoundTripFinalPost`：titlediag `globalgamemanagers.assets` path_id 489
  `Hidden/Universal Render Pipeline/FinalPost` 的 **34/34** 个程序 tail 逐字节相等。
- `TestPackRoundTrip`：解包→LZ4 打包→解包，程序逐字节相等。

## 验收 2：SerializedFile 往返

```bash
go test ./internal/serialized -v
```

不改对象时 读→写 与输入逐字节相等，每个 Shader 对象按内嵌类型树 读→写 也逐字节相等：

| 文件 | 原版游戏 | titlediag |
|---|---|---|
| `Resources/unity_builtin_extra` | 387116 字节 / 40 对象 / 40 Shader ✓ | 474320 字节 ✓ |
| `globalgamemanagers.assets` | 39416712 / 9451 / 26 ✓ | 40264272 ✓ |
| `resources.assets` | 70026216 / 4347 / 19 ✓ | 70042704 ✓ |
| `sharedassets0.assets` | 89040212 / 314 / 3 ✓ | 89046608 ✓ |

另有 `TestBundleRoundTrip`：particle_001 bundle 解析→内部 SerializedFile 往返相等→LZ4 重打包→再解析内容相等。

（实现要点：Unity 在对象之间会留空洞，例如 unity_builtin_extra 末尾对象前的 3512 字节，这些字节按原样保留。）

## 验收 3：MSL 对齐

```bash
go test ./core -run TestMSLMatchesPython -v
```

结果：`102 个 shader，2417/2417 个 .metal 逐字节相等`（同一份 HLSLcc 源码 cgo 静态链接、同样 flags `0xB0000`、同样的重排）。
Python 工作目录按 shader 名分目录、同名 shader 后处理的覆盖先处理的，测试按同样顺序生成并覆盖再比较。

参考信息（不判失败）：`go test ./core -run TestLegacyOracleReport -v` 与旧 oracle 比较——
`metal-bulk/` 1068/2452 相同、titlediag blob（bulk-audit 范围）1069/2454 相同；不同的全部是含内联采样器的 shader
（UberPost ×1280、Bloom、DoF、SMAA、StencilDeferred、MotionBlur、Panini、StopNaN、XRMirrorView、UIRDefault…，
即根因三修复带来的 constexpr sampler 差异），加上 GUIRoundedRect / GUIRoundedRectWithColorPerBorder（数组步长修复）
与 Particles/Unlit 的一个同名残留文件。

## 验收 4：blob 对齐

```bash
go test ./core -run TestBlobsMatchPython -v
```

在内存里转换 `oracle/in` 的每个 D3D11 Shader，与 Python 版输出 `oracle/out` 比较：
`102 个 shader，2550 个程序逐字节相等`（platform 14 每个程序的解压后字节，含占位程序）。另外每个 Shader 对象
**除压缩字节外**（ParsedForm、通道表、参数表清空、m_Platforms 等）编码后也逐字节相等。LZ4 压缩器不同
（pierrec/lz4 与 liblz4），压缩流本身允许不同。

## 验收 5：数量

同一测试：程序 2550，转换 **2546**，移除变体 4，与 Python audit 完全一致。CLI 全量运行：

```bash
go build -o bin/dx2metal ./cmd/dx2metal
./bin/dx2metal convert --in $R/oracle/in --out $R/go-out --progress-json > $R/go-progress.jsonl
```

```
完成：102 个 shader / 2550 个程序 → 转换 2546，移除 4 个变体，失败 shader 0  （约 8 秒，退出码 0）
```

跳过项（与 Python 版相同）：`Hidden/VR/BlitFromTex2DToTexArraySlice` 的 2 个程序（render_target_array_index 链接失败）
+ 1 个 geometry 程序、`Hidden/Universal Render Pipeline/XR/XROcclusionMesh` 1 个程序。
GUIRoundedRect 现在能转换，不再在跳过列表里（DESIGN 原文写的 ×2 已过时）。全部 Metal 程序 pad 读取 0。

`dx2metal verify` 对输出的 9 个文件逐个检查：2550 个 Metal 程序 tail 往返不一致 0 个。

## 验收 6：运行

```bash
tools/run_acceptance.sh $R/go-out $R/run
```

脚本：`/bin/cp -Rc` 克隆 `ports/arm64/build/MortalARM64.app` → `MortalARM64-dx2metal.app`；把 Data 里 9 个文件换成 Go 版输出
（已确认 MortalARM64.app 与原版只在 Shader 对象上不同，所以与从原版整体转换等价）；按 `play.sh` 的环境
（`steam_appid.txt`、`SteamAppId`、`-force-metal -screen-fullscreen 0 -screen-width 1280 -screen-height 720`）启动，
存档/偏好隔离到 `$R/run/home`（不碰 playhome）；每 5 秒把**自己的** pid 置前（游戏 runInBackground=false），
40 秒后截图并只 kill 自己启动的 pid。

结果（`ports/arm64/runs/20261025T040408Z-dx2metal/run/`）：

```json
{"alive_at_40s": 1, "platform14_not_available": 0, "unsupported": 0, "exception": 0, "replaced_files": 9}
```

Player.log：Metal 设备初始化、Wwise 与 Steam 初始化成功，加载 CoreScenes 并进入序章故事脚本 `ch1_1`。
`screenshot-window.png`：全新存档目录下游戏直接进了序章客栈（Story 场景，暂停菜单叠在上面），角色、背景、UI、文字完整，
无黑屏、无发暗、无色带。由于全新 home 会自动开始序章，这次截到的是 Story 场景而不是标题画面。
运行前后都没有其他 MortalARM64 进程。

## 附加：globalgamemanagers 图形 API 改写（2026-10-25）

`dx2metal convert` 默认把 `<in>/globalgamemanagers` 里 BuildSettings(ClassID 141) 的 `m_GraphicsAPIs` 改成 `[16]`（Metal）。
BuildSettings 类型树同样从 UnityPy 导出内嵌（`internal/serialized/gen/export_buildsettings_typetree.py`）。

```bash
go test ./internal/serialized -run TestGraphicsAPIs -v
go test ./core -run TestPatchGraphicsAPIs -v
/Users/bob/lom-ws/tools/lom-arm64-converter/build/venv/bin/python tools/gen_gfx_oracle.py "<Steam 库>/LegendOfMortal/Mortal_Data" $R/oracle/gfx
./bin/dx2metal convert --in $R/oracle/in --out $R/go-out2              # in/ 里需有 globalgamemanagers（gen_oracle.py 已会复制）
./bin/dx2metal convert --in $R/oracle/in --out $R/go-out3 --keep-graphics-api
./bin/dx2metal inspect $R/go-out2/globalgamemanagers
```

结果：

- 原版 `globalgamemanagers`（420860 字节）：BuildSettings 按内嵌类型树读→写逐字节相等，整个文件往返逐字节相等。
- 改写后 `[2] → [16]`，文件长度不变，与原版**只差 1 个字节**（偏移 187877，0x02→0x10）；读回为 `[16]`；再跑一次不再改动。
- 与 Python 版比较：`tools/gen_gfx_oracle.py` 用 lom-arm64-converter 的 `assemble.patch_settings` 生成期望输出（418112 字节）。
  18 个对象逐字节相同、元数据（类型表/外部引用）相同、数据区逐字节相同。文件字节不同的**全部原因**是 UnityPy 保存时重算了布局：
  Unity 原文件把数据区起点放在 4096，UnityPy 改成按 16 对齐到 1344，并在末尾补 4 字节对齐；4096−1344−4 = 2748 正好是长度差
  （测试里断言）。本工具保留 Unity 原布局，所以只改那 1 个字节。
- `ports/arm64/build/MortalARM64.app` 里的 `globalgamemanagers`（418136 字节，已是 `[16]`）由 `ports/arm64/build_app.py` 生成，
  它另外改了 PlayerSettings（companyName/productName/submitAnalytics/forceSingleInstance/resizableWindow），所以那 1 个对象不同；
  其余 17 个对象（含 BuildSettings）与 Go 版逐字节相同，布局差异原因同上。
- CLI 全量运行：日志 `globalgamemanagers：图形 API [2] → [16]`，audit `"graphics_apis": {"file": "globalgamemanagers", "before": [2], "after": [16], "written": true}`；
  9 个 shader 资产文件与之前的输出逐字节相同。`--keep-graphics-api` 时输出目录里没有 `globalgamemanagers`，audit 无此项。
  `inspect` 显示 `BuildSettings.m_GraphicsAPIs = [16]（Metal）`（原版显示 `[2]（Direct3D11）`）。
- `go build ./... && go vet ./... && go test ./...` 全部通过（含 gui）。
