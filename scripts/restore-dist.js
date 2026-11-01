// One-off recovery: the Wails -clean build replaced internal/web/dist with the
// Vue build, wiping the aether-based UI. The running gateway still embeds the
// correct files, and its FileServer lists directories, so crawl it back.
const fs = require("fs");
const path = require("path");

const base = process.argv[2] || "http://127.0.0.1:7888/";
const out = process.argv[3] || ".restore-dist";

async function get(p) {
  const r = await fetch(base + p);
  if (!r.ok) return null;
  return Buffer.from(await r.arrayBuffer());
}

async function listDir(p) {
  const b = await get(p);
  if (!b) return [];
  const html = b.toString();
  const re = /<a href="([^"]+)">/g;
  const list = [];
  let m;
  while ((m = re.exec(html))) list.push(m[1]);
  return list;
}

(async () => {
  const files = new Set([
    "index.html", "app.js", "app.css", "i18n.js", "kernel.js", "start.js",
    "custom.js", "overrides.css", "context.css", "context.js", "pluginpages.js",
    "plugins.css", "plugins.js", "purposes.js", "omarchy.css", "favicon.svg",
  ]);
  async function crawl(dir) {
    for (const e of await listDir(dir)) {
      if (e.endsWith("/")) await crawl(dir + e);
      else files.add(dir + e);
    }
  }
  await crawl("plugins/");
  console.log("files", files.size);
  let miss = 0;
  for (const f of files) {
    const b = await get(f);
    if (!b) { console.log("MISS", f); miss++; continue; }
    const p = path.join(out, f);
    fs.mkdirSync(path.dirname(p), { recursive: true });
    fs.writeFileSync(p, b);
  }
  console.log("restored", files.size - miss, "missing", miss);
})();
