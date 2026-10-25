#!/usr/bin/env python3
"""导出 Unity 类型树公共字符串表（offset→字符串），用于解析 bundle 内嵌类型树（只需运行一次）。

用法：<带 UnityPy 的 python> export_common_strings.py > ../common_strings.json
"""
import json
import sys

from UnityPy.helpers.Tpk import get_common_strings
from UnityPy.helpers.UnityVersion import UnityVersion

strings = get_common_strings(UnityVersion.from_str("2020.3.49f1"))
json.dump({str(k): v for k, v in sorted(strings.items())}, sys.stdout, indent=0)
sys.stdout.write("\n")
