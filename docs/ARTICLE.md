# 让《活侠传》在 Apple Silicon 上原生运行：原理、验收标准，以及怎么让你的 AI agent 替你完成

> 在 Apple Silicon 上跑《活侠传》，无需 Wine 或虚拟机。用开源工具 `lom-dx2metal` 转换着色器，再让 Claude Code 或 Codex 等编码 agent 替换原生组件，就能用正版文件拼出零 Rosetta 的原生版本。本文只讲原理与验收标准，操作交给你和 agent。

本文面向愿意动手折腾、手边有 Claude Code 或 Codex 之类编码 agent 的玩家。文中不提供任何游戏文件、转换后的着色器或打包好的 `.app`；这里讨论的是“为什么能做”和“做成了长什么样”，具体执行步骤交由你和你的 agent 完成。

## 一、核心结论与现状

《活侠传》基于 Unity 2020.3.49f1 与 Mono 运行时构建。由于 Mono 游戏的托管代码（C#）本身不绑定平台，Windows 版的 `Mortal_Data` 目录几乎可以原封不动地交给 Unity 官方的 macOS ARM64 Player 使用。

在整套游戏文件里，真正与平台绑死的只有四样东西：
1. **着色器**：Windows 版只带 D3D11 字节码，而 Mac 需要 Metal。
2. **Steam 原生库**。
3. **Wwise 音频原生库**。
4. **插件系统（可选）**。

其中，着色器转换是技术门槛最高的一环。我们为此编写了开源工具 `lom-dx2metal`：只需拖入 `Mortal_Data`，工具就会输出一份已将着色器转换为 Metal 的副本。至于剩下的三项，全部是可以从各自官方公开渠道获取的组件。只要给出明确的版本号和验收信号，一个编码 agent 在一两次上下文压缩内就能把全流程跑通。

这套方案全程 Rosetta 为零，不需要 Wine，也不需要虚拟机。实测标题画面、新游戏、Story、战斗均运行正常，赵活系统 mod 也可以正常加载使用。

---

## 二、移植原理：为什么 Windows 版数据能直接跑

要理解为什么不需要源码就能完成跨平台移植，可以先看 Unity 构建产物的三层结构：

1. **Player（引擎本体）**：即 `UnityPlayer.dylib`，这一层是平台相关的，但 Unity 官方为每个平台都提供了现成构建。Mac ARM64 Mono Player 就包含在 Unity Hub 的“Mac Build Support (Mono)”组件里，同版本（2020.3.49f1）的 Player 可以直接从 Unity 官方 CDN 下载。
2. **托管程序集**：位于 `Managed/*.dll`，本质是平台无关的 IL 字节码。Mono 会在运行时将其直接 JIT 编译为 ARM64 机器码，因此这一层完全不需要改动。
3. **资产文件**：包括 `*.assets`、`sharedassets*`、`StreamingAssets` 以及 Addressables bundle。绝大多数资产本身与平台无关，唯一的例外是 **Shader 对象里的子程序**。Windows 构建只编译了 D3D11（platform 4）的 DXBC 字节码；而 Mac Player 在运行时会寻找 Metal（platform 14）程序，一旦找不到就会报错 `Desired shader compiler platform 14 is not available in shader blob`，表现在游戏里就是画面全黑或变成品红色。

由此可见，整个移植工作的本质就是：**官方 Mac Player + 原封不动的托管层 + 原封不动的资产 + 给每个 Shader 补上 Metal 子程序 + 换掉两个原生插件**。

---

## 三、着色器转换：`lom-dx2metal` 的工作机制与三个深坑

`lom-dx2metal` 的工作流程并不复杂：把每个 D3D11 子程序的 DXBC 用 Unity 自己的 HLSLcc 翻译成 MSL（Metal Shading Language），调用本机 Metal 驱动完成编译、链接并提取反射信息，最后按 Unity 2020.3 的格式写回资产文件。

