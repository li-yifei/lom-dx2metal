#!/usr/bin/env python3
"""用 Python 版 assemble.patch_settings（只改 BuildSettings.m_GraphicsAPIs = [16]）生成 globalgamemanagers 的 oracle。

用法：gen_gfx_oracle.py <游戏 Mortal_Data 目录> <输出目录>
输出：<输出目录>/globalgamemanagers 与 patch_settings 返回的信息 settings.json。游戏目录只读。
"""
import json
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, "/Users/bob/lom-ws/tools/lom-arm64-converter")
from lom_arm64.assemble import patch_settings  # noqa: E402

game, out = Path(sys.argv[1]), Path(sys.argv[2])
out.mkdir(parents=True, exist_ok=True)
dst = out / "globalgamemanagers"
dst.unlink(missing_ok=True)
subprocess.run(["/bin/cp", "-c", str(game / "globalgamemanagers"), str(dst)], check=True)
info = patch_settings(out)
(out / "settings.json").write_text(json.dumps(info, ensure_ascii=False, indent=1) + "\n")
print(json.dumps(info, ensure_ascii=False))
