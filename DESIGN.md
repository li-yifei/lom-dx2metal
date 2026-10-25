# lom-dx2metal 设计（2026-10-25）

把 Unity 2020.3 Windows 构建里的 D3D11 着色器（DXBC）离线转成 Metal（MSL）并原地注入资产文件，
让官方 macOS ARM64 Mono Player 直接跑 Windows 版数据。首个目标游戏是《活侠传》（Unity 2020.3.49f1、URP 10），
但代码里不写死任何游戏名；对其他只带 D3D11 的 Unity 2020.3.x 游戏同样适用。

这是 `tools/lom-arm64-converter/lom_arm64/shaders/`（Python）与 `ports/arm64/`（研究代码）的 Go 重写。
Python 版是行为参考（oracle），不是要维护的代码。

## 仓库边界（版权）

- 仓库里只有：Go 源码、cgo 用的 C/ObjC 垫片、third_party 里的 HLSLcc 源码（MIT，含我们的 null-guard 补丁）、测试用的**合成**样本。
- 仓库里绝不出现：游戏资产、转换后的着色器、Unity Player、Steam/Wwise 原生库、任何 `.app`。
- 运行时 Unity Player、Steam、Wwise 一概不碰：本工具只做着色器。把玩家的游戏跑起来的其余步骤由文章和 agent 任务书说明。

## 目录

```
lom-dx2metal/
  go.mod                      module github.com/<org>/lom-dx2metal   go 1.26
  cmd/dx2metal/               CLI
  internal/serialized/        Unity SerializedFile（v22）读写、Shader(ClassID 48) 对象定位与重写、UnityFS bundle（LZ4/LZMA）
  internal/shaderblob/        Shader 子程序 blob（LZ4 block）解包/打包、legacy tail 解析/写出
  internal/dxbc/              DXBC 容器、dcl_* 解析、RDEF 合成（移植 hlslcc_bridge.py）
  internal/hlslcc/            cgo 静态链接 third_party 里的 HLSLcc，暴露 TranslateHLSLFromMem
  internal/metal/             cgo + Metal.framework：编译、链接、反射（移植 vendor/metal-tools/reflect_batch.m）
  internal/relayout/          MSL 常量缓冲区结构按 Unity 偏移重排（移植 relayout.py）
  convert/                    公开 Go API：逐 shader 流程编排（移植 bulk.py）+ audit + 进度回调。CLI 与 GUI 都只调它
  internal/progress/          `{"lom":1,...}` 进度事件（与 lom-arm64-converter/lom_arm64/progress.py 同协议）
  third_party/HLSLcc/              从 tools/lom-arm64-converter/vendor/HLSLcc 原样复制（含 UPSTREAM-COMMIT.txt 与补丁）
  cmd/dx2metal-gui/           mygo 拖放界面，同一 module，直接 import `convert` 包，进程内调用，不起子进程
  testdata/                   合成样本：Unity 官方 Player 自带 `unity default resources` 里的 GUI/Text Shader 程序
                              tail 字节（公开可得、不是游戏资产）；其余样本在测试时从 --oracle 目录读取，不入库
  docs/                       文章与 agent 任务书（中文）
```

## "尽量 Go 原生"的具体含义

| 环节 | Python 版 | Go 版 |
|---|---|---|
| 资产文件解析 | UnityPy | 自写 SerializedFile 读写。2020.3 的 Player 构建**不带 typetree**，Shader 类型树以 JSON 内嵌在 Go 代码里（从 UnityPy 对 2020.3.49f1 的 Shader 类型树导出一次，放 `internal/serialized/typetree_shader_2020_3.json`） |
| LZ4 | `lz4.block` | `github.com/pierrec/lz4/v4` block API |
| LZMA（bundle） | UnityPy | `github.com/ulikunitz/xz/lzma` |
| HLSLcc | 子进程 `hlslcc-cli` | cgo 静态链接，无子进程、无临时文件 |
| Metal 编译/反射 | 子进程 `reflect_batch` | cgo ObjC，直接用 `MTLDevice` |
| 签名 | `codesign` 子进程 | 不做（着色器工具不产出 .app） |

