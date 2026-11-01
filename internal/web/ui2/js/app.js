/* 自有前端入口：注册路由、启动外壳。
 * 首屏只同步加载概览；其余 15 条路由在首次访问时按需 import。 */
import * as router from './router.js';
import { createShell } from './shell.js';
import { toast } from './components.js';
import { initLocale } from './i18n.js';
import overview from './views/overview.js';

const lazy = (load) => () => load().then((m) => m.default);

router.register('overview', overview);
// 只有概览是首屏必需；其余按需加载，首屏少解析约 80KB
router.register('channels', lazy(() => import('./views/channels.js')));
router.register('usage', lazy(() => import('./views/usage.js')));
router.register('settings', lazy(() => import('./views/settings.js')));
router.register('onboard', lazy(() => import('./views/onboard.js')));
router.register('scheduler', lazy(() => import('./views/scheduler.js')));
router.register('launcher', lazy(() => import('./views/launcher.js')));
router.register('logs', lazy(() => import('./views/logs.js')));
router.register('hud', lazy(() => import('./views/hud.js')));
router.register('gateway', lazy(() => import('./views/gateway.js')));
router.register('sessions', lazy(() => import('./views/sessions.js')));
router.register('library', lazy(() => import('./views/library.js')));
router.register('twofa', lazy(() => import('./views/twofa.js')));
router.register('agents', lazy(() => import('./views/misc.js').then((m) => ({ default: m.agents }))));
router.register('routing', lazy(() => import('./views/misc.js').then((m) => ({ default: m.routing }))));
router.register('masking', lazy(() => import('./views/misc.js').then((m) => ({ default: m.masking }))));
router.register('plugins', lazy(() => import('./views/misc.js').then((m) => ({ default: m.plugins }))));

// 主题：auto / light / dark，优先本地偏好
try {
  const theme = localStorage.getItem('aether.theme') || 'auto';
  document.documentElement.dataset.theme = theme;
} catch {}

// 未捕获异常不再只留在控制台：给一条 snackbar，页面继续可用。
addEventListener('error', (e) => { console.warn('[ui2] error', e.message); toast('页面出错：' + (e.message || '未知错误')); });
addEventListener('unhandledrejection', (e) => {
  const reason = e.reason;
  console.warn('[ui2] rejection', reason);
  toast('请求失败：' + ((reason && reason.message) || reason || '未知错误'));
});

await initLocale();
const shell = createShell({ navigate: router.navigate });
router.start((r, view) => shell.onRoute(r, view));
