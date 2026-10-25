# 自己动手：把《活侠传》Windows 版拼成原生 Apple Silicon 版

> 这份指南只供**你自己**动手使用：所有组件都从各自的官方渠道取到你自己的电脑上，拼装出来的 .app 也只留在你自己的电脑里。
> `lom-dx2metal` 仓库只负责转换着色器那一步；至于其余组件，本文会一步步告诉你要去哪里拿、放到哪个位置、怎么验证是否成功，但不会替你下载，也不会替你打包。
> 如果遇到看不懂的步骤，可以把整段内容直接交给 Claude Code / Codex 之类的编码 agent，配合 [AGENT-BRIEF.md](AGENT-BRIEF.md) 一起使用。

## 0. 你需要准备的

| 东西 | 说明 |
|---|---|
| Apple Silicon Mac，macOS 12 以上 | 实测 macOS 26/27 |
| Xcode 命令行工具 | `xcode-select --install`，要用到 `codesign`、`lipo`、`pkgutil`、clang |
| 你自己买的 Windows 版《活侠传》 | Steam AppID 1859910，游戏目录里有 `Mortal_Data/` 和 `Mortal.exe`。可以来自 Windows 机器、外接硬盘、CrossOver/Whisky 容器，或在 Mac 上用 Steam 的"强制使用 Windows 版本"下载 |
| Mac 版 Steam 客户端，登录拥有本游戏的账号 | 游戏启动时要通过 Steam 做所有权校验 |
| 磁盘约 5 GB | Unity 安装包 0.5 GB、解包 1.5 GB、输出 .app 约等于游戏体积 |
| 本仓库构建出的 `dx2metal`（命令行或 GUI） | 见根目录 README |

为了方便说明，下文把你的游戏目录记作 `$GAME`，把正在拼装的应用记作 `$APP`（例如 `~/Applications/LegendOfMortal.app`）。整个过程中 **`$GAME` 全程只读**，所有改动都发生在 `$APP` 里。

## 1. 拼出来的 .app 长什么样

```
LegendOfMortal.app/Contents/
  Info.plist                      自己写，见 §2.4
  MacOS/LegendOfMortal            启动脚本（设 SteamAppId 后 exec 真正的 Player）
  MacOS/LegendOfMortal-player     Unity 官方 ARM64 Player（原名 UnityPlayer）
  Frameworks/                     Unity 官方：UnityPlayer.dylib、libmonobdwgc-2.0.dylib、libMonoPosixHelper.dylib
  MonoBleedingEdge/               Unity 官方：Mono 运行时配置
  Plugins/libsteam_api.dylib      §4
  Plugins/AkSoundEngine.bundle/   §5
  Resources/MainMenu.nib          Unity 官方
  Resources/Data/                 = 你的 Mortal_Data（Managed、Resources、StreamingAssets、所有序列化文件）
  Resources/Data/Resources/unity default resources   从 Unity 官方 Player 复制，不是游戏的那份
```

用一句话概括它的结构，其实就是：**Unity 官方 Player 的壳 + 你自己的游戏数据 + 两个换成 Mac 版的原生库 + 着色器转换**。

## 2. Unity Player（引擎）

### 2.1 下载

引擎版本必须和游戏完全一致，也就是 **2020.3.49f1**（changeset `18249dd5551b`）。Unity 官方 CDN 上的"Mac Mono Build Support"安装包里就已经包含了我们需要的 ARM64 Player：

```
https://download.unity3d.com/download_unity/18249dd5551b/MacEditorTargetInstaller/UnitySetup-Mac-Mono-Support-for-Editor-2020.3.49f1.pkg
大小 512890883 字节
SHA-256 3399d32bb69ddc308d809fd271dcb8332ce66c9c5e1503ec5d531aec8e55dfc3
```

下载完成后，先核对一下文件的校验值：`shasum -a 256 UnitySetup-Mac-Mono-Support-for-Editor-2020.3.49f1.pkg`。

### 2.2 解包，不要"安装"

```bash
/usr/sbin/pkgutil --expand-full UnitySetup-Mac-Mono-Support-for-Editor-2020.3.49f1.pkg unity-mac-mono
```

