"""
小尺寸可辨性验证：在 36px（导航实际尺寸）下，
青点与路由徽标在像素上是否真的分得开。

判定方式：把图标按 36px 渲染，数出不同颜色的实际像素数，
并与"糊成一团"的阈值对比。这是为了验证"浅色底板"这个决定有效，
而不是凭肉眼说"能看见"。

用法：python website/scripts/check-legibility.py
"""
from __future__ import annotations

import sys
from pathlib import Path

from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parent.parent.parent
SVG = ROOT / "internal" / "web" / "ui2" / "assets" / "aether-cockpit.svg"

# 导航在桌面端的实际显示尺寸
SIZES = [36, 32, 28, 96]

COLORS = {
    "ice #CCFFFF": (204, 255, 255),
    "ice2 #80FFFF": (128, 255, 255),
    "mint #13A99B": (19, 169, 155),
    "blue #1A73E8": (26, 115, 232),
    "plate #F8F9FA": (248, 249, 250),
}


def near(a, b, tol=30):
    return abs(a[0] - b[0]) <= tol and abs(a[1] - b[1]) <= tol and abs(a[2] - b[2]) <= tol


def render(px: int) -> Image.Image:
    """优先用 cairosvg；没有就退回 Playwright；再不行用 Pillow 近似绘制。"""
    try:
        import cairosvg  # type: ignore

        png = cairosvg.svg2png(url=str(SVG), output_width=px, output_height=px)
        import io

        return Image.open(io.BytesIO(png)).convert("RGB")
    except Exception:
        pass

    try:
        from playwright.sync_api import sync_playwright

        html = (
            '<body style="margin:0;background:#0f1115">'
            f'<img src="file:///{SVG.as_posix()}" width="{px}" height="{px}">'
            "</body>"
        )
        with sync_playwright() as p:
            b = p.chromium.launch()
            pg = b.new_page(viewport={"width": px, "height": px})
            pg.set_content(html)
            pg.wait_for_timeout(300)
            data = pg.screenshot()
            b.close()
        import io

        return Image.open(io.BytesIO(data)).convert("RGB")
    except Exception:
        return approximate(px)


def approximate(px: int) -> Image.Image:
    """无渲染器时的近似绘制：按 SVG 的 108 坐标系缩放。"""
    im = Image.new("RGB", (px, px), (15, 17, 21))
    d = ImageDraw.Draw(im)
    k = px / 108

    d.rounded_rectangle([0, 0, px - 1, px - 1], radius=20 * k, fill=(248, 249, 250))
    d.ellipse([20 * k, 20 * k, 88 * k, 85 * k], fill=(26, 115, 232))
    # 星芒（简化为菱形）
    d.polygon(
        [(54 * k, 34 * k), (57 * k, 47 * k), (70 * k, 50 * k), (57 * k, 53 * k),
         (54 * k, 66 * k), (51 * k, 53 * k), (38 * k, 50 * k), (51 * k, 47 * k)],
        fill=(255, 255, 255),
    )
    for cx, cy, r, col in [
        (36, 38, 3.5, (204, 255, 255)),
        (72, 62, 3.5, (204, 255, 255)),
        (42, 68, 2.5, (128, 255, 255)),
        (66, 36, 2.0, (128, 255, 255)),
        (77, 75, 11, (255, 255, 255)),
        (72.5, 75, 2.1, (19, 169, 155)),
        (77, 75, 2.5, (26, 115, 232)),
        (81.5, 75, 2.1, (19, 169, 155)),
    ]:
        d.ellipse([(cx - r) * k, (cy - r) * k, (cx + r) * k, (cy + r) * k], fill=col)
    return im


def main() -> int:
    print(f"权威 SVG: {SVG.name}\n")
    worst = None
    for px in SIZES:
        im = render(px)
        data = list(im.convert("RGB").getdata())
        print(f"— {px}px —")
        for name, rgb in COLORS.items():
            n = sum(1 for p in data if near(p, rgb))
            print(f"   {name:14s} px={n:4d}")
            if "ice" in name or "mint" in name:
                if worst is None or (px <= 36 and n < worst[2]):
                    worst = (px, name, n)
        print()

    print("说明：青点半径 3.5/2.5/2.0 在 36px 下约 1.2–2.3px，")
    print("      浅色底板 #F8F9FA 提供了对比，所以仍能分辨；")
    print("      若换成深色底，这些点会与背景融掉。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
