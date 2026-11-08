/* =========================================================
   AETHER IGNITION — 开屏动效
   ---------------------------------------------------------
   叙事：不是"画一个抽象图形"，而是让 App 图标按它自己的
   构成顺序长出来，最后飞进导航栏。

   图标构成（照搬 internal/web/ui2/assets/aether-cockpit.svg）：
     0. 浅色底板（108×108, rx 20）—— 元件们的"纸面"，也给青点与
        路由标提供对比度，这是它们在 40px 仍可辨的前提
     1. 蓝色对话气泡（容器）
     2. 白色四角星芒（主体）
     3. 四个青点（品牌细节）
     4. 右下路由徽标：白圆 + 青描边 + 蓝连线 + 三个节点

   节拍：
     T0     以太粒子从气泡轮廓外缘汇入中心
     T260   底板淡入并轻微回落
     T500   气泡成形
     T860   星芒旋入、放大落定
     T1160  四个青点依次点亮（放大回弹，让细节被看见）
     T1440  路由徽标：圆盘→连线→三个节点依次到位
     T1900  字标逐字上浮
     T2120  副标淡入
     T2480  图标缩为导航标记飞入左上角
     T3020  遮罩揭开
   ========================================================= */

(function () {
  "use strict";

  const root = document.getElementById("ignition");
  if (!root) return;

  const canvas = document.getElementById("ignition-canvas");
  const mark = document.getElementById("ignition-mark");
  const skipBtn = document.getElementById("ignition-skip");
  const meter = root.querySelector(".ignition-meter i");
  const words = root.querySelector(".ignition-words");
  const letters = Array.from(root.querySelectorAll(".ignition-name span"));
  const tag = root.querySelector(".ignition-tag");
  const navSlot = document.getElementById("nav-mark-slot");
  const navSvg = navSlot ? navSlot.querySelector("svg") : null;

  const plateBg = root.querySelector(".bm-bg");
  const bubble = root.querySelector(".bm-bubble");
  const star = root.querySelector(".bm-star");
  const dotsA = Array.from(root.querySelectorAll(".bm-dot-a"));
  const dotsB = Array.from(root.querySelectorAll(".bm-dot-b"));
  const plate = root.querySelector(".bm-plate");
  const link = root.querySelector(".bm-link");
  const nodesA = Array.from(root.querySelectorAll(".bm-node-a"));
  const nodeB = root.querySelector(".bm-node-b");

  const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  const coarse = window.matchMedia("(pointer: coarse)").matches;

  /* =========================================================
     粒子场：沿气泡轮廓汇聚
     粒子不凭空乱飞——它们从图标外缘的圆弧上出发，
     朝图标中心收束，视觉上就是"元件归位"。
     ========================================================= */
  const ctx = canvas.getContext("2d");
  let W = 0;
  let H = 0;
  let DPR = 1;
  let grains = [];
  let rafId = 0;
  let running = true;
  let t0 = 0;
  let phase = 0;

  const TONE = ["#1a73e8", "#4a9bf5", "#ccffff", "#13a99b"];

  const geo = { cx: 0, cy: 0, r: 0, ring: 0 };

  function measure() {
    DPR = Math.min(window.devicePixelRatio || 1, 1.5);
    W = canvas.clientWidth;
    H = canvas.clientHeight;
    canvas.width = Math.round(W * DPR);
    canvas.height = Math.round(H * DPR);
    ctx.setTransform(DPR, 0, 0, DPR, 0, 0);

    // 以图标真实位置为圆心
    let box = { x: W / 2, y: H / 2, r: 78 };
    if (mark) {
      const b = mark.getBoundingClientRect();
      if (b.width > 0) {
        box = { x: b.left + b.width / 2, y: b.top + b.height / 2, r: b.width / 2 };
      }
    }
    geo.cx = box.x;
    geo.cy = box.y;
    geo.r = box.r;
    // 粒子从图标外缘 1.15~1.9 倍半径处起步
    geo.ring = box.r;
  }

  class Grain {
    constructor(i) {
      this.i = i;
      this.spawn(true);
    }

    spawn(initial) {
      const ringCount = 3;
      const band = this.i % ringCount;
      const base = (this.i / ringCount) * Math.PI * 2 + Math.random() * 0.6;
      this.angle = base;
      this.spin = (Math.random() < 0.5 ? -1 : 1) * (0.35 + Math.random() * 0.55);
      this.radius = geo.r * (1.18 + band * 0.24 + Math.random() * 0.22);
      this.startR = this.radius;
      this.p = initial ? Math.random() * 0.82 : 0;
      this.rate = 0.05 + Math.random() * 0.06;
      this.size = 0.6 + Math.random() * 1.1;
      this.tone = TONE[(Math.random() * TONE.length) | 0];
      this.fadeIn = initial ? 1 : 0;
      this.dead = false;
      this.sync();
    }

    sync() {
      const r = this.startR * (1 - this.p * 0.94);
      this.r = r;
      this.x = geo.cx + Math.cos(this.angle) * r;
      this.y = geo.cy + Math.sin(this.angle) * r;
    }

    step(dt) {
      this.p += this.rate * (0.7 + phase * 0.9) * dt * 0.0016;
      // 越靠近中心转得越快，形成涡旋
      this.angle += this.spin * (0.4 + this.p * 2.2) * dt * 0.0012;
      if (this.p >= 1) {
        this.dead = true;
        return;
      }
      this.sync();
      this.fadeIn = Math.min(1, this.fadeIn + dt * 0.002);
      this.alpha = this.fadeIn * (0.12 + 0.88 * Math.pow(this.p, 1.2)) * (1 - Math.pow(this.p, 7));
    }

    draw() {
      const a = Math.max(0, Math.min(1, this.alpha || 0));
      if (a <= 0.014) return;
      ctx.fillStyle = hexA(this.tone, a);
      ctx.beginPath();
      ctx.arc(this.x, this.y, this.size * 0.6, 0, Math.PI * 2);
      ctx.fill();

      // 短拖尾，沿切向
      const ta = this.angle - this.spin * 0.16;
      const tx = geo.cx + Math.cos(ta) * this.r;
      const ty = geo.cy + Math.sin(ta) * this.r;
      ctx.strokeStyle = hexA(this.tone, a * 0.35);
      ctx.lineWidth = this.size * 0.8;
      ctx.lineCap = "round";
      ctx.beginPath();
      ctx.moveTo(this.x, this.y);
      ctx.lineTo(tx, ty);
      ctx.stroke();
    }
  }

  function hexA(hex, a) {
    const n = parseInt(hex.slice(1), 16);
    return (
      "rgba(" + ((n >> 16) & 255) + "," + ((n >> 8) & 255) + "," + (n & 255) + "," + a.toFixed(3) + ")"
    );
  }

  function buildGrains() {
    const count = coarse ? 70 : W < 900 ? 100 : 150;
    grains = [];
    for (let i = 0; i < count; i++) grains.push(new Grain(i));
  }

  function frame(ts) {
    if (!running) return;
    if (!t0) t0 = ts;
    const dt = Math.min(42, ts - (frame.prev || ts));
    frame.prev = ts;
    const t = ts - t0;

    // 前期缓慢铺陈，气泡成形后加速收束
    phase = t < 420 ? 0.2 : t < 900 ? 0.75 : 1.5;

    ctx.clearRect(0, 0, W, H);
    ctx.globalCompositeOperation = "lighter";
    for (let i = 0; i < grains.length; i++) {
      const g = grains[i];
      g.step(dt);
      if (g.dead) {
        g.spawn(false);
        continue;
      }
      g.draw();
    }
    ctx.globalCompositeOperation = "source-over";

    rafId = requestAnimationFrame(frame);
  }

  measure();
  buildGrains();
  if (!reduced) rafId = requestAnimationFrame(frame);

  let rz = 0;
  window.addEventListener("resize", () => {
    clearTimeout(rz);
    rz = setTimeout(() => {
      measure();
      buildGrains();
    }, 180);
  });

  /* =========================================================
     时间线
     ========================================================= */
  const T = {
    plate: 260,
    bubble: 500,
    star: 860,
    dots: 1160,
    route: 1440,
    letters: 1900,
    tag: 2120,
    collapse: 2480,
    mask: 3020,
    end: 3660,
  };
  const k = reduced ? 0.3 : 1;
  const at = (ms, fn) => setTimeout(fn, ms * k);

  let done = false;

  window.__aetherTimeline = { startedAt: performance.now(), marks: T, scale: k };

  function finish() {
    if (done) return;
    done = true;
    running = false;
    if (rafId) cancelAnimationFrame(rafId);
    if (navSvg) {
      navSvg.style.transition = "none";
      navSvg.style.opacity = "1";
    }
    root.dataset.state = "done";
    document.documentElement.classList.add("is-ready");
    window.dispatchEvent(new CustomEvent("aether:ready"));
  }

  /* 0) 底板：先铺纸面。轻微回弹，让图标"落在"屏幕上而不是淡入 */
  at(T.plate, () => {
    if (plateBg) {
      plateBg.style.transition =
        "opacity 480ms ease, transform 760ms cubic-bezier(.22,1.16,.34,1)";
      plateBg.style.transform = "scale(1)";
      plateBg.style.opacity = "1";
    }
  });

  /* 1) 蓝色气泡：从中心弹性放大，尾巴朝左下（与图标一致） */
  at(T.bubble, () => {
    if (bubble) {
      bubble.style.transition =
        "transform 900ms cubic-bezier(.22,1.18,.36,1), opacity 420ms ease";
      bubble.style.transform = "scale(1)";
      bubble.style.opacity = "1";
    }
  });

  /* 2) 白色星芒：旋入并放大，是图标的视觉重心 */
  at(T.star, () => {
    if (star) {
      star.style.transition =
        "transform 820ms cubic-bezier(.2,1.25,.3,1), opacity 380ms ease";
      star.style.transform = "scale(1) rotate(0deg)";
      star.style.opacity = "1";
    }
  });

  /* 3) 四个青点：错峰点亮。给一点放大回弹，
        否则 1.4px 的点在动画里几乎无声无息。 */
  at(T.dots, () => {
    [...dotsA, ...dotsB].forEach((d, i) => {
      d.style.transition =
        "opacity 420ms ease " + i * 100 + "ms, transform 520ms cubic-bezier(.3,1.5,.42,1) " + i * 100 + "ms";
      d.style.transform = "scale(1)";
      d.style.opacity = "1";
    });
  });

  /* 4) 路由徽标：白盘 → 连线 → 三个节点。
        这是图标里最"有信息量"的一笔，值得单独给一拍。 */
  at(T.route, () => {
    if (plate) {
      plate.style.transition = "opacity 380ms ease";
      plate.style.opacity = "1";
    }
    if (link) {
      link.style.transition = "opacity 360ms ease 200ms";
      link.style.opacity = "1";
    }
    nodesA.forEach((n, i) => {
      n.style.transition = "opacity 380ms ease " + (300 + i * 120) + "ms";
      n.style.opacity = "1";
    });
    if (nodeB) {
      nodeB.style.transition = "opacity 380ms ease 460ms";
      nodeB.style.opacity = "1";
    }
  });

  /* 5) 字标 */
  at(T.letters, () => {
    letters.forEach((s, i) => {
      const d = i * 26;
      s.style.transition = "opacity 540ms ease " + d + "ms, transform 640ms cubic-bezier(.16,1,.3,1) " + d + "ms";
      s.style.opacity = "1";
      s.style.transform = "translateY(0)";
    });
    if (skipBtn) skipBtn.style.opacity = "1";
  });

  /* 6) 副标 */
  at(T.tag, () => {
    if (!tag) return;
    tag.style.transition = "opacity 680ms ease, letter-spacing 860ms cubic-bezier(.16,1,.3,1)";
    tag.style.opacity = "1";
    tag.style.letterSpacing = "0.32em";
  });

  /* 7) 收缩飞入导航槽位 */
  function collapse() {
    if (done || !mark) return;

    if (meter) {
      meter.style.transition = "transform 520ms cubic-bezier(.5,0,.2,1)";
      meter.style.transform = "scaleX(1)";
    }
    if (words) {
      words.style.transition = "opacity 260ms ease, transform 260ms ease";
      words.style.opacity = "0";
      words.style.transform = "translateX(-50%) translateY(10px)";
    }
    if (skipBtn) skipBtn.style.opacity = "0";
    if (canvas) {
      canvas.style.transition = "opacity 560ms ease 80ms";
      canvas.style.opacity = "0";
    }

    const mr = mark.getBoundingClientRect();
    const sr = navSlot
      ? navSlot.getBoundingClientRect()
      : { left: 22, top: 17, width: 32, height: 32 };
    const scale = Math.max(0.05, sr.width / mr.width);
    const dx = sr.left - mr.left;
    const dy = sr.top - mr.top;

    requestAnimationFrame(() => {
      mark.style.transformOrigin = "top left";
      mark.style.transition = "transform 800ms cubic-bezier(.66,.02,.2,1)";
      mark.style.transform =
        "translate(" + dx.toFixed(1) + "px," + dy.toFixed(1) + "px) scale(" + scale.toFixed(4) + ")";
    });
  }

  at(T.collapse, collapse);

  at(T.mask, finish);

  at(T.end, () => {
    root.setAttribute("aria-hidden", "true");
    root.style.display = "none";
    if (navSvg) navSvg.style.opacity = "1";
  });

  /* ---------- 跳过：直达终态 ---------- */
  function skip(e) {
    if (e) {
      e.preventDefault();
      e.stopPropagation();
    }
    if (done) return;
    if (plateBg) {
      plateBg.style.transition = "none";
      plateBg.style.transform = "scale(1)";
      plateBg.style.opacity = "1";
    }
    if (bubble) {
      bubble.style.transition = "none";
      bubble.style.transform = "scale(1)";
      bubble.style.opacity = "1";
    }
    if (star) {
      star.style.transition = "none";
      star.style.transform = "scale(1) rotate(0deg)";
      star.style.opacity = "1";
    }
    [...dotsA, ...dotsB].forEach((d) => {
      d.style.transition = "none";
      d.style.transform = "scale(1)";
      d.style.opacity = "1";
    });
    [plate, link, nodeB, ...nodesA].forEach((n) => {
      if (!n) return;
      n.style.transition = "none";
      n.style.opacity = "1";
    });
    running = false;
    if (rafId) cancelAnimationFrame(rafId);
    finish();
    setTimeout(() => {
      root.style.display = "none";
    }, 700);
  }

  if (skipBtn) skipBtn.addEventListener("click", skip);
  root.addEventListener("click", (e) => {
    if (e.target === root || e.target.classList.contains("ignition-glow")) skip(e);
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !done) skip();
  });

  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible" && root.dataset.state === "done") {
      root.style.display = "none";
      if (navSvg) navSvg.style.opacity = "1";
    }
  });
})();