产物是一个二进制 `dx2metal`，除 Xcode 命令行工具（cgo 需要 clang + Metal.framework 头）外不依赖任何运行时。

## Go API（GUI 和 CLI 都依赖这个）

```go
package convert

type Options struct {
    In, Out   string      // 输入 Data 目录或单文件；输出目录。In 永远只读
    Only      []string    // 只转这些 shader 名
    Jobs      int
    AuditPath string
}

type Event struct {               // 与 progress.py 的事件一一对应
    Kind    string              // "step" | "progress" | "error" | "result"
    Step    string              // "scan" | "convert" | "write"
    State   string              // step: "start" | "done"
    Current, Total int          // progress
    Detail  string              // 当前 shader 名
    Message string              // error
}

type Result struct { Shaders, Programs, Converted, Removed, FailedShaders int; Out, Audit string; Failed []string }

func Run(ctx context.Context, opt Options, onEvent func(Event)) (*Result, error)
```

- `Run` 可在 goroutine 里跑，`ctx` 取消即中止，`onEvent` 在工作 goroutine 调用，UI 自己切回主线程。
- CLI 的 `--progress-json` 只是把 `Event` 编成下面的 JSON 行；GUI 不经过 JSON。

## CLI 契约

```
dx2metal convert --in <Data目录或单个资产文件> --out <输出目录> [--only <shader名>...] [--audit <json>] [--progress-json] [--jobs N]
dx2metal inspect <资产文件>          列出 Shader 对象、各平台程序数、已有 Metal 程序的 tail 摘要
dx2metal verify <资产文件>           对每个 Metal 程序做 tail parse→write 字节往返，报告不一致
dx2metal version
```

- `--in` 永远只读。`--out` 里生成同名资产文件；bundle 原样重打包。没给 `--out` 时报错，不做原地改写。
- 默认处理 `--in` 下所有含 Shader 对象的文件：`*.assets`、`Resources/unity_builtin_extra`、`StreamingAssets/**/*.bundle`。
- 退出码：0 成功；2 输入错误；3 有 shader 转换失败但其余已输出（audit 里列明）；1 其他错误。
- `--progress-json` 时 stdout 每行一个 JSON 事件（`Event` 的编码），格式与 `lom_arm64/progress.py` 完全一致，`lom` 字段固定 1：
  - `{"lom":1,"event":"step","step":"scan|convert|write","state":"start|done","title":"...","seconds":…}`
  - `{"lom":1,"event":"progress","step":"convert","current":n,"total":m,"unit":"programs","detail":"<shader名>"}`
  - `{"lom":1,"event":"error","step":…,"message":…}`
  - `{"lom":1,"event":"result","ok":bool,"out":"...","audit":"...","shaders":{"shaders":N,"programs":N,"converted":N,"removed":N,"failed_shaders":N}}`
  - 普通日志走 stderr。

## 转换流程（每个 Shader 对象）

与 `bulk.py` 一致：
1. 解包 platform 4（D3D11）的全部子程序 blob；跳过 geometry/hull/domain/raytracing 阶段并记入 audit。
2. 每个 vertex/fragment 程序：从 ParsedForm 参数表 + 子程序参数表（**同名 CB 成员要合并**，见 `names.merge_parameters`）得到名字与偏移；
   解析 DXBC 的 dcl_*；合成 RDEF（部分占用的行按 dword 补标量 pad；customdata 块按长度跳过）；拼回完整 DXBC；HLSLcc 翻译
   （flags `0x20000|0x10000|0x80000`）；顶点着色器做 `postprocess_vs`；按 Unity 偏移重排 CB 结构（`relayout`）。
