"""打包一份可直接上传到云服务器的站点。

只收运行时需要的文件（index.html / 404.html / assets/），
开发脚本、Markdown、校验工具都不进包。

产出：
    dist/aether-cockpit-site.tar.gz
"""

import os
import subprocess
import sys
import tarfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DIST = ROOT / "dist"
OUT = DIST / "aether-cockpit-site.tar.gz"

# 只这些进包
INCLUDE_FILES = ["index.html", "404.html"]
INCLUDE_DIRS = ["assets"]

# 这些扩展名一律排除（开发产物）
EXCLUDE_EXT = {".py", ".mjs", ".md", ".txt"}
EXCLUDE_NAMES = {".gitignore", ".DS_Store"}


def iter_files():
    for name in INCLUDE_FILES:
        p = ROOT / name
        if p.exists():
            yield p
        else:
            print(f"警告：缺少 {name}", file=sys.stderr)
    for d in INCLUDE_DIRS:
        base = ROOT / d
        if not base.exists():
            continue
        for p in sorted(base.rglob("*")):
            if not p.is_file():
                continue
            if p.suffix in EXCLUDE_EXT and p.name not in {"OFL-Inter.txt", "OFL-NotoSansSC.txt"}:
                continue
            if p.name in EXCLUDE_NAMES:
                continue
            yield p


def main() -> int:
    DIST.mkdir(exist_ok=True)
    files = list(iter_files())

    # 校验：HTML 里引用的本地资源必须都在包里
    import re

    html = (ROOT / "index.html").read_text(encoding="utf-8")
    refs = set(re.findall(r'(?:src|href)="((?!https?:|#)[^"]+)"', html))
    packed_rel = {p.relative_to(ROOT).as_posix() for p in files}
    missing = sorted(r for r in refs if r not in packed_rel and not r.startswith("#"))
    if missing:
        print("引用但未打包：", missing, file=sys.stderr)
        return 1

    with tarfile.open(OUT, "w:gz", compresslevel=9) as tar:
        for p in files:
            tar.add(p, arcname=p.relative_to(ROOT).as_posix())

    total = sum(p.stat().st_size for p in files)
    print(f"打包 {len(files)} 个文件，原始 {total/1048576:.2f} MB")
    print(f"产出 dist/{OUT.name}  {OUT.stat().st_size/1048576:.2f} MB")
    print("\n服务器上解包：")
    print("  sudo mkdir -p /var/www/aether-cockpit")
    print("  sudo tar -xzf aether-cockpit-site.tar.gz -C /var/www/aether-cockpit")
    print("  sudo chown -R www-data:www-data /var/www/aether-cockpit")
    return 0


if __name__ == "__main__":
    sys.exit(main())
