"""画 resources/icon.png（1024x1024）：左边一个线框立方体（D3D11），箭头，右边一个切成三角面的菱形（Metal）。纯图形，不含字体字形。

用法（需要 Pillow）：python3 scripts/make_icon.py
"""
from pathlib import Path

from PIL import Image, ImageDraw

S = 1024
img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
d = ImageDraw.Draw(img)

# macOS 图标网格：1024 里居中的 824 圆角方块，纵向深蓝渐变。
m, r = 100, 185
for i in range(824):
    t = i / 823
    d.line([(m, m + i), (S - m, m + i)], fill=(int(24 - 10 * t), int(46 - 20 * t), int(92 - 40 * t), 255))
mask = Image.new("L", (S, S), 0)
ImageDraw.Draw(mask).rounded_rectangle([m, m, S - m, S - m], r, fill=255)
img.putalpha(mask)
d = ImageDraw.Draw(img)

gold, pale = (236, 200, 120, 255), (245, 240, 228, 255)
W = 26

# 左：线框立方体（D3D11）。
cx, cy, a = 285, 520, 120
front = [(cx - a, cy - a + 40), (cx + a - 40, cy - a + 40), (cx + a - 40, cy + a + 40), (cx - a, cy + a + 40)]
back = [(x + 80, y - 80) for x, y in front]
for poly in (back, front):
    d.line(poly + [poly[0]], fill=gold, width=W, joint="curve")
for p, q in zip(front, back):
    d.line([p, q], fill=gold, width=W)

# 中：箭头。
ay = 520
d.line([(465, ay), (555, ay)], fill=pale, width=34)
d.polygon([(545, ay - 55), (625, ay), (545, ay + 55)], fill=pale)

# 右：菱形，分成四块三角面（Metal）。
mx, my, rx, ry = 790, 520, 120, 170
top, right, bottom, left, mid = (mx, my - ry), (mx + rx, my), (mx, my + ry), (mx - rx, my), (mx, my)
d.polygon([top, right, mid], fill=(250, 244, 230, 255))
d.polygon([right, bottom, mid], fill=(190, 205, 235, 255))
d.polygon([bottom, left, mid], fill=(120, 150, 215, 255))
d.polygon([left, top, mid], fill=(215, 225, 245, 255))
d.line([top, right, bottom, left, top], fill=gold, width=W // 2, joint="curve")

out = Path(__file__).resolve().parents[1] / "resources/icon.png"
img.save(out)
print(out)