我们要用的东西都在解包出来的 `unity-mac-mono/TargetSupport.pkg.tmp/Payload/` 目录下（下文记作 `$PAYLOAD`）：

- `Variations/macos_arm64_nondevelopment_mono/UnityPlayer.app`：ARM64 Player 模板（这里要选 `nondevelopment`，因为 `development` 变体在运行时会弹出开发者覆盖层）
- `MonoBleedingEdge/`
- `Source/Player/MacPlayer/MacPlayerEntryPoint/Info.plist` 与 `Resources/MainMenu.nib`

你可以顺手用 `lipo -archs` 检查一下，确认 `UnityPlayer.app/Contents/MacOS/UnityPlayer` 与 `Frameworks/*.dylib` 的架构都只有 `arm64`。

### 2.3 拼装

1. 把 `$PAYLOAD/Variations/macos_arm64_nondevelopment_mono/UnityPlayer.app` 整个复制为 `$APP`（在 APFS 上用 `/bin/cp -Rc` 可以秒级克隆）。
2. 把 `$APP/Contents/MacOS/UnityPlayer` 改名为 `LegendOfMortal-player`。
3. 编写启动脚本 `$APP/Contents/MacOS/LegendOfMortal`（记得执行 `chmod 755` 赋予可执行权限）：

   ```bash
   #!/bin/bash
   DIR="$(cd "$(dirname "$0")" && pwd)"
   export SteamAppId=1859910
   export SteamGameId=1859910
   exec "$DIR/LegendOfMortal-player" "$@"
   ```

   只要有了这两个环境变量，`SteamAPI_Init` 就会把进程当作从 Steam 启动的游戏，不需要再往工作目录里放 `steam_appid.txt`。
4. 新建 `$APP/Contents/Resources/Data/` 目录，从 `$GAME/Mortal_Data/` 里把这些内容复制进去：`Managed/`、`Resources/`、`StreamingAssets/` 三个目录，以及根目录下的全部文件（`globalgamemanagers`、`*.assets`、`level*`、`*.resS`……）。**不要复制** `Plugins/`（里面是 Windows 的 dll）和其它目录。
5. 把 `$APP/Contents/Resources/unity default resources`（Player 自带）复制到 `$APP/Contents/Resources/Data/Resources/unity default resources`，覆盖掉游戏原本自带的那份。Mac Player 内置的 GUI/文字着色器都存放在这里。
6. 把 `$PAYLOAD/MonoBleedingEdge` 复制到 `$APP/Contents/MonoBleedingEdge`；再把 `$PAYLOAD/Source/Player/MacPlayer/MacPlayerEntryPoint/Resources/MainMenu.nib` 复制到 `$APP/Contents/Resources/MainMenu.nib`。

### 2.4 Info.plist

拿 `$PAYLOAD/Source/Player/MacPlayer/MacPlayerEntryPoint/Info.plist` 作为底板，修改下面这些键（其余保持原样即可）：

| 键 | 值 |
|---|---|
| `CFBundleExecutable` | `LegendOfMortal`（启动脚本名） |
| `CFBundleIdentifier` | 自己起，例如 `local.lom.native`。**存档目录由它决定**，见 §6；别和 Steam 版或别的实验副本重名 |
| `CFBundleName` / `CFBundleDisplayName` | 随意 |
| `CFBundleShortVersionString` | 游戏版本号（`globalgamemanagers` 里 PlayerSettings 的 `bundleVersion`） |
| `UnityBuildNumber` | `18249dd5551b` |
| `LSMinimumSystemVersion` | `12.0` |
| `NSHighResolutionCapable` | true |

删掉 `CFBundleIconFile`（因为没有图标文件），另外再新建一个内容为 `APPL????` 的 `Contents/PkgInfo` 文件。

### 2.5 把图形 API 切到 Metal

原版 `Mortal_Data/globalgamemanagers` 里的 `BuildSettings.m_GraphicsAPIs` 默认是 Windows 的 D3D11 列表，需要改成只含 Metal（枚举值 16）。
好在 `dx2metal convert` 处理 `globalgamemanagers` 时会顺手帮你改掉（如果不想要这个改动，可以加上 `--keep-graphics-api`），所以只要把 §3 转换出来的输出文件覆盖进去就行，不用手动去改。
用 `dx2metal inspect globalgamemanagers` 可以查看当前的图形 API 列表。我们所有成功运行的副本都做了这一步。

