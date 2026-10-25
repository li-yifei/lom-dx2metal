# 让《活侠传》在 Apple Silicon 上原生运行：原理、验收标准，以及怎么让你的 AI agent 替你完成（底稿）

> 状态：底稿，供改写。目标读者是愿意折腾、手边有 Claude Code / Codex 之类编码 agent 的玩家。
> 本文不提供任何游戏文件、转换后的着色器或打包好的 .app；它讲的是"为什么能做"和"做成了长什么样"，具体操作交给你和你的 agent。

## 0. 先说结论

- 《活侠传》是 Unity 2020.3.49f1 + Mono 的游戏。Mono 游戏的托管代码（C#）本身不分平台，Windows 版的 `Mortal_Data` 目录几乎可以原样喂给 Unity 官方的 macOS ARM64 Player。
- 真正和平台绑死的只有四样：**着色器**（Windows 版只带 D3D11 字节码，Mac 需要 Metal）、**Steam 原生库**、**Wwise 音频原生库**、**插件系统（可选）**。
- 着色器是最难的一环，我们把它做成了开源工具 `lom-dx2metal`：拖入 `Mortal_Data`，输出一份着色器已转成 Metal 的副本。
- 剩下三样都是公开的、各自官方渠道可得的东西，给出版本号和验收信号后，一个编码 agent 能在一两次上下文压缩内走完。
- 全程 Rosetta 为零，不用 Wine、不用虚拟机。实测标题画面、新游戏、Story、战斗都正常，赵活系统 mod 可用。

## 1. 原理：为什么 Windows 版数据能直接跑

Unity 的构建分三层：

1. **Player**：引擎本体（`UnityPlayer.dylib`），平台相关，Unity 官方给每个平台都有。Mac ARM64 Mono Player 包含在 Unity Hub 的"Mac Build Support (Mono)"组件里，同版本（2020.3.49f1）的 Player 可以从 Unity 官方 CDN 下载。
2. **托管程序集**：`Managed/*.dll`，IL 字节码，Mono 在运行时 JIT 成 ARM64。不需要改。
3. **资产**：`*.assets`、`sharedassets*`、`StreamingAssets`、Addressables bundle。绝大多数平台无关。唯一的例外是 **Shader 对象里的子程序**：Windows 构建只编译了 D3D11（platform 4）的 DXBC，Mac Player 在运行时找 Metal（platform 14）程序，找不到就报 `Desired shader compiler platform 14 is not available in shader blob`，画面要么黑、要么品红。

所以整个移植 = 官方 Mac Player + 原封不动的托管层 + 原封不动的资产 + **给每个 Shader 补上 Metal 子程序** + 换掉两个原生插件。

## 2. 着色器：`lom-dx2metal` 做了什么，以及三个坑

工具的流程：把每个 D3D11 子程序的 DXBC 用 Unity 自己的 HLSLcc 翻成 MSL，用本机 Metal 驱动编译、链接、取反射，再按 Unity 2020.3 的格式写回资产文件。听起来直白，实际上有三个不写明白就会让 agent 烧掉上亿 token 的坑：

1. **常量和纹理的反射必须写在子程序 blob 的"legacy tail"里，而不是 ParsedForm 的参数表。** Unity 2020.3 的 Metal 运行时只读前者，后者留空。写反了的结果是编译链接全部通过、输入和音效都活着，但画面全黑。证据就在 Unity 官方 Mac Player 自带的 `unity default resources` 里：拿它的 `GUI/Text Shader` 对照一下就清楚了。
2. **同一个常量缓冲区的成员分散在 common 参数表和 variant 参数表里，要按名字合并后再排偏移。** 漏了的结果是某些材质参数错位，表现为战斗里角色贴图发暗。
3. **DXBC 里的内联采样器（inline sampler state）要保留成 MSL 的 `constexpr sampler` 并在 tail 里以 kind 4 登记。** 丢了的结果是 Story 场景大面积色带。

这三条工具已经处理好。你不需要理解它们，但你的 agent 需要知道它们存在，免得它在验证阶段自作聪明地"修"回去。

工具用法（CLI 和 GUI 二选一）：

```
dx2metal convert --in /path/to/Mortal_Data --out /path/to/Mortal_Data.metal --audit audit.json
```

验收信号：工具报告 2550 个程序里转换 ≥ 2546，跳过的 6 个是 XR 专用的 geometry/render-target-array 变体和 GUIRoundedRect，游戏用不到。

## 3. 其余平台相关件：症状、换成什么、怎么验证

