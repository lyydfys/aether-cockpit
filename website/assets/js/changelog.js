/* 更新日志页：读取 /download/releases.json 并渲染。
   数据由服务器上的 sync-changelog.py 生成，前端只负责显示。 */

const listEl = document.getElementById("cl-list");
const metaEl = document.getElementById("cl-meta");
const emptyEl = document.getElementById("cl-empty");

/** 体积人性化 */
function humanSize(bytes) {
  if (!bytes) return "";
  const mb = bytes / 1048576;
  if (mb >= 1) return mb.toFixed(1) + " MB";
  return Math.max(1, Math.round(bytes / 1024)) + " KB";
}

/** 日期只保留年月日，且按本地时区 */
function fmtDate(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** 判断一个 release 里的包属于哪个平台 */
function platformOf(name) {
  if (/\.apk$/i.test(name)) return "Android";
  if (/windows/i.test(name)) return "Windows";
  if (/mac|darwin/i.test(name)) return "macOS";
  if (/linux/i.test(name)) return "Linux";
  return "安装包";
}

const TAG_HEAD = { h: "h3", "1": "h3" };

function renderBlocks(blocks) {
  const wrap = document.createElement("div");
  wrap.className = "cl-flow";

  for (const b of blocks || []) {
    if (b.t === "h") {
      // 版本号本身已是标题，正文里的标题统一降一级
      const el = document.createElement("h4");
      el.innerHTML = b.h;
      wrap.appendChild(el);
    } else if (b.t === "p") {
      const el = document.createElement("p");
      el.innerHTML = b.h;
      wrap.appendChild(el);
    } else if (b.t === "ul" || b.t === "ol") {
      const el = document.createElement(b.t);
      (b.items || []).forEach((it) => {
        const li = document.createElement("li");
        li.innerHTML = it;
        el.appendChild(li);
      });
      wrap.appendChild(el);
    } else if (b.t === "hr") {
      wrap.appendChild(document.createElement("hr"));
    }
  }
  return wrap;
}

function renderRelease(rel, index, total) {
  const card = document.createElement("article");
  card.className = "cl-rel";
  if (index === 0) card.classList.add("cl-rel--latest");

  const head = document.createElement("div");
  head.className = "cl-head";

  const ver = document.createElement("span");
  ver.className = "cl-ver";
  ver.textContent = rel.version || rel.tag;
  head.appendChild(ver);

  const date = document.createElement("span");
  date.className = "cl-date";
  date.textContent = fmtDate(rel.published);
  head.appendChild(date);

  if (index === 0) {
    const tag = document.createElement("span");
    tag.className = "cl-tag";
    tag.textContent = "最新";
    head.appendChild(tag);
  } else if (rel.prerelease) {
    const tag = document.createElement("span");
    tag.className = "cl-tag";
    tag.textContent = "预览版";
    head.appendChild(tag);
  }

  card.appendChild(head);
  card.appendChild(renderBlocks(rel.blocks));

  // 该版本自己的安装包
  if (rel.assets && rel.assets.length) {
    const dl = document.createElement("div");
    dl.className = "cl-dl";

    const label = document.createElement("span");
    label.className = "cl-dl-label";
    label.textContent = "此版本构建";
    dl.appendChild(label);

    rel.assets.forEach((a) => {
      const link = document.createElement("a");
      // 站点自己镜像的路径：/download/<文件名>
      link.href = "/download/" + encodeURIComponent(a.name);
      link.setAttribute("download", "");
      link.innerHTML =
        `<span>${platformOf(a.name)}</span>` +
        (a.size ? `<span class="cl-size">${humanSize(a.size)}</span>` : "");
      dl.appendChild(link);
    });

    card.appendChild(dl);
  }

  return card;
}

function showSkeleton(n = 3) {
  listEl.innerHTML = "";
  for (let i = 0; i < n; i++) {
    const s = document.createElement("div");
    s.className = "cl-skeleton";
    listEl.appendChild(s);
  }
}

async function main() {
  showSkeleton();

  let data;
  try {
    const res = await fetch("/download/releases.json", { cache: "no-store" });
    if (!res.ok) throw new Error("HTTP " + res.status);
    data = await res.json();
  } catch (err) {
    listEl.innerHTML = "";
    emptyEl.hidden = false;
    metaEl.textContent = "读取失败";
    return;
  }

  const rels = (data && data.releases) || [];
  if (!rels.length) {
    listEl.innerHTML = "";
    emptyEl.hidden = false;
    metaEl.textContent = "";
    return;
  }

  listEl.innerHTML = "";
  rels.forEach((rel, i) => listEl.appendChild(renderRelease(rel, i, rels.length)));

  metaEl.textContent =
    `共 ${rels.length} 个版本 · 最新 ${rels[0].version || rels[0].tag}` +
    ` · 更新于 ${fmtDate(rels[0].published)}`;
}

main();
