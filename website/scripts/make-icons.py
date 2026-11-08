"""生成 favicon 变体：SVG（矢量）、180×180 apple-touch-icon、32×32 PNG。

权威图形只有一份：仓库里的 internal/web/ui2/assets/aether-cockpit.svg。
这里复制一份到 assets/img/，而不是重新画——品牌检查脚本会校验两者一致。
"""

import io
import shutil
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parent.parent
REPO = ROOT.parent
SRC_SVG = REPO / "internal/web/ui2/assets/aether-cockpit.svg"
OUT = ROOT / "assets/img"

assert SRC_SVG.exists(), f"权威图标缺失: {SRC_SVG}"

# 1) 矢量 favicon：直接复制权威 SVG
svg_dst = OUT / "favicon.svg"
shutil.copyfile(SRC_SVG, svg_dst)
print(f"favicon.svg      <- {SRC_SVG.relative_to(REPO)}  ({svg_dst.stat().st_size} B)")

# 2) apple-touch-icon：iOS 会自己把图标裁成圆角，所以这里要**整幅铺满**
#    （留白会让自带的圆角底板浮在方块中央，出现一圈多余的边）。
#    同时必须去掉 alpha，否则 iOS 显示为黑底。
appicon = Image.open(OUT / "appicon.png").convert("RGBA")

side = 180
plate = (248, 249, 250, 255)  # 与图标底板同色

canvas = Image.new("RGBA", (side, side), plate)
mark = appicon.resize((side, side), Image.LANCZOS)
canvas.alpha_composite(mark)
canvas = canvas.convert("RGB")
apple = OUT / "apple-touch-icon.png"
canvas.save(apple, "PNG", optimize=True)
print(f"apple-touch-icon <- {apple.stat().st_size} B  ({side}x{side}, 满幅/不透明)")

# 3) 32x32 PNG 兜底（老浏览器）
small = appicon.resize((32, 32), Image.LANCZOS)
p32 = OUT / "favicon-32.png"
small.save(p32, "PNG", optimize=True)
print(f"favicon-32       <- {p32.stat().st_size} B")

# 4) 与权威 SVG 的一致性校验
a = io.open(SRC_SVG, encoding="utf-8").read().strip()
b = io.open(svg_dst, encoding="utf-8").read().strip()
assert a == b, "favicon.svg 与权威图标不一致"
print("\n一致性：favicon.svg == aether-cockpit.svg ✓")
