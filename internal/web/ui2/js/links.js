/* 站外链接的唯一出口。
   桌面端跑在 Wails/WebView2 里：window.open 会开一个没有标题栏、也没有 Wails
   runtime 的裸窗口，用户关掉之前回不到主界面。所以外链一律交给后端 /api/open
   用系统默认浏览器打开；浏览器模式下同样在新标签页打开，行为一致。 */
import { openExternal } from './api.js';
import { toast } from './components.js';

export function openInBrowser(url) {
  if (!url) return;
  openExternal(url).catch((e) => toast('打开浏览器失败：' + (e.message || e)));
}
