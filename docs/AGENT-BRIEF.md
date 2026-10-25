# 任务书：把《活侠传》Windows 版移植为 macOS Apple Silicon 原生版

把下面分割线后的整段内容贴给你的编码 agent，并将方括号里的占位符替换成你自己的实际路径或地址。

---

你在一台 Apple Silicon Mac 上工作，环境已具备 Xcode 命令行工具和 Go 1.26+。
任务目标：基于我本机 Steam 库中的 Windows 版《活侠传》（AppID `1859910`，Unity `2020.3.49f1`，Mono，URP 10），构建出一个原生 ARM64 的 macOS `.app`（Rosetta 转译为零，不使用 Wine）。

## 硬约束

1. **源目录只读**：我的游戏目录 `[/path/to/steamapps/common/LegendOfMortal]`、Steam 目录、任何存档目录、任何 Wine/CrossOver 容器均**严格只读**。任何需要修改的文件，必须先复制到 `[工作目录]` 中再操作。
2. **工作区隔离**：所有下载产物和构建输出必须全部落在 `[工作目录]` 内；不做任何系统级安装，不修改我的任何 shell 配置文件。
3. **进程与日志管理**：每次启动游戏都必须带 `-logFile` 参数并将日志写入独立目录；测试结束清理进程时，只允许 `kill` 你自己启动的进程 PID。
4. **进度持久化**：每完成一步，必须在 `[工作目录]/STATUS.md` 中追加一段记录：做了什么、验证命令、实际结果。上下文被压缩后，以此文件作为唯一进度依据。
5. **严禁上传**：不要把任何游戏文件、转换后的着色器或构建出的 `.app` 上传到任何地方。

## 要做成什么

| 件 | 用什么 | 做对了的信号 |
|---|---|---|
| 引擎 | 使用 Unity 官方 Mac Mono Player **2020.3.49f1** 的 `macos_arm64_nondevelopment_mono` 变体；将 Windows 版 `Mortal_Data` 原样作为 Data 目录放入 `.app` | `Player.log` 显示架构为 `arm64`、版本为 `2020.3.49f1`、Metal 初始化成功；运行进程的 `sysctl.proc_translated=0` |
| 着色器 | 使用开源工具 `lom-dx2metal`（https://github.com/li-yifei/lom-dx2metal）对 Data 目录执行一次转换，将转换输出替换进 `.app` | 工具报告转换成功数 `≥2546/2550`；`Player.log` 中 `platform 14 is not available` 出现 0 次、`not supported on this GPU` 出现 0 次；标题画面显示完整且无品红色块 |
| Steam | Steamworks.NET **20.1.0**：托管层使用 `STEAMWORKS_LIN_OSX` + `STEAMWORKS_X64` 编译宏从同版本源码重新编译；`libsteam_api.dylib` 取自该仓库随附的 Mac 版 `arm64` 切片；运行期间保持 Mac Steam 客户端处于已登录状态 | 日志成功获取到 Steam ID，且无 `SteamAPI_Init` 失败报错 |
| Wwise | 游戏使用 Wwise **2021.1.14** Unity 集成。从我本机 Audiokinetic Launcher 安装的同主版本集成包中提取 macOS 版 `AkSoundEngine`（由我来安装 Launcher，你负责告诉我需要安装哪个具体版本、目标文件在哪个路径）。因小版本号不同会缺少少数原生导出符号：查看 `Player.log` 中的 `MissingMethodException` 指向哪个托管方法，在 `AK.Wwise.Unity.API.dll` 中把那一个方法改成直接返回 | 日志输出 `Sound engine initialized`；标题界面按钮悬停有音效；一段 BGM 能够完整播放 |
| 存档 | 存档目录映射为 Windows `AppData/LocalLow/Obb Studio/Mortal` ↔ Mac `~/Library/Application Support/Obb Studio/Mortal`；测试阶段必须指定独立 `HOME` 环境变量运行，严禁触碰真实存档 | 新建存档可正常保存并读回；复制进来的旧存档可正常加载 |
| 打包 | 执行 ad-hoc 签名，设置自定义 bundle id | 双击 `.app` 可正常启动 |
| 插件（可选） | BepInEx 6 `Unity.Mono` 的 `be` 分支 + UnityDoorstop **4.6**；原生 `arm64` 所需的两处预加载器修正和 detour 修正连同源码已在 `lom-dx2metal` 仓库的 `arm64-mods/` 目录公开；搭配 `zhaohuo-system` 最新 release | BepInEx 日志 0 异常；游戏内按 `F8` 可打开面板 |

## 顺序与门槛

执行顺序：**引擎 → 着色器 → 标题可见 → Steam → Wwise → 新游戏/存读一轮 → 打包 → 插件**。

- 在拿到当前步骤“做对了的信号”之前，严禁进入下一步。
- 连续两次实验没有拿到新证据时，立即停下来向我汇报，不要自行更换方向。

## 已知的坑（直接采信，不要重新验证）

- 着色器的三条根因（反射写在 blob legacy tail 而非 `ParsedForm`；同名 CB 成员跨参数表合并；内联采样器保留为 `constexpr sampler`）已由 `lom-dx2metal` 工具处理完毕，不要去手动“修”工具的输出结果。
- 后处理 shader（`UberPost`、`Bloom` 等）和标题黑屏无关，因为标题相机本身关闭了后处理。
- 如果 `PATH` 前面装有 GNU coreutils，直接运行 `cp -c`、`sed -i ''` 会失败，必须显式调用 `/bin/cp`、`/usr/bin/sed`。
