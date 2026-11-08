"""
静态校验（不依赖浏览器）：
把网站里内联的图标几何与项目权威 SVG 逐条比对，
确认 path / circle 的坐标与颜色没有被改动。

这是防止"图标又画错了"的回归闸口；比截图更稳定，也不受浏览器环境影响。

用法：python website/scripts/check-brand-static.py
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent.parent

SRC = ROOT / "internal" / "web" / "ui2" / "assets" / "aether-cockpit.svg"
SITE = ROOT / "website" / "index.html"
CSS = ROOT / "website" / "assets" / "css" / "site.css"

# 权威 SVG 里的几何（顺序即构成顺序）
PATHS = {
    "bubble": "M54,20 C35.22,20 20,33.43 20,50 C20,59.39 24.87,67.76 32.22,73.19 L28,88 L43.55,83.33 C46.86,84.45 50.36,85 54,85 C72.78,85 88,71.57 88,55 C88,38.43 72.78,20 54,20 Z",
    "star": "M54,34 L57,47 L70,50 L57,53 L54,66 L51,53 L38,50 L51,47 Z",
}
DOTS = [
    ("#CCFFFF", "36", "38", "3.5"),
    ("#CCFFFF", "72", "62", "3.5"),
    ("#80FFFF", "42", "68", "2.5"),
    ("#80FFFF", "66", "36", "2"),
]
# tokens.css 的色值必须出现在站点里
TOKENS = {
    "#1a73e8": "强调色",
    "#13a99b": "薄荷（徽标描边）",
    "#ccffff": "冰蓝青点",
    "#80ffff": "冰蓝青点2",
    "#0f1115": "暗色底",
    "#f7f8fa": "浅色底",
    "#f8f9fa": "图标底板",
}


def norm(s: str) -> str:
    return re.sub(r"\s+", " ", s).strip()


def circle(cx: str, cy: str, r: str) -> re.Pattern[str]:
    return re.compile(
        rf"<circle[^>]*\bcx=\"{cx}\"[^>]*\bcy=\"{cy}\"[^>]*\br=\"{r}\""
        rf"|<circle[^>]*\bcx=\"{cx}\"[^>]*\br=\"{r}\"[^>]*\bcy=\"{cy}\""
    )


def main() -> int:
    for p, label in ((SRC, "权威 SVG"), (SITE, "站点"), (CSS, "样式")):
        if not p.exists():
            print(f"找不到{label}: {p}")
            return 1

    svg = norm(SRC.read_text(encoding="utf-8"))
    site_raw = SITE.read_text(encoding="utf-8")
    site = norm(site_raw)
    css = CSS.read_text(encoding="utf-8").lower()

    fails: list[str] = []

    # 1) 气泡与星芒 path 逐字一致
    for key, d in PATHS.items():
        in_src = norm(d) in svg
        in_site = norm(d) in site
        print(f"{'OK  ' if in_src and in_site else 'FAIL'}  path/{key:6s}  源={in_src} 站点={in_site}")
        if not (in_src and in_site):
            fails.append(f"path/{key}")

    # 2) 四个青点的坐标与半径
    for color, cx, cy, r in DOTS:
        found = bool(circle(cx, cy, r).search(site_raw))
        print(f"{'OK  ' if found else 'FAIL'}  dot {color} ({cx},{cy}) r={r}")
        if not found:
            fails.append(f"dot/{cx},{cy}")

    # 3) 路由徽标：圆盘 r=11、连线 72.5→81.5、三节点
    route_checks = {
        "plate r=11": bool(re.search(r'class="bm-plate"[^>]*r="11"', site_raw)),
        "link path": "M72.5 75 H81.5" in site_raw,
        "node 72.5": bool(re.search(r'class="bm-node-a"[^>]*cx="72.5"', site_raw)),
        "node 77": bool(re.search(r'class="bm-node-b"[^>]*cx="77"', site_raw)),
        "node 81.5": bool(re.search(r'class="bm-node-a"[^>]*cx="81\.5"', site_raw)),
    }
    for name, ok in route_checks.items():
        print(f"{'OK  ' if ok else 'FAIL'}  route/{name}")
        if not ok:
            fails.append(f"route/{name}")

    # 4) 底板必须存在（青点与路由标可辨的前提）
    bg_count = site_raw.count('class="bm-bg"')
    has_bg_css = ".bm-bg" in css and "#f8f9fa" in css
    ok = bg_count >= 2 and has_bg_css
    print(f"{'OK  ' if ok else 'FAIL'}  plate-bg 出现={bg_count} 样式={has_bg_css}")
    if not ok:
        fails.append("plate-bg")

    # 5) 导航与页脚必须用完整版（含青点与路由标），不得退回简化版
    simple_left = "bm-simple" in site_raw
    full_uses = site_raw.count('href="#bm-full"')
    ok = (not simple_left) and full_uses >= 2
    print(f"{'OK  ' if ok else 'FAIL'}  full-mark 使用={full_uses} 简化版残留={simple_left}")
    if not ok:
        fails.append("full-mark")

    # 6) 配色令牌
    for hexv, label in TOKENS.items():
        ok = hexv in css or hexv in site_raw.lower()
        print(f"{'OK  ' if ok else 'FAIL'}  token {hexv} ({label})")
        if not ok:
            fails.append(f"token/{hexv}")

    # 7) 不得残留旧臆造图形
    banned = ["markGrad", "ring-outer", "ring-inner", "--signal", "--void", "#0A0E27", "#1A1F3E"]
    left = [b for b in banned if b in site_raw or b in css]
    print(f"{'OK  ' if not left else 'FAIL'}  无旧图形残留 {left if left else ''}")
    if left:
        fails.append("residue")

    print()
    if fails:
        print(f"{len(fails)} 项未通过: " + ", ".join(fails))
        return 1
    print("图标四层（底板/气泡+星芒/青点/路由标）齐全，配色与 tokens.css 对齐")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
