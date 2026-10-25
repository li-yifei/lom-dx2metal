#!/usr/bin/env python3
"""用当前 Python 版转换器（lom-arm64-converter，只读引用）生成验收用 oracle。

用法：gen_oracle.py <游戏 Mortal_Data 目录> <oracle 输出目录>
输出目录下：in/（原始资产文件的 APFS 克隆）、out/（Python 版转换后的资产）、work/（每个变体的 .metal）、audit.json。
游戏目录只读；所有写入落在输出目录。
"""
import shutil
import subprocess
import sys
from pathlib import Path

TOOL = Path("/Users/bob/lom-ws/tools/lom-arm64-converter")
sys.path.insert(0, str(TOOL))
from lom_arm64.shaders import bulk, hlslcc_bridge  # noqa: E402

game, out = Path(sys.argv[1]), Path(sys.argv[2])
src_files = bulk.shader_files(game)
for name in ("in", "out"):
    for f in src_files:
        dst = out / name / f.relative_to(game)
        dst.parent.mkdir(parents=True, exist_ok=True)
        if not dst.exists():
            subprocess.run(["/bin/cp", "-c", str(f), str(dst)], check=True)
hlslcc_bridge.set_hlslcc(TOOL / "build/bin/hlslcc-cli")
work = out / "work"
shutil.rmtree(work, ignore_errors=True)
data = out / "out"
bulk.convert_data_dir(data, work, TOOL / "build/bin/reflect_batch", out / "audit.json",
                      files=[data / f.relative_to(game) for f in src_files])
