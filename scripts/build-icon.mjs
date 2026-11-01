import { chromium } from 'playwright';
import fs from 'node:fs/promises';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const svgPath = path.join(root, 'internal/web/ui2/assets/aether-cockpit.svg');
const svg = await fs.readFile(svgPath, 'utf8');
const browserCandidates = [
  process.env.CHROME_PATH,
  'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
  'C:/Program Files/Microsoft/Edge/Application/msedge.exe',
  'C:/Program Files/Google/Chrome/Application/chrome.exe',
];
const executablePath = browserCandidates.find((candidate) => candidate && existsSync(candidate));
const browser = await chromium.launch(executablePath ? { executablePath } : {});

try {
  const page = await browser.newPage({ viewport: { width: 1024, height: 1024 } });
  const frames = [];
  for (const size of [16, 24, 32, 48, 64, 128, 256, 1024]) {
    await page.setViewportSize({ width: size, height: size });
    await page.setContent(`<style>html,body{margin:0;width:100%;height:100%;overflow:hidden}svg{display:block;width:100vw;height:100vh}</style>${svg}`);
    const png = await page.screenshot({ omitBackground: true });
    if (size === 1024) {
      await fs.writeFile(path.join(root, 'build/appicon.png'), png);
    } else {
      frames.push({ size, png });
    }
  }

  const headerSize = 6 + frames.length * 16;
  const header = Buffer.alloc(headerSize);
  header.writeUInt16LE(0, 0);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(frames.length, 4);
  let offset = headerSize;
  for (const [index, frame] of frames.entries()) {
    const entry = 6 + index * 16;
    header.writeUInt8(frame.size === 256 ? 0 : frame.size, entry);
    header.writeUInt8(frame.size === 256 ? 0 : frame.size, entry + 1);
    header.writeUInt8(0, entry + 2);
    header.writeUInt8(0, entry + 3);
    header.writeUInt16LE(1, entry + 4);
    header.writeUInt16LE(32, entry + 6);
    header.writeUInt32LE(frame.png.length, entry + 8);
    header.writeUInt32LE(offset, entry + 12);
    offset += frame.png.length;
  }
  const ico = Buffer.concat([header, ...frames.map((frame) => frame.png)]);
  await fs.writeFile(path.join(root, 'build/windows/icon.ico'), ico);
  await fs.writeFile(path.join(root, 'internal/desktop/tray.ico'), ico);
} finally {
  await browser.close();
}

console.log('Built appicon.png, Windows icon.ico, and tray.ico from aether-cockpit.svg.');
