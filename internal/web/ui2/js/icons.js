/* 自有图标集：统一 20×20 视框、1.7 描边，避免继承旧界面的图形。 */
import { svg } from './dom.js';

const P = {
  overview: '<rect x="3" y="3" width="6" height="6" rx="1.4"/><rect x="11" y="3" width="6" height="6" rx="1.4"/><rect x="3" y="11" width="6" height="6" rx="1.4"/><rect x="11" y="11" width="6" height="6" rx="1.4"/>',
  channels: '<circle cx="7" cy="10" r="3"/><path d="M10 10h7M14 10v3M16.5 10v2"/>',
  usage: '<path d="M4 16V9M8 16V5M12 16v-5M16 16V7"/>',
  library: '<path d="M5 3.5h9v13H5a1.5 1.5 0 0 1 0-3h9"/>',
  sessions: '<path d="M4 5h12v8H9l-4 3v-3H4z"/>',
  logs: '<path d="M4 5h12M4 10h12M4 15h8"/>',
  gateway: '<rect x="3" y="4" width="14" height="5" rx="1.5"/><rect x="3" y="11" width="14" height="5" rx="1.5"/><path d="M6 6.5h.01M6 13.5h.01"/>',
  settings: '<circle cx="10" cy="10" r="2.6"/><path d="M10 2.8v2M10 15.2v2M2.8 10h2M15.2 10h2M4.9 4.9l1.4 1.4M13.7 13.7l1.4 1.4M15.1 4.9l-1.4 1.4M6.3 13.7l-1.4 1.4"/>',
  plugin: '<path d="M8 3.5h4v2.5a1.5 1.5 0 0 0 3 0V3.5h1.5v13H4v-4h2.5a1.5 1.5 0 0 0 0-3H4v-6z"/>',
  agents: '<path d="M4 6l4 4-4 4M10 14h6"/>',
  routing: '<path d="M3 10h5l4-5h5M8 10l4 5h5"/>',
  masking: '<path d="M10 2.8 16 5v4.6c0 3.4-2.4 6.1-6 7.4-3.6-1.3-6-4-6-7.4V5Z"/>',
  plus: '<path d="M10 4v12M4 10h12"/>',
  refresh: '<path d="M16 10a6 6 0 1 1-1.8-4.3M16 3v3.5h-3.5"/>',
  export: '<path d="M10 13V3.5M6.5 7 10 3.5 13.5 7M4 12v4h12v-4"/>',
  folder: '<path d="M3 5.5h5l1.5 2H17v7H3z"/>',
  search: '<circle cx="9" cy="9" r="5"/><path d="m13 13 3.5 3.5"/>',
  more: '<circle cx="5" cy="10" r="1.2"/><circle cx="10" cy="10" r="1.2"/><circle cx="15" cy="10" r="1.2"/>',
  tag: '<path d="M3.5 9.5V4h5.5l7 7-5.5 5.5z"/><circle cx="6.8" cy="6.8" r=".9"/>',
  menu: '<path d="M4 6h12M4 10h12M4 14h12"/>',
  area: '<path d="M3 16.5V12l4-4.2 3.6 2.8L14.8 5 17 8.6v7.9z"/>',
  copy: '<rect x="7" y="7" width="9" height="9" rx="1.5"/><path d="M13 7V4.5A1.5 1.5 0 0 0 11.5 3h-7A1.5 1.5 0 0 0 3 4.5v7A1.5 1.5 0 0 0 4.5 13H7"/>',
  play: '<path d="M7 4.5 15 10l-8 5.5z"/>',
  trash: '<path d="M4.5 6h11M8 6V4h4v2M6 6l.7 10h6.6L14 6"/>',
  edit: '<path d="M4 16h3.5L16 7.5 12.5 4 4 12.5z"/>',
  external: '<path d="M9 4H4v12h12v-5M11 3h6v6M17 3l-7 7"/>',
  key: '<circle cx="6.5" cy="10" r="3"/><path d="M9.5 10H17M14 10v3M16 10v2"/>',
  shield: '<path d="M10 2.8 16 5v4.6c0 3.4-2.4 6.1-6 7.4-3.6-1.3-6-4-6-7.4V5Z"/><path d="m7.5 9.8 1.8 1.8 3.4-3.4"/>',
  check: '<path d="m4 10 4 4 8-8"/>',
  x: '<path d="m5 5 10 10M15 5 5 15"/>',
  minimize: '<path d="M4 10h12"/>',
  maximize: '<rect x="4" y="4" width="12" height="12" rx="1.5"/>',
  restore: '<rect x="6.5" y="6.5" width="9" height="9" rx="1.3"/><path d="M4 13V4h9"/>',
  chevron: '<path d="m8 5 5 5-5 5"/>',
  clock: '<circle cx="10" cy="10" r="7"/><path d="M10 6v4l2.5 1.5"/>',
  database: '<ellipse cx="10" cy="5.5" rx="6" ry="2.5"/><path d="M4 5.5v9c0 1.4 2.7 2.5 6 2.5s6-1.1 6-2.5v-9M4 10c0 1.4 2.7 2.5 6 2.5s6-1.1 6-2.5"/>',
  users: '<circle cx="7.5" cy="7" r="2.5"/><path d="M3 16c0-2.5 2-4 4.5-4s4.5 1.5 4.5 4"/><circle cx="14" cy="7.5" r="2"/><path d="M13 12.2c2.2.2 4 1.6 4 3.8"/>',
  link: '<path d="M8 12 12 8M6.5 9 4.8 10.7a2.8 2.8 0 0 0 4 4L10.5 13M13.5 11l1.7-1.7a2.8 2.8 0 0 0-4-4L9.5 7"/>',
  bolt: '<path d="M11 2 5 11h4l-1 7 6-9h-4z"/>',
  github: '<circle cx="10" cy="10" r="7"/><path d="M6.6 15.6v-2.9c0-.7.3-1.2.8-1.4-2.4-.3-3.3-1.6-3.3-3.3 0-.8.3-1.5.8-2-.1-.5-.1-1.2.1-1.8 0 0 .7 0 1.6.7a6.5 6.5 0 0 1 3 0c.9-.7 1.6-.7 1.6-.7.2.6.2 1.3.1 1.8.5.5.8 1.2.8 2 0 1.7-.9 3-3.3 3.3.4.2.7.7.7 1.4v2.8"/>',
  star: '<path d="m10 2.8 2.2 4.5 5 .7-3.6 3.5.9 5-4.5-2.4-4.5 2.4.9-5L2.8 8l5-.7z"/>',
  heart: '<path d="M10 17s-6.5-4-6.5-8.3A3.7 3.7 0 0 1 10 5.8a3.7 3.7 0 0 1 6.5 2.9C16.5 13 10 17 10 17Z"/>',
  message: '<path d="M3.5 4.5h13v9h-7l-4 3v-3h-2z"/>',
  download: '<path d="M10 3v10M6 9l4 4 4-4M4 16h12"/>',
  eye: '<path d="M2.5 10S5 5.5 10 5.5 17.5 10 17.5 10 15 14.5 10 14.5 2.5 10 2.5 10Z"/><circle cx="10" cy="10" r="2.2"/>',
  'eye-off': '<path d="M3.2 3.2 16.8 16.8"/><path d="M7.3 6.6A7.6 7.6 0 0 0 2.5 10S5 14.5 10 14.5c1 0 1.9-.2 2.7-.5"/><path d="M12.3 12.2A2.2 2.2 0 0 1 9 8.9"/><path d="M17.5 10a12.6 12.6 0 0 0-2.3-2.7"/>',
};