这一节故意不写操作步骤。每一项都给三样东西：**不做会看到什么、要换成什么、做对了日志里有什么**。把这一节原样喂给 agent 就够了。

### 3.1 Unity Player

- 症状：没有它什么都跑不起来。
- 换成什么：Unity 官方 Mac Mono Player，版本**必须**是 2020.3.49f1，Variations 里选 `macos_arm64_nondevelopment_mono`。把 Windows 版 `Mortal_Data` 放到 `.app/Contents/Resources/Data`，`Managed`、`Resources`、`StreamingAssets` 跟着走；`Info.plist` 的 bundle id 自己起一个，别和 Steam 版重名。
- 验证：Player.log 开头的架构是 arm64，`Initialize engine version: 2020.3.49f1`，`Metal` 初始化成功；`sysctl sysctl.proc_translated` 对进程返回 0。

### 3.2 Steam

- 症状：启动即退出，或日志里 `SteamAPI_Init` 失败、`DllNotFoundException: steam_api`。
- 换成什么：游戏用的是 Steamworks.NET 20.1.0。托管层 `com.rlabrecque.steamworks.net.dll` 要用 `STEAMWORKS_LIN_OSX` + `STEAMWORKS_X64` 从同版本源码重编（MIT）；原生层 `libsteam_api.dylib` 用 Steamworks.NET 仓库里随附的那份，取 arm64 切片。Mac 上的 Steam 客户端要登录着。
- 验证：日志里拿到 Steam ID，成就和云存档接口不报错。

### 3.3 Wwise 音频

- 症状：`DllNotFoundException: AkSoundEngine`，或者初始化通过但启动时抛 `MissingMethodException`。
- 换成什么：游戏用的是 Wwise 2021.1.14 的 Unity 集成。Audiokinetic 的 EULA 不允许第三方再分发它的库，所以这一步只能你自己做：装 Audiokinetic Launcher，取同主版本（2021.1.x）的 Unity 集成里的 macOS `AkSoundEngine`。版本小号不同会少几个导出，Player.log 会明确告诉你是哪个托管方法找不到；把那一个方法空掉即可，它只涉及 Windows 的设备枚举和系统音频对象上限，不影响实际播放。
- 验证：`Sound engine initialized`，标题画面鼠标悬停按钮有音效，一段 BGM 完整播放。Windows 版的 bank 文件可以直接用。

### 3.4 存档

- 症状：游戏能玩但读不到老存档，或者存档写进了奇怪的地方。
- 换成什么：Windows 版存档在 `AppData/LocalLow/Obb Studio/Mortal`，Mac 上对应 `~/Library/Application Support/Obb Studio/Mortal`。从 Wine/CrossOver 容器迁移过来的话，把容器里的那个目录链接或复制过去。
- 验证：新建一个存档再读出来；迁移的存档能加载。**让 agent 对真实存档只读，先复制一份再试。**

### 3.5 插件系统（可选）

- 症状：BepInEx 在原生 ARM64 Mono 上加载即崩，或 MonoMod 打 detour 时段错误。
- 换成什么：BepInEx 6（Unity.Mono，be 分支）+ UnityDoorstop 4.6，两者都是 LGPL，可以明说：预加载器有两处要改（`PreloaderPreMain` 里在 `SetPlatform()` 之后挂一个修正；原生 arm64 跳过 `XTermFix`），detour 写入要改走 doorstop 的 `doorstop_jit_memcpy`，这个修法来自 BepInEx 仓库的 PR #1402。我们改过的预加载器和修正程序集连同源码一起公开在仓库里。赵活系统（zhaohuo-system）装进去直接能用。
- 验证：BepInEx 日志显示插件加载 0 异常，游戏内 F8 打开赵活系统面板。

### 3.6 打包

- ad-hoc 签名（`codesign --force --deep --sign -`），第一次打开在"隐私与安全性"里放行。
- 不要分发产物。它包含游戏资产和 Unity Player，两者都不是你的。

## 4. 法律边界

- 本方案全部基于你自己购买的正版副本在你自己机器上操作。工具仓库里只有转换器源码和 MIT/LGPL 的第三方代码，没有任何游戏文件。
- Unity Player、Steam 原生库、Wwise 库各有自己的许可，都要从各自官方渠道获取，不要互相传。
- 这是非官方的玩家移植，和开发商鸟熊工作室没有关系。如果官方日后出 Mac 版，请买官方的。

## 5. 给 agent 的任务书

见同目录 `AGENT-BRIEF.md`，可以整段贴进 Claude Code 或 Codex。它只包含版本号、验收信号和安全约束，不包含操作步骤。