3. 按 keyword 组合配对 vs/fs，用 Metal 驱动编译 + 建渲染管线取反射；失败的变体从 `m_SubPrograms` 删除并记 audit。
4. 从反射写**完整 legacy tail**（CB 成员去掉 `hlslcc_mtx4x4` 前缀、矩阵 flag=1、kind 1=CB、0=纹理、4=内联采样器、2=StructuredBuffer），
   ParsedForm 参数留空；顶点 channel 表保留；程序头为 Metal 版本 4 格式（`metal_code`）。
5. `replace_platform(14)` 写回，重新 LZ4 打包，重写 SerializedFile 对象表。

三条根因必须在代码注释里写明，它们是本工具存在的理由：
- 常量/纹理反射在 blob legacy tail，不在 ParsedForm（否则黑屏）。
- 同名 CB 成员跨 common/variant 参数表合并（否则角色贴图发暗）。
- 内联采样器要保留为 constexpr sampler 并写 kind 4（否则 Story 场景色带）。

## 验收（oracle 对比，而不是凭感觉）

oracle 目录由测试参数 `-oracle <dir>` 指定，默认 `/Users/bob/lom-ws/ports/arm64`，不入库。

1. **tail 往返**：Unity 官方 Player `unity default resources` 的 GUI/Text Shader 8 个程序、游戏 FinalPost 34 个程序，parse→write 逐字节相等。
2. **SerializedFile 往返**：不改任何对象时，读→写输出与输入逐字节相等（`unity_builtin_extra`、`globalgamemanagers.assets`、`resources.assets`、`sharedassets0.assets`）。
3. **MSL 对齐**：对 `ports/arm64/build/metal-bulk/` 里 Python 版产出的每个 `.metal`，Go 版生成的 MSL 文本逐字节相等（同一 HLSLcc、同一 flags、同一重排）。
4. **blob 对齐**：对 `ports/arm64/build/MortalARM64-titlediag.app/Contents/Resources/Data` 里由 `convert_all_metal.py` 转出的 shader
   （以 `investigation/assets/bulk-audit.json` 为准，排除走手工 manifest 的那几个），Go 版的 platform 14 程序 blob 逐字节相等。
5. **数量**：2550 个程序里转换数 ≥ 2546；跳过项与 Python 版相同（XR geometry/RTAI ×4、GUIRoundedRect ×2）。
6. **运行**：用 Go 版输出替换一份 `MortalARM64.app` 克隆里的 Data，`ports/arm64/play.sh` 式启动 40 秒，
   Player.log 里 `platform 14 is not available` 0 次、unsupported 0、Exception 0，截图标题画面完整。**绝不 kill 用户自己启动的 MortalARM64 进程**，
   只 kill 自己启动的那个 pid。

## 安全约束（对所有会话）

- 真实游戏目录（Steam 库）、存档、Whisky/Wine 容器、`ports/arm64/build/*.app`、`playhome/` 一律只读。
- 所有写入只落在 `tools/lom-dx2metal/` 和会话自己的 `ports/arm64/runs/<时间戳>/`。
- 依赖只装在项目目录（go module cache 除外），不做系统级安装。
- 文档、注释、GUI 文案一律中文（简体为主，GUI 另有繁体与日文目录）。

注：C/C++/ObjC 第三方源码放 `third_party/`，不要用 `vendor/`，那个目录名对 go 工具链有特殊含义（会触发 `-mod=vendor`）。


更新 2026-10-25：GUI 改为同 module 内 `cmd/dx2metal-gui`，进程内调用 `convert.Run`，不再包装 CLI。核心会话负责把流程编排放在公开包 `convert/` 而不是 `internal/convert/`，并保证 `Run` 不依赖进程级全局状态（可在同一进程多次调用）。cgo 侧 HLSLcc 若崩溃会拖垮 GUI 进程，所以 HLSLcc 的 null-guard 补丁必须保留，并且 `Run` 对单个程序的失败只记 audit 不 panic。

