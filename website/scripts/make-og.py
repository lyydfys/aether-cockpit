"""生成社交分享卡（Open Graph），1200x630。

不另画一张图——直接用站点自己的首屏渲染出来，
这样分享卡和真实观感永远一致，不会出现"图比站好看"的落差。
"""

import io
import subprocess
import sys
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "assets/img/og-cover.png"
RAW = ROOT / "_og-raw.png"

URL = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8141/index.html"

# 用 1440x756 渲染（= 1200x630 的同比例），再缩放
W, H = 1440, 756

SCRIPT = f"""
const {{ chromium }} = require('playwright');
(async () => {{
  const b = await chromium.launch({{ executablePath: process.env.AETHER_CHROME || undefined }});
  const ctx = await b.newContext({{ viewport: {{ width: {W}, height: {H} }}, deviceScaleFactor: 2 }});
  await ctx.addInitScript(() => {{ try {{ localStorage.setItem('aether-theme','glass'); }} catch(e){{}} }});
  const p = await ctx.newPage();
  await p.goto('{URL}', {{ waitUntil: 'load' }});
  await p.waitForFunction(() => document.documentElement.classList.contains('is-ready'), null, {{ timeout: 20000 }});
  await p.waitForTimeout(1200);
  // 只截首屏上半，避开手机图被裁一半的尴尬
  await p.screenshot({{ path: '{RAW.as_posix()}' }});
  await b.close();
}})();
"""

r = subprocess.run(["node", "-e", SCRIPT], capture_output=True, text=True)
if r.returncode != 0:
    print(r.stderr[-800:], file=sys.stderr)
    sys.exit(1)

im = Image.open(RAW).convert("RGB")
print("原始渲染:", im.size)

# 等比缩到 1200 宽，再从顶部裁 630（保留标题与按钮，
# 底部手机图本来就只露一截，从中间裁会把头也切掉）
target_w, target_h = 1200, 630
scale = target_w / im.width
im2 = im.resize((target_w, round(im.height * scale)), Image.LANCZOS)
print("缩放后:", im2.size)

if im2.height >= target_h:
    im2 = im2.crop((0, 0, target_w, target_h))
else:
    # 高度不足时补底色（不应发生，留个保险）
    canvas = Image.new("RGB", (target_w, target_h), (238, 242, 250))
    canvas.paste(im2, (0, 0))
    im2 = canvas

# OG 卡建议 <300 KB：先降色再压
im2 = im2.convert("P", palette=Image.ADAPTIVE, colors=256)
im2.save(OUT, "PNG", optimize=True)
print(f"og-cover -> {OUT.name}  {im2.size}  {OUT.stat().st_size/1024:.0f} KB")

# 同时出一份 JPEG，供不支持/偏好 JPEG 的抓取器使用（体积更小）
jp = OUT.with_suffix(".jpg")
im2.convert("RGB").save(jp, "JPEG", quality=88, optimize=True, progressive=True)
print(f"og-cover -> {jp.name}  {jp.stat().st_size/1024:.0f} KB")

RAW.unlink(missing_ok=True)
