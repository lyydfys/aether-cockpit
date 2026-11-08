"""把站点里的绝对地址改成你自己的域名。

用法：
    python scripts/set-site-url.py https://example.com
    python scripts/set-site-url.py https://example.com/gateway/     # 子路径

会改写 index.html 里的：
    <link rel="canonical">
    og:url / og:image / twitter:image

其它资源都是相对路径，不受影响。
"""

import io
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
INDEX = ROOT / "index.html"


def main() -> int:
    if len(sys.argv) != 2:
        print(__doc__)
        return 2

    url = sys.argv[1].strip()
    if not re.match(r"^https?://[^/\s]+", url):
        print(f"不是合法地址：{url}", file=sys.stderr)
        return 2
    if not url.endswith("/"):
        url += "/"

    s = io.open(INDEX, encoding="utf-8").read()
    image = url + "assets/img/og-cover.png"

    before = s

    # canonical：整个 href 换成新地址
    s = re.sub(
        r'(<link rel="canonical" href=")[^"]*(")',
        lambda m: m.group(1) + url + m.group(2),
        s,
    )
    # og:url
    s = re.sub(
        r'(<meta property="og:url" content=")[^"]*(")',
        lambda m: m.group(1) + url + m.group(2),
        s,
    )
    # og:image / twitter:image
    s = re.sub(
        r'(<meta property="og:image" content=")[^"]*(")',
        lambda m: m.group(1) + image + m.group(2),
        s,
    )
    s = re.sub(
        r'(<meta name="twitter:image" content=")[^"]*(")',
        lambda m: m.group(1) + image + m.group(2),
        s,
    )

    if s == before:
        print("没有改动（可能已经是该地址）")
    else:
        io.open(INDEX, "w", encoding="utf-8", newline="\n").write(s)
        print("已更新为:", url)

    # 回显当前值，方便核对
    head = s.split("</head>")[0]
    for key in ["canonical", "og:url", "og:image", "twitter:image"]:
        for m in re.finditer(rf'[^<>]*{re.escape(key)}[^<>]*>', head):
            line = m.group(0).strip()
            if "http" in line:
                print("   ", line[:130])
                break
    return 0


if __name__ == "__main__":
    sys.exit(main())
