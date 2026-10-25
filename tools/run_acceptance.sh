#!/bin/zsh
# 验收 6（运行）：克隆 MortalARM64.app → MortalARM64-dx2metal.app，把 Data 里含 Shader 的文件换成 dx2metal 输出，
# 按 ports/arm64/play.sh 的方式带日志启动 40 秒，截图，只 kill 自己启动的那个 pid，统计 Player.log。
# 用法：tools/run_acceptance.sh <dx2metal 输出目录> <本次 runs 目录>
# 写入只落在 build/MortalARM64-dx2metal.app 与 <runs 目录>；存档/偏好隔离到 <runs 目录>/home（不碰 playhome）。
set -eu
OUT="$1"; RUN="$2"
PORT=/Users/bob/lom-ws/ports/arm64
SRC="$PORT/build/MortalARM64.app"
APP="$PORT/build/MortalARM64-dx2metal.app"
mkdir -p "$RUN/home/Library/Application Support" "$RUN/home/Library/Preferences" "$RUN/tmp"

if pgrep -f "MortalARM64-dx2metal.app/Contents/MacOS" >/dev/null; then
  echo "MortalARM64-dx2metal.app 还在运行，先关掉它" >&2; exit 1
fi
rm -rf "$APP"
/bin/cp -Rc "$SRC" "$APP"                      # 系统 cp 的 APFS clone；PATH 里的 GNU cp 不认 -c
DATA="$APP/Contents/Resources/Data"
replaced=0
for f in $(cd "$OUT" && find . -type f ! -name 'dx2metal-audit.json' | sed 's|^\./||'); do
  rm -f "$DATA/$f"
  /bin/cp -c "$OUT/$f" "$DATA/$f"
  replaced=$((replaced+1))
done
echo "替换 $replaced 个资产文件"

echo 1859910 > "$RUN/steam_appid.txt"
export SteamAppId=1859910 SteamGameId=1859910
export CFFIXED_USER_HOME="$RUN/home"
export TMPDIR="$RUN/tmp/"
export STEAM_CLIENT_CONFIG_FILE="$HOME/Library/Application Support/Steam/config/config.vdf"
cd "$RUN"
"$APP/Contents/MacOS/MortalARM64" -logFile "$RUN/Player.log" -force-metal \
  -screen-fullscreen 0 -screen-width 1280 -screen-height 720 > "$RUN/console.log" 2>&1 &
PID=$!
echo "pid $PID" | tee "$RUN/pid.txt"
# 游戏 runInBackground=false，失焦会暂停：反复把自己的窗口置前。
for i in 1 2 3 4 5 6; do
  sleep 5
  kill -0 $PID 2>/dev/null || break
  osascript -e "tell application \"System Events\" to set frontmost of (first process whose unix id is $PID) to true" >/dev/null 2>&1 || true
done
sleep 8
if kill -0 $PID 2>/dev/null; then
  osascript -e "tell application \"System Events\" to set frontmost of (first process whose unix id is $PID) to true" >/dev/null 2>&1 || true
  sleep 1
  WID=$("$PORT/investigation/assets/winid" $PID | head -1 || true)
  [[ -n "$WID" ]] && screencapture -x -o -l "$WID" "$RUN/screenshot-window.png" || true
  screencapture -x "$RUN/screenshot.png" || true
  sleep 1
  kill $PID 2>/dev/null || true                 # 只 kill 自己启动的 pid
  sleep 3
  kill -9 $PID 2>/dev/null || true
  ALIVE=1
else
  ALIVE=0
fi
wait $PID 2>/dev/null || true
LOG="$RUN/Player.log"
c() { grep -c "$1" "$LOG" 2>/dev/null || true; }
cat > "$RUN/result.json" <<JSON
{"alive_at_40s": $ALIVE, "platform14_not_available": $(c 'platform 14 is not available'),
 "unsupported": $(c 'not supported on this GPU'), "exception": $(c 'Exception'),
 "metal_init": $(c 'Metal'), "replaced_files": $replaced}
JSON
cat "$RUN/result.json"
