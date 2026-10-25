#!/usr/bin/env python3
"""从 UnityPy 内置 TPK 导出 Unity 2020.3.49f1 的 BuildSettings(ClassID 141) 类型树（只需运行一次）。

用法：<带 UnityPy 的 python> export_buildsettings_typetree.py > ../typetree_buildsettings_2020_3.json
格式与 export_shader_typetree.py 相同。
"""
import json
import sys

from UnityPy.helpers.Tpk import get_typetree_node
from UnityPy.helpers.UnityVersion import UnityVersion

root = get_typetree_node(141, UnityVersion.from_str("2020.3.49f1"))
nodes = []


def walk(node):
    nodes.append({"level": node.m_Level, "type": node.m_Type, "name": node.m_Name,
                  "byte_size": node.m_ByteSize, "meta_flag": node.m_MetaFlag or 0})
    for child in node.m_Children:
        walk(child)


walk(root)
json.dump({"unity": "2020.3.49f1", "class_id": 141, "nodes": nodes}, sys.stdout, indent=0)
sys.stdout.write("\n")
