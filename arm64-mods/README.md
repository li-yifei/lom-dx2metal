# BepInEx 6 在原生 ARM64（Apple Silicon）上的修正

`convert.sh install-jp-mod` 用到的预编译文件。来源是 `ports/arm64/bepinex_arm64`（BepInEx 移植会话，2026-10-23），
在 `ports/arm64/build/MortalARM64-mods` 中实测可用（BepInEx 6.0.0-be.788 + 赵活系统 + 日语化 MOD）。

| 文件 | SHA-256 | 说明 |
| --- | --- | --- |
| `LomArm64.AppleSiliconDetourFix.dll` | `de5913e1…2b1c4` | 本项目自写（源码 `src/AppleSiliconDetourFix.cs`），把 MonoMod 写 detour 的操作改走 UnityDoorstop 4.6 的 `doorstop_jit_memcpy`；移植自 BepInEx/BepInEx#1402 |
| `BepInEx.Unity.Mono.Preloader.arm64-patched.dll` | `ea85ab8e…3e18884` | BepInEx 6.0.0-be.788+5b766a3 macOS 包里的原版（`c4f667b8…01fb54`）经 `src/PreloaderPatcher.cs`（Mono.Cecil）打两处 IL 补丁：`UnityPreloaderRunner.PreloaderPreMain` 在 `SetPlatform()` 后调用 `AppleSiliconDetourFix.Apply()`；原生 arm64 上跳过 `XTermFix.Apply` |
| `win32compat/gdi32_compat.m`、`user32_compat.m` | — | 本项目自写：`AddFontResourceEx` 等 gdi32 字体注册（CoreText）与 user32 键盘/前台窗口查询的 macOS 替身，安装时用 Xcode 命令行工具编译 |

转换工具安装时会先校验下载的 BepInEx 包里原版 preloader 的 SHA-256，与上表一致才替换为补丁版。

## 许可

- BepInEx 采用 LGPL-2.1。`BepInEx.Unity.Mono.Preloader.arm64-patched.dll` 是其修改版，按 LGPL-2.1 再分发：
  原始源码见 https://github.com/BepInEx/BepInEx （commit `5b766a3`），修改方式完整记录在 `src/PreloaderPatcher.cs`，
  任何人都可以用它从原版重新生成本文件，或换用自己修改的版本。
- `AppleSiliconDetourFix`、`PreloaderPatcher`、`win32compat` 为本项目代码，与本工具同样采用 MIT。
- UnityDoorstop 4.6.0（LGPL-2.1）、BepInEx 其余程序集不在仓库中，运行时从官方地址下载并校验 SHA-256。
- 日语化 MOD（LOM_JP_Mod）为第三方闭源作品，不随本工具分发；玩家自行从 https://dlaqe2334.github.io/LOM-JPMOD/ 下载。