流程听起来很直白，但底层藏着三个如果不写明白、就会让编码 agent 在排查时烧掉上亿 token 的坑：

1. **常量和纹理的反射必须写在子程序 blob 的“legacy tail”里，而不是 ParsedForm 的参数表。**
   Unity 2020.3 的 Metal 运行时只读取前者，后者是留空的。如果写反了，编译和链接都会顺利通过，游戏输入和音效也都活着，唯独画面全黑。这一点的直接证据就在 Unity 官方 Mac Player 自带的 `unity default resources` 里：拿它里面的 `GUI/Text Shader` 出来对照一下，结构便一目了然。
2. **同一个常量缓冲区的成员分散在 common 参数表和 variant 参数表里，要按名字合并后再排偏移。**
   如果漏掉了合并步骤，会导致某些材质参数发生错位，在游戏里的具体表现就是战斗场景中角色贴图发暗。
3. **DXBC 里的内联采样器（inline sampler state）要保留成 MSL 的 `constexpr sampler` 并在 tail 里以 kind 4 登记。**
   如果丢掉了这项信息，Story 场景就会出现大面积色带。

以上三条细节，`lom-dx2metal` 已经在内部处理妥当。你本人不需要去抠这些二进制格式，但你的 agent 必须知道它们的存在——这能防止 agent 在后续验证阶段遇到问题时，自作聪明地把正确的结构“修”回去。

工具提供 CLI 和 GUI 两种方式，二选一即可。命令行用法如下：

```bash
dx2metal convert --in /path/to/Mortal_Data --out /path/to/Mortal_Data.metal --audit audit.json
```

**验收信号**：工具报告 2550 个程序里转换 ≥ 2546。被跳过的 6 个是 XR 专用的 geometry/render-target-array 变体和 GUIRoundedRect，游戏实际运行中用不到。

---

## 四、其余平台相关组件：症状、替换目标与验收标准

这一节故意不写具体操作步骤。针对每一个组件，下面只给出三样信息：**不做会看到什么症状、要换成什么东西、做对了日志里会有什么信号**。在实际操作时，把这一节原样喂给你的 agent 就足够了。

### 4.1 Unity Player

- **不做会看到什么**：没有它，整个游戏什么都跑不起来。
- **要换成什么**：Unity 官方 Mac Mono Player，版本**必须**严格对应 `2020.3.49f1`，在 Variations 里选择 `macos_arm64_nondevelopment_mono`。将转换后的 Windows 版 `Mortal_Data` 放入 `.app/Contents/Resources/Data`，里头的 `Managed`、`Resources`、`StreamingAssets` 跟着一起走；`Info.plist` 中的 bundle id 自己重新起一个，注意不要和 Steam 版重名。
- **怎么验证**：检查 `Player.log`，开头的架构显示为 `arm64`，包含 `Initialize engine version: 2020.3.49f1`，且 `Metal` 初始化成功；同时在终端对运行中的游戏进程执行 `sysctl sysctl.proc_translated`，返回值为 `0`。

### 4.2 Steam 接口

- **不做会看到什么**：游戏启动即退出，或者日志里出现 `SteamAPI_Init` 失败、`DllNotFoundException: steam_api`。
- **要换成什么**：游戏使用的是 Steamworks.NET `20.1.0`。托管层的 `com.rlabrecque.steamworks.net.dll` 需要用 `STEAMWORKS_LIN_OSX` + `STEAMWORKS_X64` 宏定义从同版本源码重新编译（MIT 许可）；原生层的 `libsteam_api.dylib` 直接使用 Steamworks.NET 仓库里随附的那份，从中取出 arm64 切片。运行前，Mac 上的 Steam 客户端需要保持登录状态。
- **怎么验证**：日志里能正常拿到 Steam ID，且成就和云存档接口均不报错。

### 4.3 Wwise 音频

