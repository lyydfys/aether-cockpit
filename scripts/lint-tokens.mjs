#!/usr/bin/env node
/* 令牌校验：色值只允许出现在 css/tokens.css。
   其它 CSS 里出现十六进制颜色即失败（rgba 阴影与透明层不算色值，允许）。
   用法：node scripts/lint-tokens.mjs
*/
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'internal', 'web', 'ui2', 'css');
const ALLOW = new Set(['tokens.css']);
const HEX = /#[0-9a-fA-F]{3,8}\b/g;

const files = fs.readdirSync(ROOT).filter((f) => f.endsWith('.css'));
const findings = [];

for (const file of files) {
  if (ALLOW.has(file)) continue;
  const text = fs.readFileSync(path.join(ROOT, file), 'utf8');
  text.split('\n').forEach((line, i) => {
    if (line.includes('lint-allow-hex')) return;
    const hits = line.match(HEX);
    if (hits) findings.push({ file, line: i + 1, hits: hits.join(' '), text: line.trim().slice(0, 90) });
  });
}

if (!findings.length) {
  console.log(`令牌校验通过：${files.length - ALLOW.size} 个样式文件未出现十六进制色值。`);
  process.exit(0);
}

console.error(`令牌校验失败：${findings.length} 处色值写死在 tokens.css 之外`);
for (const f of findings) console.error(`  ${f.file}:${f.line}  ${f.hits}  ${f.text}`);
console.error('\n改法：把色值加进 tokens.css 的 :root（含暗色覆盖），这里改用 var(--…)。');
process.exit(1);
