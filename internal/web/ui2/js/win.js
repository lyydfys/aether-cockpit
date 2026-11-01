/* 桌面窗口能力：只在 Wails 注入 window.runtime 时可用；浏览器里是空操作。
   Wails v2 没有多窗口，HUD 用「缩放主窗口 + 置顶 + 一键还原」实现。 */

export const hasWindowRuntime = () => typeof window !== 'undefined' && !!(window.runtime && window.runtime.WindowSetSize && window.runtime.WindowGetSize);

const KEY = 'aether.winSize';

export async function enterHUD() {
  if (!hasWindowRuntime()) return false;
  try {
    const size = await window.runtime.WindowGetSize(); // { w, h }
    if (size && size.w) sessionStorage.setItem(KEY, JSON.stringify({ w: size.w, h: size.h }));
    try { if (await window.runtime.WindowIsMaximised()) { window.runtime.WindowUnmaximise(); await new Promise((r) => setTimeout(r, 180)); } } catch {}
    window.runtime.WindowSetMinSize(240, 280);
    // 两个调用都是异步投递；不等一拍，SetSize 会被旧的 980×640 最小尺寸夹住。
    await new Promise((r) => setTimeout(r, 180));
    window.runtime.WindowSetSize(280, 320);
    window.runtime.WindowSetAlwaysOnTop(true);
    return true;
  } catch { return false; }
}

export async function exitHUD() {
  if (!hasWindowRuntime()) return;
  try {
    window.runtime.WindowSetAlwaysOnTop(false);
    try { if (await window.runtime.WindowIsMaximised()) { window.runtime.WindowUnmaximise(); await new Promise((r) => setTimeout(r, 180)); } } catch {}
    window.runtime.WindowSetMinSize(980, 640);
    // 与进入时同理：等最小尺寸生效，否则还原尺寸会被 980×640 夹住。
    await new Promise((r) => setTimeout(r, 180));
    let size = null;
    try { size = JSON.parse(sessionStorage.getItem(KEY) || 'null'); } catch {}
    window.runtime.WindowSetSize(size && size.w ? size.w : 1320, size && size.h ? size.h : 860);
  } catch {}
}