- **不做会看到什么**：报错 `DllNotFoundException: AkSoundEngine`，或者音频引擎初始化通过了，但游戏启动时抛出 `MissingMethodException`。
- **要换成什么**：游戏使用的是 Wwise `2021.1.14` 的 Unity 集成。由于 Audiokinetic 的 EULA 不允许第三方再分发它的库文件，这一步只能由你自己动手：安装 Audiokinetic Launcher，获取同主版本（`2021.1.x`）Unity 集成里的 macOS `AkSoundEngine`。因为小版本号不同，库里可能会少几个导出函数，此时 `Player.log` 会明确告诉你是哪个托管方法找不到；把那一个方法空掉即可——它只涉及 Windows 的设备枚举和系统音频对象上限，不影响实际音频播放。Windows 版原有的 bank 文件则可以直接复用。
- **怎么验证**：日志输出 `Sound engine initialized`，在标题画面用鼠标悬停按钮时有音效，且能完整播放一段 BGM。

### 4.4 存档目录

- **不做会看到什么**：游戏能正常游玩，但读不到以前的老存档，或者新存档写进了奇怪的位置。
- **要换成什么**：Windows 版存档默认在 `AppData/LocalLow/Obb Studio/Mortal`，而在 Mac 上对应的路径是 `~/Library/Application Support/Obb Studio/Mortal`。如果你是从 Wine 或 CrossOver 容器迁移过来的，把容器里的那个目录链接或复制过去即可。
- **怎么验证**：在游戏里新建一个存档再读取出来；同时确认迁移过来的老存档能够正常加载。**注意安全约束：让 agent 对真实存档保持只读，务必先复制一份备份再做测试。**

### 4.5 插件系统（可选）

- **不做会看到什么**：BepInEx 在原生 ARM64 Mono 上加载即崩溃，或者 MonoMod 在打 detour 时触发段错误（Segmentation fault）。
- **要换成什么**：采用 BepInEx 6（`Unity.Mono`，`be` 分支）+ UnityDoorstop `4.6`。这两者都是 LGPL 许可，因此可以直接说明修改细节：预加载器有两处要改（一是在 `PreloaderPreMain` 里、紧跟 `SetPlatform()` 之后挂一个修正；二是原生 arm64 环境下跳过 `XTermFix`），同时把 detour 写入改走 doorstop 的 `doorstop_jit_memcpy`——这个修法来自 BepInEx 仓库的 PR #1402。我们改过的预加载器和修正程序集连同源码一起公开在仓库里。处理完成后，赵活系统（`zhaohuo-system`）装进去就能直接用。
- **怎么验证**：BepInEx 日志显示插件加载 0 异常，进入游戏内按 F8 能正常打开赵活系统面板。

### 4.6 签名与打包

- **操作与验证**：对最终的 `.app` 执行 ad-hoc 签名（`codesign --force --deep --sign -`），首次打开时在 macOS 的“隐私与安全性”设置里手动放行。
- **约束**：请勿分发打包产物。产物中包含游戏资产和 Unity Player，这两者的版权都不属于你。

---

## 五、法律边界说明

1. **仅限自用**：本方案全部基于你自己购买的正版游戏副本，并在你自己的机器上操作完成。我们的工具仓库里只包含转换器源码以及遵循 MIT / LGPL 协议的第三方代码，不包含任何游戏文件。
2. **严禁互传专有组件**：Unity Player、Steam 原生库、Wwise 库各自受其许可协议约束，都必须从各自的官方渠道获取，玩家之间不要互相传阅或分发。
3. **非官方属性**：这只是非官方的玩家自行移植方案，与开发商鸟熊工作室没有任何关系。如果官方日后推出正式的 Mac 版，请支持并购买官方版本。

---

## 六、给 agent 的任务书

具体交给 AI 执行的指令书见同目录下的 `AGENT-BRIEF.md`，你可以将其整段复制并粘贴进 Claude Code 或 Codex。该文件只包含精确的版本号、各阶段验收信号与安全约束，不包含硬编码的操作步骤，方便 agent 根据你本机的实际环境自主完成构建。
