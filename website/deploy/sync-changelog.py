"""把 GitHub release 说明同步成站内更新日志。

产出：
    /opt/aether-site/releases.json   结构化数据（页面读它渲染）
    （不生成 HTML，渲染在前端做，避免在服务器上跑模板）

为什么不用 markdown 库：
    站点是零构建静态页，不想为一个小页面引入依赖。
    release 正文用的是很有限的 Markdown 子集（标题/列表/粗体/行内码/链接），
    这里直接转成安全的 HTML，白名单式处理，不执行任意内容。
"""

import hashlib
import json
import os
import re
import subprocess
import sys

REPO = os.environ.get("REPO", "lyydfys/aether-cockpit")
OUT_DIR = os.environ.get("OUT_DIR", "/opt/aether-site")
API = f"https://api.github.com/repos/{REPO}/releases?per_page=30"


def fetch():
    # 注意：服务器是 Python 3.6，不能用 capture_output/text（3.7+ 才有）
    r = subprocess.run(
        ["curl", "-fsSL", "--max-time", "40", API],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        universal_newlines=True,
    )
    if r.returncode != 0:
        print("curl 失败:", (r.stderr or "")[:300], file=sys.stderr)
        sys.exit(1)
    data = json.loads(r.stdout)
    if not isinstance(data, list):
        print("API 返回异常:", data, file=sys.stderr)
        sys.exit(1)
    return data


def esc(s: str) -> str:
    return (
        s.replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
    )


def inline(s: str) -> str:
    """行内：`code`、**bold**、[text](url)"""
    s = esc(s)
    # 用函数做替换：替换串里的反斜杠会被 re 当成转义（\b 会变成退格符），
    # 正文里出现 \base_url 这类内容就会被吃掉字符。函数返回则原样保留。
    # 否则页面上会显示一个多余的反斜杠。
    # 注意：不要用正则"清理"反斜杠。无法可靠区分手误的 \base_url 与
    # Windows 路径 C:\Users，强删会把 C:\Users\x 变成 C:Usersx，比原问题更糟。
    # 下面用函数做替换（而不是替换串），反斜杠会原样保留 —— 内容不失真即可。
    s = re.sub(r"`([^`]+)`", lambda m: "<code>" + m.group(1) + "</code>", s)
    s = re.sub(
        r"\*\*([^*]+)\*\*", lambda m: "<strong>" + m.group(1) + "</strong>", s
    )

    def link(m):
        text, url = m.group(1), m.group(2)
        if not re.match(r"^https?://", url):
            return text
        return f'<a href="{url}" rel="noopener" target="_blank">{text}</a>'

    s = re.sub(r"\[([^\]]+)\]\((https?://[^)\s]+)\)", link, s)
    return s


def strip_download_section(body: str) -> str:
    """去掉正文开头那段「## 下载」表格 —— 页面上按钮就在旁边，重复。"""
    # 从 "## 下载" 到下一个 "---" 分隔线为止
    m = re.search(r"^##\s*下载\s*$", body, re.M)
    if not m:
        return body
    rest = body[m.end() :]
    sep = re.search(r"^---+\s*$", rest, re.M)
    if sep:
        return rest[sep.end() :].lstrip("\n")
    return rest.lstrip("\n")


def to_blocks(body: str):
    """把 Markdown 转成块结构，前端按类型渲染。"""
    body = strip_download_section(body)
    blocks = []
    para = []
    lst = None  # ('ul'|'ol', [items])

    def flush_para():
        nonlocal para
        if para:
            blocks.append({"t": "p", "h": inline(" ".join(para))})
            para = []

    def flush_list():
        nonlocal lst
        if lst:
            blocks.append({"t": lst[0], "items": [inline(i) for i in lst[1]]})
            lst = None

    for raw in body.split("\n"):
        line = raw.rstrip()
        if not line.strip():
            flush_para()
            flush_list()
            continue

        m = re.match(r"^(#{2,4})\s+(.*)$", line)
        if m:
            flush_para()
            flush_list()
            level = len(m.group(1))  # 2→3 是因为页面里 h2 已被版本号占用
            blocks.append({"t": "h", "lv": min(level, 4), "h": inline(m.group(2))})
            continue

        if re.match(r"^---+\s*$", line):
            flush_para()
            flush_list()
            blocks.append({"t": "hr"})
            continue

        m = re.match(r"^[-*]\s+(.*)$", line)
        if m:
            flush_para()
            if not lst or lst[0] != "ul":
                flush_list()
                lst = ("ul", [])
            lst[1].append(m.group(1))
            continue

        m = re.match(r"^\d+[.)]\s+(.*)$", line)
        if m:
            flush_para()
            if not lst or lst[0] != "ol":
                flush_list()
                lst = ("ol", [])
            lst[1].append(m.group(1))
            continue

        # 列表项的续行（缩进）
        if lst and raw.startswith((" ", "\t")):
            lst[1][-1] += " " + line.strip()
            continue

        flush_list()
        para.append(line.strip())

    flush_para()
    flush_list()
    return blocks


def main() -> int:
    rels = fetch()
    out = []
    for rel in rels:
        if rel.get("draft"):
            continue
        tag = rel.get("tag_name") or ""
        body = rel.get("body") or ""
        blocks = to_blocks(body)
        assets = [
            {
                "name": a["name"],
                "size": a["size"],
                "url": a["browser_download_url"],
            }
            for a in rel.get("assets", [])
            if a["name"].endswith((".apk", ".zip"))
        ]
        out.append(
            {
                "tag": tag,
                "name": rel.get("name") or tag,
                "version": tag.lstrip("v"),
                "published": rel.get("published_at"),
                "prerelease": bool(rel.get("prerelease")),
                "url": rel.get("html_url"),
                "blocks": blocks,
                "assets": assets,
            }
        )

    payload = {
        "repo": REPO,
        "count": len(out),
        "digest": hashlib.sha256(
            json.dumps([r["tag"] for r in out]).encode()
        ).hexdigest()[:12],
        "releases": out,
    }

    os.makedirs(OUT_DIR, exist_ok=True)
    path = os.path.join(OUT_DIR, "releases.json")
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, ensure_ascii=False, indent=2, sort_keys=False)

    print(f"== 写入 {path}  ({os.path.getsize(path)} B)")
    print(f"   共 {len(out)} 个 release")
    for r in out:
        nb = len(r["blocks"])
        print(f"   {r['tag']:<10} {r['published'][:10]}  {nb} 块  {len(r['assets'])} 个包")
    return 0


if __name__ == "__main__":
    sys.exit(main())
