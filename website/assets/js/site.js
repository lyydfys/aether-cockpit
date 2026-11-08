/* =========================================================
   站点交互
   - 滚动揭示
   - 导航吸附状态
   - 工作台标签页
   - 路由策略演示
   - 复制按钮
   - 数字滚动
   ========================================================= */

(function () {
  "use strict";

  const ready = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* ---------- 1) 滚动揭示 ---------- */
  const revealables = Array.from(document.querySelectorAll("[data-reveal]"));
  if ("IntersectionObserver" in window && !ready) {
    const io = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (!entry.isIntersecting) return;
          entry.target.classList.add("in");
          io.unobserve(entry.target);
        });
      },
      { rootMargin: "0px 0px -12% 0px", threshold: 0.12 }
    );
    revealables.forEach((n) => io.observe(n));
  } else {
    revealables.forEach((n) => n.classList.add("in"));
  }

  /* ---------- 2) 导航吸附 ---------- */
  const nav = document.getElementById("site-nav");
  let stuck = false;
  function onScroll() {
    const should = window.scrollY > 12;
    if (should !== stuck) {
      stuck = should;
      nav.dataset.stuck = String(should);
    }
  }
  onScroll();
  window.addEventListener("scroll", onScroll, { passive: true });

  /* ---------- 3) 工作台标签页 ---------- */
  const tabs = Array.from(document.querySelectorAll(".stage-tab"));
  const shots = Array.from(document.querySelectorAll(".stage-shot"));
  const captions = Array.from(document.querySelectorAll("[data-caption-for]"));

  function selectTab(tab) {
    const key = tab.dataset.shot;
    tabs.forEach((t) => {
      const on = t === tab;
      t.setAttribute("aria-selected", String(on));
      t.tabIndex = on ? 0 : -1;
    });
    shots.forEach((s) => s.setAttribute("data-active", String(s.dataset.shot === key)));
    captions.forEach((c) => {
      c.hidden = c.dataset.captionFor !== key;
    });
  }

  tabs.forEach((tab, i) => {
    tab.addEventListener("click", () => selectTab(tab));
    tab.addEventListener("keydown", (e) => {
      let next = -1;
      if (e.key === "ArrowRight") next = (i + 1) % tabs.length;
      else if (e.key === "ArrowLeft") next = (i - 1 + tabs.length) % tabs.length;
      else if (e.key === "Home") next = 0;
      else if (e.key === "End") next = tabs.length - 1;
      if (next < 0) return;
      e.preventDefault();
      tabs[next].focus();
      selectTab(tabs[next]);
    });
  });

  /* ---------- 4) 路由策略演示 ---------- */
  const routeItems = Array.from(document.querySelectorAll(".route-item"));
  const pucks = {
    a: document.querySelector('[data-node="a"]'),
    b: document.querySelector('[data-node="b"]'),
    c: document.querySelector('[data-node="c"]'),
  };
  const links = {
    a: document.querySelector('[data-link="a"]'),
    b: document.querySelector('[data-link="b"]'),
    c: document.querySelector('[data-link="c"]'),
  };
  const routeNote = document.getElementById("route-note");

  const STRATEGIES = {
    sticky: { hit: ["a"], note: "sticky → 账号 A · 已绑定会话" },
    round: { hit: ["b"], note: "round-robin → 账号 B · 轮到此位" },
    priority: { hit: ["a"], note: "priority → 账号 A · 排在最前" },
    quota: { hit: ["b"], note: "quota-aware → 账号 B · 余量 88% 最高" },
  };

  let roundCursor = 0;
  function paint(strategy) {
    let conf = STRATEGIES[strategy];
    if (strategy === "round") {
      roundCursor = (roundCursor + 1) % 3;
      const key = ["a", "b", "c"][roundCursor];
      conf = { hit: [key], note: "round-robin → 账号 " + key.toUpperCase() + " · 依次轮转" };
    }
    Object.entries(pucks).forEach(([k, el]) => {
      if (!el) return;
      const on = conf.hit.includes(k);
      el.dataset.hit = String(on);
      // 命中账号向外挑一点，形成受力感
      el.style.transform = on ? "translateY(-3px) scale(1.08)" : "none";
    });
    Object.entries(links).forEach(([k, path]) => {
      if (!path) return;
      path.dataset.live = String(conf.hit.includes(k));
    });
    if (routeNote) routeNote.textContent = conf.note;
  }

  routeItems.forEach((item) => {
    item.addEventListener("click", () => {
      routeItems.forEach((r) => r.setAttribute("aria-pressed", String(r === item)));
      paint(item.dataset.strategy);
    });
  });
  paint("sticky");

  // 自动示范一次轮转，提示这里可点
  if (!ready) {
    const routeSec = document.getElementById("routing");
    let autoPlayed = false;
    if ("IntersectionObserver" in window && routeSec) {
      const ro = new IntersectionObserver(
        (entries) => {
          entries.forEach((en) => {
            if (!en.isIntersecting || autoPlayed) return;
            autoPlayed = true;
            const roundBtn = routeItems.find((r) => r.dataset.strategy === "round");
            if (!roundBtn) return;
            setTimeout(() => {
              roundBtn.setAttribute("aria-pressed", "true");
              routeItems.forEach((r) => r !== roundBtn && r.setAttribute("aria-pressed", "false"));
              paint("round");
              setTimeout(() => {
                const stickyBtn = routeItems.find((r) => r.dataset.strategy === "sticky");
                if (!stickyBtn) return;
                stickyBtn.setAttribute("aria-pressed", "true");
                routeItems.forEach((r) => r !== stickyBtn && r.setAttribute("aria-pressed", "false"));
                paint("sticky");
              }, 2000);
            }, 700);
            ro.disconnect();
          });
        },
        { threshold: 0.4 }
      );
      ro.observe(routeSec);
    }
  }

  /* ---------- 5) 复制 ---------- */
  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise((resolve, reject) => {
      try {
        const ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
        resolve();
      } catch (err) {
        reject(err);
      }
    });
  }

  function flash(btn) {
    btn.dataset.copied = "true";
    const prev = btn.getAttribute("aria-label") || "";
    btn.setAttribute("aria-label", "已复制");
    setTimeout(() => {
      btn.dataset.copied = "false";
      if (prev) btn.setAttribute("aria-label", prev);
    }, 1500);
  }

  document.querySelectorAll(".copy-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      let text = btn.dataset.copy;
      if (!text && btn.dataset.copyTarget) {
        const target = document.getElementById(btn.dataset.copyTarget);
        if (target) text = target.innerText.replace(/\u00a0/g, " ");
      }
      if (!text) return;
      copyText(text).then(
        () => flash(btn),
        () => {
          btn.dataset.copied = "false";
        }
      );
    });
  });

  /* ---------- 6) 数字滚动 ---------- */
  function animateCount(node) {
    const target = Number(node.dataset.count);
    if (!Number.isFinite(target) || ready) return;
    const dur = 900;
    const t0 = performance.now();
    function frame(now) {
      const p = Math.min(1, (now - t0) / dur);
      const eased = 1 - Math.pow(1 - p, 3);
      node.textContent = String(Math.round(target * eased));
      if (p < 1) requestAnimationFrame(frame);
    }
    requestAnimationFrame(frame);
  }

  const counters = Array.from(document.querySelectorAll("[data-count]"));
  if ("IntersectionObserver" in window && !ready) {
    const co = new IntersectionObserver(
      (entries) => {
        entries.forEach((en) => {
          if (!en.isIntersecting) return;
          animateCount(en.target);
          co.unobserve(en.target);
        });
      },
      { threshold: 0.6 }
    );
    counters.forEach((c) => co.observe(c));
  }

  /* ---------- 7) 主题：黑 / 白 ---------- */
  const root = document.documentElement;
  const themeButtons = Array.from(document.querySelectorAll("[data-theme-set]"));
  const THEME_KEY = "aether-theme";

  // theme-color：浏览器 UI 的取色，需与各主题底色一致
  const THEMES = {
    dark: "#0f1115",
    light: "#f7f8fa",
    glass: "#eef2fa",
  };

  function applyTheme(name, persist) {
    const t = THEMES[name] ? name : "glass";
    root.dataset.theme = t;
    themeButtons.forEach((b) => {
      b.setAttribute("aria-pressed", String(b.dataset.themeSet === t));
    });
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.setAttribute("content", THEMES[t]);
    // 玻璃主题需要环境光层
    document.body.classList.toggle("is-glass", t === "glass");
    if (persist) {
      try {
        localStorage.setItem(THEME_KEY, t);
      } catch (err) {
        /* 隐私模式下忽略 */
      }
    }
  }

  // 初始主题：用户上次选择 > 柔光玻璃（默认）
  // 不跟随系统深/浅偏好：玻璃是这套站点的品牌表情，
  // 访客系统是深色时也不应该换成纯黑。
  let saved = null;
  try {
    saved = localStorage.getItem(THEME_KEY);
  } catch (err) {
    saved = null;
  }
  if (saved !== "light" && saved !== "dark" && saved !== "glass") {
    saved = "glass";
  }
  applyTheme(saved, false);

  // 主题顺序：与导航开关的排列一致
  const THEME_CYCLE = ["glass", "light", "dark"];

  function switchTheme(name) {
    // 切换瞬间给全局挂过渡，避免硬跳色
    root.classList.add("theme-fade");
    applyTheme(name, true);
    clearTimeout(applyTheme.timer);
    applyTheme.timer = setTimeout(() => root.classList.remove("theme-fade"), 300);
  }

  themeButtons.forEach((btn) => {
    btn.addEventListener("click", () => switchTheme(btn.dataset.themeSet));
  });

  // 手机上主题开关收成单按钮（隐藏未选中项），
  // 此时点击可见的那一格应当"切到下一个主题"而不是重复选中自己。
  // 用容器捕获，保证窄屏下依然能轮换。
  const themeSwitch = document.querySelector(".theme-switch");
  if (themeSwitch) {
    themeSwitch.addEventListener("click", (e) => {
      if (window.innerWidth > 620) return;
      const current = root.dataset.theme || "glass";
      const idx = THEME_CYCLE.indexOf(current);
      const next = THEME_CYCLE[(idx + 1) % THEME_CYCLE.length];
      e.stopPropagation();
      switchTheme(next);
    });
  }

  /* ---------- 8) 首屏平台开关 ---------- */
  const platformTabs = Array.from(document.querySelectorAll(".platform-tab"));
  const showcase = document.querySelector(".showcase");

  function selectPlatform(name) {
    platformTabs.forEach((t) => {
      t.setAttribute("aria-selected", String(t.dataset.platform === name));
    });
    if (showcase) showcase.dataset.focus = name === "mobile" ? "mobile" : "desktop";
  }

  platformTabs.forEach((tab) => {
    tab.addEventListener("click", () => selectPlatform(tab.dataset.platform));
    tab.addEventListener("keydown", (e) => {
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
      e.preventDefault();
      const i = platformTabs.indexOf(tab);
      const next = platformTabs[(i + (e.key === "ArrowRight" ? 1 : -1) + platformTabs.length) % platformTabs.length];
      next.focus();
      selectPlatform(next.dataset.platform);
    });
  });

  /* ---------- 8.5) 柔光玻璃：交互光效 ----------
     对应 HyperOS 官方描述的两项能力：
       - Tap to see light glow    → 按下时整块发亮（CSS :active）
       - Drag to see light flow   → 高光点跟随指针位置
     只依赖 pointer 事件，不做每帧 rAF，避免长页面上的持续开销。 */
  const GLASS_SELECTOR =
    ".g-surface, .cap, .flow-card, .laptop, .phone, .stage-panel, .route-stage, .security, .dl-card, .step";
  let glossBound = false;

  function ensureGloss(node) {
    let gloss = node.querySelector(":scope > .g-gloss");
    if (!gloss) {
      gloss = document.createElement("span");
      gloss.className = "g-gloss";
      gloss.setAttribute("aria-hidden", "true");
      node.appendChild(gloss);
    }
    return gloss;
  }

  function bindGloss() {
    if (glossBound) return;
    glossBound = true;

    const nodes = Array.from(document.querySelectorAll(GLASS_SELECTOR));

    nodes.forEach((node) => {
      ensureGloss(node);

      node.addEventListener(
        "pointermove",
        (e) => {
          if (document.documentElement.dataset.theme !== "glass") return;
          const r = node.getBoundingClientRect();
          node.style.setProperty("--gx", (((e.clientX - r.left) / r.width) * 100).toFixed(2) + "%");
          node.style.setProperty("--gy", (((e.clientY - r.top) / r.height) * 100).toFixed(2) + "%");
          // 按住移动 = 拖拽：光点变亮并跟随（light flow）
          if (e.buttons & 1) node.classList.add("is-dragging");
        },
        { passive: true }
      );

      node.addEventListener("pointerdown", () => {
        node.classList.add("is-dragging");
      });

      const release = () => node.classList.remove("is-dragging");
      node.addEventListener("pointerup", release);
      node.addEventListener("pointercancel", release);

      node.addEventListener("pointerleave", () => {
        node.classList.remove("is-dragging");
        node.style.removeProperty("--gx");
        node.style.removeProperty("--gy");
      });
    });
  }

  bindGloss();

  /* ---------- 8.6) 小屏抽屉导航 ----------
     900px 以下 nav-links 隐藏，靠这个抽屉提供入口。
     收起时用 [hidden]，展开时由 .site-nav[data-drawer="open"] 控制。 */
  const navToggle = document.querySelector(".nav-toggle");
  const navDrawer = document.getElementById("nav-drawer");
  const navScrim = document.getElementById("nav-scrim");
  const siteNav = document.getElementById("site-nav");

  function setDrawer(open) {
    if (!navToggle || !navDrawer || !siteNav) return;
    navToggle.setAttribute("aria-expanded", String(open));
    navDrawer.hidden = !open;
    if (navScrim) navScrim.hidden = !open;
    siteNav.dataset.drawer = open ? "open" : "closed";
  }

  if (navToggle && navDrawer) {
    navToggle.addEventListener("click", () => {
      setDrawer(navToggle.getAttribute("aria-expanded") !== "true");
    });

    // 点抽屉里的链接后收起，否则半屏被挡住
    navDrawer.querySelectorAll("a").forEach((a) => {
      a.addEventListener("click", () => setDrawer(false));
    });

    // 点遮罩或空白处收起
    if (navScrim) {
      navScrim.addEventListener("click", () => setDrawer(false));
    }
    document.addEventListener("click", (e) => {
      if (navToggle.getAttribute("aria-expanded") !== "true") return;
      if (navDrawer.contains(e.target) || navToggle.contains(e.target)) return;
      if (navScrim && e.target === navScrim) return;
      setDrawer(false);
    });

    // Esc 收起
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape" && navToggle.getAttribute("aria-expanded") === "true") {
        setDrawer(false);
        navToggle.focus();
      }
    });

    // 放大到桌面宽度时自动收起，避免抽屉状态残留
    window.addEventListener("resize", () => {
      if (window.innerWidth > 900 && navToggle.getAttribute("aria-expanded") === "true") {
        setDrawer(false);
      }
    });
  }

  /* ---------- 8.7) 下载区：从 manifest 同步版本与大小 ----------
     下载文件镜像在本站 /download/ 下，发版后由 sync-downloads.sh 更新。
     页面不写死版本号，改从 manifest.json 读取，避免发版后文案对不上。
     读不到就保持 HTML 里的兜底值，不影响下载本身。 */
  function humanSize(bytes) {
    if (!bytes || bytes < 1024) return bytes + " B";
    const mb = bytes / 1048576;
    return mb >= 1 ? mb.toFixed(1) + " MB" : (bytes / 1024).toFixed(0) + " KB";
  }

  const autoLinks = {};

  function applyAutoLink() {
    const links = document.querySelectorAll("[data-dl-auto]");
    if (!links.length) return;
    const ua = navigator.userAgent || "";
    const isAndroid = /Android/i.test(ua);
    const url = isAndroid ? autoLinks.android : autoLinks.windows;
    if (!url) return;
    links.forEach((a) => {
      a.setAttribute("href", url);
      // 安卓上把文案说清楚，避免用户点完才发现装不上
      if (isAndroid) a.textContent = "下载 Android 版";
    });
  }

  async function syncDownloadInfo() {
    let data;
    try {
      const res = await fetch("/download/manifest.json", { cache: "no-store" });
      if (!res.ok) return;
      data = await res.json();
    } catch (err) {
      // 离线或清单尚未生成：保留 HTML 兜底值
      return;
    }
    if (!data || !Array.isArray(data.assets)) return;

    const version = data.version || "";
    const verEls = document.querySelectorAll("[data-dl-version]");
    verEls.forEach((el) => {
      if (version) el.textContent = version;
    });

    const lineEl = document.querySelector("[data-dl-version-line]");
    if (lineEl && version) lineEl.textContent = "当前版本 " + version;

    data.assets.forEach((asset) => {
      const name = String(asset.name || "");
      let kind = null;
      if (/\.apk$/i.test(name)) kind = "android";
      else if (/\.zip$/i.test(name)) kind = "windows";
      if (!kind || !asset.url) return;

      // 按钮：贴上真实地址（万一版本变了，链接自动跟对）
      document.querySelectorAll(`[data-dl-btn="${kind}"]`).forEach((a) => {
        a.setAttribute("href", asset.url);
      });

      // 大小标签
      const sizeEl = document.querySelector(`[data-dl-size="${kind}"]`);
      if (sizeEl && asset.size) sizeEl.textContent = humanSize(asset.size);

      // 底部 CTA：按当前设备指向合适的包（安卓给 APK，其余给 Windows）
      if (kind === "android" || kind === "windows") {
        autoLinks[kind] = asset.url;
      }

      // 文件名与版本：优先只更新版本节点，保留 <code> 结构；
      // 若该节点不存在（例如换成纯文本），整体重写兜底。
      const fileBox = document.querySelector(`[data-dl-file="${kind}"] code`);
      if (fileBox) {
        const verNode = fileBox.querySelector("[data-dl-version]");
        if (verNode) {
          if (version) verNode.textContent = version.replace(/^v(?=\d)/, "");
        } else if (name) {
          fileBox.textContent = name;
        }
      }
    });
  }

  syncDownloadInfo().then(applyAutoLink);

  // 点击下载时给一点反馈（大文件下载浏览器不会有立即反馈）
  document.querySelectorAll("[data-dl-btn]").forEach((a) => {
    a.addEventListener("click", () => {
      a.classList.add("is-downloading");
      const label = a.textContent;
      a.textContent = "开始下载…";
      setTimeout(() => {
        a.classList.remove("is-downloading");
        a.textContent = label;
      }, 2200);
    });
  });

  /* ---------- 9) 锚点平滑滚动（补偿固定导航高度） ---------- */
  document.querySelectorAll('a[href^="#"]').forEach((a) => {
    a.addEventListener("click", (e) => {
      const id = a.getAttribute("href").slice(1);
      if (!id) return;
      const target = document.getElementById(id);
      if (!target) return;
      e.preventDefault();
      const y = target.getBoundingClientRect().top + window.scrollY - 76;
      window.scrollTo({ top: y, behavior: ready ? "auto" : "smooth" });
      history.replaceState(null, "", "#" + id);
    });
  });
})();
