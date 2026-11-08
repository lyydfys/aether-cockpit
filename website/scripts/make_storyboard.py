"""
把开屏动效的关键帧拼成一张分镜图（storyboard）。

用法（先起本地服务，默认 8125）：
    python website/scripts/make_storyboard.py [url] [out.png]

关键点：用真实时间等到目标时刻再截图。
开屏的层次是「JS 定时器 → 触发 CSS 过渡」，Playwright 的假时钟只推进定时器、
不推进 CSS 过渡，定格帧会停在过渡起点，所以这里不能用 page.clock。
"""
from __future__ import annotations

import subprocess
import sys
import tempfile
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent

FRAMES = [
    (420, "T+0.42s", "浅色底板铺开，图标落在屏幕上"),
    (780, "T+0.78s", "蓝色气泡弹性成形"),
    (1080, "T+1.08s", "白色四角星芒旋入落定"),
    (1330, "T+1.33s", "四个青点依次点亮"),
    (1660, "T+1.66s", "路由徽标：圆盘→连线→三节点"),
    (2020, "T+2.02s", "字标逐字上浮"),
    (2340, "T+2.34s", "副标淡入，字距收紧"),
    (2680, "T+2.68s", "图标收缩飞向导航槽位"),
]

CELL_W = 720
CELL_H = 450
PAD = 18
LABEL_H = 62
COLS = 4

BG = (15, 17, 21)
INK = (232, 234, 238)
DIM = (141, 148, 159)
FAINT = (107, 114, 128)
BLUE = (26, 115, 232)
EDGE = (42, 47, 56)


def font(size: int, bold: bool = False) -> ImageFont.FreeTypeFont:
    name = "C:/Windows/Fonts/msyhbd.ttc" if bold else "C:/Windows/Fonts/msyh.ttc"
    try:
        return ImageFont.truetype(name, size)
    except OSError:
        return ImageFont.load_default()


def grab(url: str, out_dir: Path) -> list[Path]:
    """逐帧截图：以动画真实起点为基准等到目标时刻。"""
    shots = []
    for i, (ms, _, _) in enumerate(FRAMES):
        target = out_dir / f"frame-{i:02d}.png"
        script = f"""
const {{ chromium }} = require('playwright');
(async () => {{
  const b = await chromium.launch({{ executablePath: process.env.AETHER_CHROME || undefined }});
  const p = await b.newPage({{ viewport: {{ width: 1440, height: 900 }} }});
  await p.goto({url!r}, {{ waitUntil: 'domcontentloaded' }});
  await p.waitForFunction(() => !!window.__aetherTimeline, null, {{ timeout: 10000 }});
  await p.evaluate((t) => new Promise((resolve) => {{
    const tick = () => {{
      const tl = window.__aetherTimeline;
      if (performance.now() - tl.startedAt >= t) resolve();
      else requestAnimationFrame(tick);
    }};
    tick();
  }}), {ms});
  await p.screenshot({{ path: {str(target)!r} }});
  await b.close();
}})();
"""
        subprocess.run(["node", "-e", script], check=True, cwd=ROOT.parent)
        shots.append(target)
    return shots


def compose(shots: list[Path], out: Path) -> None:
    rows = (len(shots) + COLS - 1) // COLS
    W = COLS * CELL_W + (COLS + 1) * PAD
    H = rows * (CELL_H + LABEL_H) + (rows + 1) * PAD + 74
    canvas = Image.new("RGB", (W, H), BG)
    draw = ImageDraw.Draw(canvas)

    title_font = font(30, bold=True)
    time_font = font(19, bold=True)
    note_font = font(17)

    draw.text((PAD, 22), "AETHER IGNITION — 开屏分镜", font=title_font, fill=INK)
    draw.text((W - PAD, 30), "aether cockpit · 图标按自身构成长出来", font=note_font, fill=FAINT, anchor="ra")

    for i, (path, (_, stamp, note)) in enumerate(zip(shots, FRAMES)):
        col = i % COLS
        row = i // COLS
        x = PAD + col * (CELL_W + PAD)
        y = 74 + PAD + row * (CELL_H + LABEL_H + PAD)

        with Image.open(path) as im:
            im = im.convert("RGB").resize((CELL_W, CELL_H), Image.LANCZOS)
            canvas.paste(im, (x, y))

        draw.rectangle([x, y, x + CELL_W - 1, y + CELL_H - 1], outline=EDGE, width=2)
        draw.text((x + 2, y + CELL_H + 8), stamp, font=time_font, fill=BLUE)
        draw.text((x + 2, y + CELL_H + 34), note, font=note_font, fill=DIM)

    canvas.save(out, optimize=True)
    print(f"storyboard -> {out}  ({canvas.width}x{canvas.height})")


def main() -> int:
    url = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8125/index.html"
    out = Path(sys.argv[2]) if len(sys.argv) > 2 else ROOT / "assets" / "img" / "ignition-storyboard.png"
    with tempfile.TemporaryDirectory() as tmp:
        shots = grab(url, Path(tmp))
        compose(shots, out)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