### 2.6 验证

```bash
"$APP/Contents/MacOS/LegendOfMortal" -logFile /tmp/lom.log -screen-fullscreen 0 -screen-width 1280 -screen-height 720
```

这时候还没替换原生库、也没转换着色器，游戏启动后会在 Steam 或 Wwise 初始化的地方报错退出，这是符合预期的。我们打开 `/tmp/lom.log` 看开头有没有下面几项：

- `Initialize engine version: 2020.3.49f1`
- `Metal` 设备初始化成功的日志行（`Initializing Metal device caps`）
- 进程架构：对该 pid 执行 `sysctl -n sysctl.proc_translated` 结果应为 0；执行 `ps -o arch -p <pid>` 应显示 `arm64`

## 3. 着色器：dx2metal

```bash
dx2metal convert --in "$GAME/Mortal_Data" --out /tmp/Data.metal
```

你也可以直接用 GUI 拖入 `Mortal_Data` 进行转换。转换完成后，把输出目录里的文件按相对路径覆盖到 `$APP/Contents/Resources/Data/`（输出目录里只有改过的资产文件和一份 `dx2metal-audit.json`，其中 audit 文件可以不复制）。

验证方法：工具报告的转换数量应为 ≥ 2546 / 2550；覆盖之后每次启动游戏，日志里 `platform 14 is not available` 出现 0 次、`not supported on this GPU` 也出现 0 次。剩下 4 个未转换的是 VR/XR 专用程序，游戏本身不会用到。

## 4. Steam

游戏使用的是 **Steamworks.NET 20.1.0**（对应 Steamworks SDK 1.53a）。这里需要替换两层文件：

### 4.1 原生库 `libsteam_api.dylib`

Steamworks.NET 仓库（MIT 许可）里随附了 Valve 的 Mac 版原生库，直接从同版本 tag 下获取这个文件即可：

```
https://raw.githubusercontent.com/rlabrecque/Steamworks.NET/20.1.0/com.rlabrecque.steamworks.net/Plugins/steam_api.bundle/Contents/MacOS/libsteam_api.dylib
大小 609584 字节（universal）
SHA-256 162b0c71e1e724582a884c95f2748cb20944a003e858c017dc6bdd10c18e1536
```

```bash
lipo -thin arm64 libsteam_api.dylib -output "$APP/Contents/Plugins/libsteam_api.dylib"
shasum -a 256 "$APP/Contents/Plugins/libsteam_api.dylib"   # 应为 d24f233a118f5aefdda4499b5d7ef14b28a5991e9ba778aeb2e6a8cd292718ab
codesign --force --sign - "$APP/Contents/Plugins/libsteam_api.dylib"
```

因为 Mono 的回退加载器会在 `Contents/Frameworks/MonoEmbedRuntime/osx/` 目录下按名字探测动态库，所以我们再放两个符号链接过去：

```bash
cd "$APP/Contents/Frameworks/MonoEmbedRuntime/osx"
ln -s ../../../Plugins/libsteam_api.dylib libsteam_api.dylib
ln -s ../../../Plugins/libsteam_api.dylib libsteam_api64.dylib
```

### 4.2 托管层 `com.rlabrecque.steamworks.net.dll`

游戏自带的这份 dll 是按 Windows 平台编译的（结构体布局、P/Invoke 库名都和 Mac 不同）。我们需要从 Steamworks.NET **20.1.0** 的源码重新编译一份，编译时定义符号 `STEAMWORKS_LIN_OSX` 和 `STEAMWORKS_X64`，用生成的输出替换掉 `$APP/Contents/Resources/Data/Managed/com.rlabrecque.steamworks.net.dll`，并删掉旁边的 `com.rlabrecque.steamworks.net.dll.config`（它会把库名映射到 Windows 的 dll）。

验证方法：保持 Steam 客户端处于登录状态，启动后在日志里确认能正常拿到 Steam ID，并且没有 `SteamAPI_Init` 失败、也没有 `DllNotFoundException: steam_api`。

## 5. Wwise（音频）

