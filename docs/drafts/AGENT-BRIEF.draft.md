# 任务书：把《活侠传》Windows 版移植为 macOS Apple Silicon 原生版

把下面整段贴给你的编码 agent。方括号里的内容替换成你自己的路径。

---

你在一台 Apple Silicon Mac 上工作，有 Xcode 命令行工具和 Go 1.26+。目标：用我自己 Steam 库里的 Windows 版《活侠传》（AppID 1859910，Unity 2020.3.49f1，Mono，URP 10）做出一个原生 ARM64 的 macOS .app，Rosetta 为零，不用 Wine。

## 硬约束

1. 我的游戏目录 `[/path/to/steamapps/common/LegendOfMortal]`、Steam 目录、任何存档目录、任何 Wine/CrossOver 容器**只读**。需要改的东西先复制到 `[工作目录]` 再改。
2. 所有下载和构建只落在 `[工作目录]` 里，不做系统级安装，不改我的 shell 配置。
3. 每启动一次游戏就带 `-logFile` 写到独立目录，结束时只 kill 你自己启动的 pid。
4. 每完成一步在 `[工作目录]/STATUS.md` 记一段：做了什么、验证命令、结果。上下文被压缩后以这个文件为准。
5. 不要把任何游戏文件、转换后的着色器或 .app 上传到任何地方。

## 要做成什么

| 件 | 用什么 | 做对了的信号 |
|---|---|---|
| 引擎 | Unity 官方 Mac Mono Player **2020.3.49f1**，`macos_arm64_nondevelopment_mono` 变体；Windows 版 `Mortal_Data` 原样作为 Data | Player.log 架构 arm64、`2020.3.49f1`、Metal 初始化成功；`sysctl.proc_translated=0` |
| 着色器 | 开源工具 `lom-dx2metal`（`[仓库地址]`）对 Data 做一次转换，输出替换进 .app | 工具报告转换 ≥2546/2550；Player.log 里 `platform 14 is not available` 0 次、`not supported on this GPU` 0 次；标题画面完整不品红 |
| Steam | Steamworks.NET **20.1.0**：托管层用 `STEAMWORKS_LIN_OSX`+`STEAMWORKS_X64` 从同版本源码重编；`libsteam_api.dylib` 取该仓库随附的 Mac 版 arm64 切片；Mac Steam 客户端保持登录 | 日志拿到 Steam ID，无 `SteamAPI_Init` 失败 |
| Wwise | 游戏用 Wwise **2021.1.14** Unity 集成。从我本机 Audiokinetic Launcher 装的同主版本集成里取 macOS `AkSoundEngine`（我来装 Launcher，你告诉我装哪个版本、文件在哪）。版本小号不同会缺少数导出：看 Player.log 里的 `MissingMethodException` 指向哪个托管方法，把那一个方法在 `AK.Wwise.Unity.API.dll` 里改成直接返回 | `Sound engine initialized`；标题按钮悬停有音效；一段 BGM 完整播放 |
| 存档 | Windows `AppData/LocalLow/Obb Studio/Mortal` ↔ Mac `~/Library/Application Support/Obb Studio/Mortal`；测试阶段用独立 HOME，别碰真实存档 | 新建存档可读回；复制来的旧存档可加载 |
| 打包 | ad-hoc 签名，自起 bundle id | 双击可启动 |
| 插件（可选） | BepInEx 6 Unity.Mono be 分支 + UnityDoorstop 4.6；原生 arm64 需要的两处预加载器修正和 detour 修正在 `lom-dx2metal` 仓库 `arm64-mods/` 目录连源码公开；zhaohuo-system 最新 release | BepInEx 日志 0 异常；游戏内 F8 打开面板 |

## 顺序与门槛

引擎 → 着色器 → 标题可见 → Steam → Wwise → 新游戏/存读一轮 → 打包 → 插件。
每一步的信号没拿到之前不要进下一步；连续两次实验没有新证据就停下来向我汇报，不要自己换方向。

## 已知的坑（直接采信，不要重新验证）

- 着色器三条根因（反射写在 blob legacy tail 而非 ParsedForm；同名 CB 成员跨参数表合并；内联采样器保留为 constexpr sampler）工具已处理，不要去"修"工具输出。
- 后处理 shader（UberPost、Bloom 等）和标题黑屏无关，标题相机关了后处理。
- GNU coreutils 装在 PATH 前面的话 `cp -c`、`sed -i ''` 会失败，用 `/bin/cp`、`/usr/bin/sed`。
---