export function icon(name, size = 16, stroke = 1.5) {
  const paths = P[name];
  if (!paths) {
    // 以前是静默回落到「更多」：写错一个名字，界面上只少一个图标，没人会发现。
    console.warn('[icons] 未定义的图标：' + name + '（已回退为 more）');
  }
  return svg(paths || P.more, size, '0 0 20 20', stroke);
}

/* MCA 同源标记 + Aether Cockpit 的三节点网关徽章。 */
const BRAND = `
<path fill="#1A73E8" d="M54,20 C35.22,20 20,33.43 20,50 C20,59.39 24.87,67.76 32.22,73.19 L28,88 L43.55,83.33 C46.86,84.45 50.36,85 54,85 C72.78,85 88,71.57 88,55 C88,38.43 72.78,20 54,20 Z"/>
<path fill="#FFFFFF" d="M54,34 L57,47 L70,50 L57,53 L54,66 L51,53 L38,50 L51,47 Z"/>
<path fill="#CCFFFF" d="M36,34.5 A3.5,3.5 0,1 1,36,41.5 A3.5,3.5 0,1 1,36,34.5 Z"/>
<path fill="#CCFFFF" d="M72,58.5 A3.5,3.5 0,1 1,72,65.5 A3.5,3.5 0,1 1,72,58.5 Z"/>
<path fill="#80FFFF" d="M42,65.5 A2.5,2.5 0,1 1,42,70.5 A2.5,2.5 0,1 1,42,65.5 Z"/>
<path fill="#80FFFF" d="M66,34 A2,2 0,1 1,66,38 A2,2 0,1 1,66,34 Z"/>
<circle cx="77" cy="75" r="11" fill="#FFFFFF" stroke="#13A99B" stroke-width="1.7"/>
<path d="M72.5 75 H81.5" fill="none" stroke="#1A73E8" stroke-width="2.2" stroke-linecap="round"/>
<circle cx="72.5" cy="75" r="2.1" fill="#13A99B"/>
<circle cx="77" cy="75" r="2.5" fill="#1A73E8"/>
<circle cx="81.5" cy="75" r="2.1" fill="#13A99B"/>`;

export function brandMark(size = 26) {
  const s = svg(BRAND, size, '16 16 76 76', 0);
  s.removeAttribute('stroke');
  s.removeAttribute('fill');
  s.setAttribute('role', 'img');
  s.setAttribute('aria-label', 'Aether Cockpit');
  return s;
}