游戏的音频系统用的是 **Wwise 2021.1.14**（库版本 2021.1.14.8108）的 Unity 集成。由于 Audiokinetic 的许可协议不允许第三方再分发它的运行库，这一步只能由你自己动手准备：

1. 到 Audiokinetic 官网注册账号，下载并安装 **Wwise Launcher**。
2. 在 Launcher 里安装 **2021.1.x** 的 Wwise 及其 **Unity Integration**（小版本尽量选接近 .14 的，越接近，缺少的导出符号就越少）。
3. 从安装好的 Unity 集成目录里找到 macOS 的 Release 构建 `AkSoundEngine.bundle/Contents/MacOS/AkSoundEngine`（这是一个 universal 二进制文件）。

把它放进应用包里：

```bash
mkdir -p "$APP/Contents/Plugins/AkSoundEngine.bundle/Contents/MacOS"
lipo -thin arm64 AkSoundEngine -output "$APP/Contents/Plugins/AkSoundEngine.bundle/Contents/MacOS/AkSoundEngine"
# 给 bundle 一个最小 Info.plist：CFBundleExecutable=AkSoundEngine、CFBundlePackageType=BNDL、CFBundleName=AkSoundEngine
codesign --force --sign - "$APP/Contents/Plugins/AkSoundEngine.bundle"
cd "$APP/Contents/Frameworks/MonoEmbedRuntime/osx" && ln -s ../../../Plugins/AkSoundEngine.bundle/Contents/MacOS/AkSoundEngine libAkSoundEngine.dylib
```

**如果版本小号不同，库里会少几个导出函数。** 游戏的 `AK.Wwise.Unity.API.dll` 一共导入了 1188 个 `CSharp_*` 符号，以 Mac 版 2021.1.11 为例，它缺少其中的 13 个，都是 Windows 设备枚举、AVX 开关、系统音频对象上限和几个 `AkChannelConfig` 构造函数，游戏在 Mac 上运行时本来也不会走到它们。只有一处在初始化时真的会被调用：`AkWindowsSettings.PlatformAdvancedSettings.CopyTo` 会把 `MaxSystemAudioObjects` 转发给原生的 `set_uMaxSystemAudioObjects`，而 Mac 版的库里没有这个 setter，跑到这里就会抛出 `EntryPointNotFoundException`。处理办法是把这个方法的方法体改成直接 `ret`（它原本也只有 13 字节的 IL：`ldarg.1; ldarg.0; ldfld; callvirt; ret`）。你可以用 dnSpy/ILSpy 手动修改，或者让 agent 用 Mono.Cecil / dnfile 帮你改，改的同样是你自己副本里的 dll。

你可以用 `nm -gU AkSoundEngine | grep CSharp_` 拉出符号列表，和 dll 的导入表核对一下，确认缺少的只有这类符号；如果你换了别的版本、还缺了别的东西，启动日志里的 `MissingMethodException` / `EntryPointNotFoundException` 也会明确告诉你是哪一个。

验证方法：日志里出现 `Sound engine initialized`；在标题画面把鼠标悬停在按钮上有音效；并且能完整播放一段 BGM。游戏自带的 Windows 版 bank 文件（`StreamingAssets/Audio/GeneratedSoundBanks/Windows/`）可以直接使用。

## 6. 存档

Unity 在 macOS 上的 `persistentDataPath` 是 `~/Library/Application Support/<CFBundleIdentifier>/`，所以存档目录的位置由你在 §2.4 里自己起的 bundle id 决定。Windows 版的存档位于 `AppData/LocalLow/Obb Studio/Mortal/`（如果是跑在 Wine/CrossOver 容器里，则是 `drive_c/users/<用户>/AppData/LocalLow/Obb Studio/Mortal/`），把里面的文件直接复制到上面的 Mac 存档目录里，就能接着原来的进度玩。

在试拼装阶段，建议先用独立的 HOME 目录来跑（例如指定 `CFFIXED_USER_HOME=/某个临时目录`），等确认一切正常之后再指向真实存档；操作真实存档之前也请先做好备份。

## 7. 签名与首次启动

```bash
codesign --force --deep --sign - "$APP"
```

