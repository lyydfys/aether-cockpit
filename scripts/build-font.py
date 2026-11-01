#!/usr/bin/env python3
"""生成内置中文子集字体（Noto Sans SC，OFL）。

为什么要有这一步：
    界面原本把汉字交给系统字体，Windows 上是雅黑、macOS 上是苹方、Linux 上是 Noto，
    同一份设计在三个平台是三种字面高度与笔画粗细。"跨平台一致"这件事只能靠自己带字体，
    但完整 Noto Sans SC 三个字重约 30MB，进二进制显然不行。

这里做的是"够用且可复现"的子集：
    GB2312 一级汉字（3755，覆盖日常中文 99.7%）∪ 仓库源码里出现过的全部非 ASCII 字符
    ∪ CJK 标点与全角区。三个字重合计约 1.6MB。

    二级汉字（鑫、淼、垚这类多见于人名的字）不在子集里，界面上出现时会回落到系统字体。
    如果哪天需要覆盖，把 LEVEL2 = True 打开重跑，体积会到约 2.4MB。

用法：
    python -m pip install fonttools brotli
    python scripts/build-font.py            # 首次会下载源字体到临时目录

产物：internal/web/ui2/assets/fonts/noto-sans-sc-{400,500,600}.woff2
"""

import os
import sys
import glob
import urllib.request

try:
    from fontTools import subset
except ImportError:
    sys.exit("需要 fonttools 与 brotli：python -m pip install fonttools brotli")

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
UI = os.path.join(ROOT, "internal", "web", "ui2")
OUT_DIR = os.path.join(UI, "assets", "fonts")
CACHE = os.path.join(os.environ.get("TEMP") or "/tmp", "aether-font-src")

WEIGHTS = ("400", "500", "600")
SRC_URL = ("https://cdn.jsdelivr.net/npm/@fontsource/noto-sans-sc@5/files/"
           "noto-sans-sc-chinese-simplified-{w}-normal.woff2")

LEVEL2 = False  # 打开后包含 GB2312 二级汉字，体积约 +800KB

# 标点与全角区：这些字符几乎每个界面都会用到，整段收进来
PUNCT_RANGES = [
    (0x2000, 0x206F),  # 通用标点 —— … — ·
    (0x2E80, 0x2EFF),  # 部首补充
    (0x3000, 0x303F),  # 中文标点 。、；：「」
    (0xFE30, 0xFE4F),  # 兼容形式
    (0xFF00, 0xFFEF),  # 全角字符
]


def gb2312_hanzi(level2: bool) -> set:
    out = set()
    for cp in range(0x4E00, 0xA000):
        ch = chr(cp)
        try:
            b = ch.encode("gb2312")
        except UnicodeEncodeError:
            continue
        if not level2 and not (0xB0 <= b[0] <= 0xD7):  # 一级汉字区
            continue
        out.add(ch)
    return out


def repo_chars() -> set:
    """源码里出现过的非 ASCII 字符：保证所有写死的文案都在子集里。"""
    out = set()
    patterns = ["js/**/*.js", "*.html", "css/*.css"]
    for pat in patterns:
        for fp in glob.glob(os.path.join(UI, pat), recursive=True):
            with open(fp, encoding="utf-8") as f:
                out |= {c for c in f.read() if ord(c) >= 0x80}
    return out


def charset() -> set:
    cs = gb2312_hanzi(LEVEL2) | repo_chars()
    for lo, hi in PUNCT_RANGES:
        cs |= {chr(c) for c in range(lo, hi + 1)}
    return cs


def fetch(weight: str) -> str:
    os.makedirs(CACHE, exist_ok=True)
    dst = os.path.join(CACHE, f"nsc-{weight}.woff2")
    if not os.path.exists(dst) or os.path.getsize(dst) < 100_000:
        url = SRC_URL.format(w=weight)
        print(f"  下载 {url}")
        urllib.request.urlretrieve(url, dst)
    return dst


def build(src: str, chars: set, out: str) -> int:
    opts = subset.Options()
    opts.flavor = "woff2"
    opts.layout_features = ["*"]
    opts.name_IDs = ["*"]
    opts.notdef_outline = True
    opts.recalc_bounds = True
    font = subset.load_font(src, opts)
    sub = subset.Subsetter(options=opts)
    sub.populate(text="".join(sorted(chars)))
    sub.subset(font)
    subset.save_font(font, out, opts)
    return os.path.getsize(out)


def main() -> None:
    chars = charset()
    hanzi = sum(1 for c in chars if 0x4E00 <= ord(c) <= 0x9FFF)
    print(f"字符集：{len(chars)} 个（其中汉字 {hanzi}）")
    os.makedirs(OUT_DIR, exist_ok=True)
    total = 0
    for w in WEIGHTS:
        src = fetch(w)
        out = os.path.join(OUT_DIR, f"noto-sans-sc-{w}.woff2")
        size = build(src, chars, out)
        total += size
        print(f"  {os.path.basename(out)}  {size / 1024:.0f} KB")
    print(f"合计 {total / 1024:.0f} KB（{total / 1024 / 1024:.2f} MB）")


if __name__ == "__main__":
    main()