更新 2026-10-25（核心会话，实现 Go 版时对本文的修正）：
- **oracle**：验收 3/4 原定的 `ports/arm64/build/metal-bulk/` 与 titlediag 早于内联采样器修复（根因三）和 GUIRoundedRect 数组步长修复，
  含内联采样器的 shader 必然不同。改为用 `tools/gen_oracle.py` 以当前 Python 版（lom-arm64-converter）从原版游戏重新生成
  oracle（in/out/work/audit.json，放在本次 `ports/arm64/runs/<时间戳>/oracle`），测试参数 `-pyoracle`。旧参考仍作为参考信息报告
  （`TestLegacyOracleReport`），差异全部落在根因三相关 shader、GUIRoundedRect 与一个同名残留文件上。
- **验收 5 跳过项**：现在是 4 个程序（VR BlitFromTex2DToTexArraySlice 2 个链接失败 + 1 个 geometry、XROcclusionMesh 1 个），
  GUIRoundedRect 已能转换，转换数 2546。
- **验收 1 testdata**：存的是 GUI/Text Shader（unity default resources，path_id 10101，那份文件标的是 2020.3.48f1）8 个 Metal 程序的完整字节，
  不只是 tail；`go test ./internal/shaderblob -run TestExportTestdata -update` 可重新导出。
- **module 路径**：用 `lom-dx2metal`（原文 `github.com/<org>/…` 是占位，确定 org 后改 go.mod 与 import 即可）。
- **进度事件**：没有单独的 `internal/progress` 包；`convert.Event` 由 `cmd/dx2metal` 编码成 JSON 行（字段顺序、分隔符与 progress.py 的
  `json.dumps` 一致）。Event 额外带 Title/Seconds/Unit/OK/Result 字段，Result 额外带 PadReads。
- **输出**：`--out` 里只写改过的资产文件和 `dx2metal-audit.json`（默认 audit 路径）；`--in` 为目录时还扫描 `level*`（与 Python 版相同）。
- **HLSLcc 崩溃隔离**：Python 版靠子进程；cgo 版在垫片里翻译期间临时接管 SIGSEGV/SIGBUS，本线程的崩溃 siglongjmp 回来记为该变体失败，
  其他线程的信号转交 Go 运行时。研究目录里的 6 个会让 hlslcc-cli 段错误的 DXBC 样本在测试里返回错误而不是崩溃。
- **保留的 Python 行为**：bulk.keyword_key 读的是 2020.3 类型树里不存在的 `m_KeywordIndices`（实际字段叫 `m_GlobalKeywordIndices`），
  所以配对只比较局部关键字；为了逐字节一致照搬，代码里有注释。
- **SerializedFile**：Unity 会在对象之间留空洞（unity_builtin_extra 末尾有 3512 字节），写出时按原样保留，所以往返逐字节相等。

更新 2026-10-25（晚）：两个会话完成，六条验收全部通过（`docs/ACCEPTANCE.md`）。与原设计的两处偏差：
- GUI 做成了子 module `cmd/dx2metal-gui/go.mod`（`replace lom-dx2metal => ../..`），而不是根 module 的一部分：mygo v0.6.3 要求 go ≥ 1.27.1，本机 1.26.2，根 module 保持 1.26。等本机升到 1.27 后可以并回根 module。进程内调用 `convert.Run` 这一点没变。
- 跳过项变成 4 个（XR 的 3 个程序 + XROcclusionMesh 1 个），GUIRoundedRect 现在能转。前文"×2"已过时。
- `arm64-mods/` 放 BepInEx 在 Apple Silicon 上的补丁（LGPL，连源码），从 `lom-arm64-converter/vendor/bepinex-arm64` 复制，供文章与任务书引用。
已知小问题：GUI 窗口在后台时不重绘，转换结束要激活窗口才显示结果；疑似 mygo 行为，未深究。