签名完成后就可以双击打开了。第一次启动时会被系统拦截，到"系统设置 → 隐私与安全性"里点允许即可。排查问题时建议总是带上 `-logFile` 参数启动，日志会输出到上面指定的路径。

## 8. 可选：BepInEx 与赵活系统

这一层用到的全是开源组件，可以直接说明白：

| 组件 | 版本 | 来源 |
|---|---|---|
| BepInEx | 6.0.0-be.788（Unity.Mono，macOS 包） | https://builds.bepinex.dev/projects/bepinex_be/788/ ，SHA-256 `f0bf5fea37f7bd3906b3514c40f2202b86eb4b7bfb845b0f311e9dd7acaead6b` |
| UnityDoorstop | 4.6.0 | https://github.com/NeighTools/UnityDoorstop/releases/tag/v4.6.0 的 `doorstop_macos_release_4.6.0.zip`，取 `universal/libdoorstop.dylib` |
| 本仓库 `arm64-mods/` | — | `BepInEx.Unity.Mono.Preloader.arm64-patched.dll`（替换 BepInEx 包里的原版 preloader，原版 SHA-256 `c4f667b8…01fb54`）、`LomArm64.AppleSiliconDetourFix.dll`（放进 `BepInEx/core/`）、`win32compat/` 两个 ObjC 替身源码 |
| 赵活系统 | 最新 release | https://github.com/Toufff/zhaohuo-system |

配置时的几个要点：

- 把 BepInEx 放在**游戏根目录**（可以放在 `$APP` 旁边或任意目录），启动脚本在检测到有 `BepInEx/doorstop/libdoorstop.dylib` 时，用 `DYLD_INSERT_LIBRARIES` 注入 doorstop，然后再 exec 真正的 Player。
- 在原生 arm64 上，MonoMod 直接写内存做 detour 会触发段错误，`AppleSiliconDetourFix` 会把写入操作改走 doorstop 4.6 导出的 `doorstop_jit_memcpy`（这个修法来自 BepInEx/BepInEx#1402）；补丁版 preloader 会在 `SetPlatform()` 之后调用它，并跳过 `XTermFix`。
- 赵活系统会调用 Windows 的 `gdi32`（用于注册字体）和 `user32`（用于键盘与前台窗口）。本仓库的 `win32compat/gdi32_compat.m` 和 `user32_compat.m` 用 CoreText / AppKit 实现了这几个入口，你需要把它们编译成 `libgdi32.dylib`、`libuser32.dylib` 放到 `Contents/Plugins/`，并在 Mono 的 `config` 里加上 `<dllmap dll="gdi32" target="libgdi32.dylib" os="!windows"/>`。
- 装完 BepInEx 后，记得重新执行一次 `codesign --force --deep --sign -`。

验证方法：BepInEx 日志里插件加载 0 异常；进入游戏后按 F8 能正常打开赵活系统面板。

## 9. 验收清单

| 步骤 | 看哪里 | 通过的样子 |
|---|---|---|
| 引擎 | Player.log 开头 | `2020.3.49f1`、Metal 初始化、进程 arm64 且 `proc_translated=0` |
| 着色器 | dx2metal 报告 + Player.log | ≥2546/2550；`platform 14 is not available` 0 次；标题画面完整、无品红 |
| Steam | Player.log | 拿到 Steam ID，无 `SteamAPI_Init` 失败 |
| Wwise | Player.log + 耳朵 | `Sound engine initialized`；按钮音效；BGM 完整播放 |
| 存档 | 游戏内 | 新建存档可读回；迁移的旧存档可加载 |
| 整体 | 游戏内 | 新游戏 → 剧情 → 一场战斗，无黑屏、发暗、色带，Player.log 无 Exception |
| 插件（可选） | BepInEx 日志 | 0 异常；F8 面板可开 |

## 10. 别做的事

- 别把拼好的 `$APP`、转换后的资产、Unity Player、Steam 或 Wwise 的库发给任何人。每个人自己动手走一遍就行，熟练下来也就十几分钟的事。
- 别动 `$GAME`，别动 Steam 目录，也别在没备份的情况下直接指向真实存档。
- Wwise 的库只能从你自己的 Audiokinetic 账号获取，别到处去找什么"别人传的 AkSoundEngine"。
