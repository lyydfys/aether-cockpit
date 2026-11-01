/* 渠道品牌图标：simple-icons（CC0）随前端打包，缺图的渠道回退为品牌色字母块。 */
import { h } from './dom.js';

const ICONS = {
  'claude-sub': 'claude.svg',
  'openai': 'openai.svg',
  'openai-compat': 'openai.svg',
  'codex': 'openai.svg',
  'xai': 'xai.svg',
  'google': 'gemini.svg',
  'gemini-compat': 'gemini.svg',
  'vertex': 'googlecloud.svg',
  'deepseek': 'deepseek.svg',
  'moonshot': 'kimi.svg',
  'moonshot-cn': 'kimi.svg',
  'kimi-code': 'kimi.svg',
  'mistral': 'mistral.svg',
  'cline': 'cline.svg',
  'volcengine': 'volcengine.svg',
  'openrouter': 'openrouter.svg',
  'ollama': 'ollama.svg',
  'ollama-cloud': 'ollama.svg',
  'qwen': 'qwen.svg',
  'qwen-cn': 'qwen.svg',
  'qwen-token-plan': 'qwen.svg',
  'bailian-token-plan': 'alibaba.svg',
  'baidu-qianfan': 'baidu.svg',
  'minimax': 'minimax.svg',
  'minimax-cn': 'minimax.svg',
  'minimax-code': 'minimax.svg',
  'minimax-code-global': 'minimax.svg',
  'opencode-go': 'opencode.svg',
  'xiaomi': 'xiaomi.svg',
  'github-copilot': 'githubcopilot.svg',
  'cursor': 'cursor.svg',
  'windsurf': 'windsurf.svg',
  'zed': 'zed.svg',
  'trae-cn': 'trae.svg',
  'trae-intl': 'trae.svg',
  'trae-solo-cn': 'trae.svg',
  'trae-solo': 'trae.svg',
  'codebuddy': 'codebuddy.svg',
  'codebuddy-intl': 'codebuddy.svg',
  'codebuddy-cn': 'codebuddy.svg',
  'opencode': 'opencode.svg',
  'lmstudio': 'lmstudio.svg',
  'vllm': 'vllm.svg',
  'typesafe': 'typesafe.png',
  'commandcode': 'commandcode.png',
  'groq': 'groq.svg',
  'zhipu': 'zai.svg',
  'zai': 'zai.svg',
  'zcode': 'zai.svg',
  'zcode-cn': 'zai.svg',
  'tencent-cloud': 'tencentcloud.svg',
  'bedrock': 'bedrock.svg',
  'azure': 'azure.svg',
  'antigravity': 'antigravity.svg',
  'antigravity-ide': 'antigravity.svg',
  'qoder-cn': 'qoder.svg',
  'qoder-intl': 'qoder.svg',
  'kiro': 'kiro.svg',
  'workbuddy-cn': 'workbuddy.svg',
  'workbuddy-intl': 'workbuddy.svg',
  'siliconflow': 'siliconflow.svg',
  'stepfun': 'stepfun.svg',
  'devin': 'devin.svg',
};

/* 单色品牌标（纯黑/currentColor）在暗色下会整块消失，需要反相；
   彩色标（Gemini 紫、MiniMax 红…）不受影响。有白底 plate 的地方不反相。 */
const MONO = new Set([
  'claude.svg', 'cursor.svg', 'devin.svg', 'githubcopilot.svg', 'kimi.svg', 'lmstudio.svg',
  'moonshot.svg', 'ollama.svg', 'openai.svg', 'opencode.svg', 'siliconflow.svg',
  'stepfun.svg', 'tencentcloud.svg', 'xai.svg',
]);

export function brandIcon(provider, size = 18) {
  const id = typeof provider === 'string' ? provider : (provider && (provider.id || provider.provider_id));
  if (!id) return null;
  const custom = typeof provider === 'object' && provider && typeof provider.icon === 'string' ? provider.icon : '';
  if (custom && custom.startsWith('file:')) {
    return h('img', { class: 'brand-img', src: '/api/icons/' + custom.slice(5), alt: '', width: size, height: size });
  }
  const file = ICONS[id];
  if (!file) return null;
  return h('img', {
    class: 'brand-img' + (MONO.has(file) ? ' brand-img--mono' : ''),
    src: '/assets/icons/' + file, alt: '', width: size, height: size,
  });
}

// brandTile 交付整块渠道图标：有品牌图就用白底图标，没有则用品牌色字母块。
export function brandTile(provider, size = 30) {
  const img = brandIcon(provider, Math.round(size * 0.66));
  const tile = h('span', {
    class: 'brand-tile',
    style: 'width:' + size + 'px;height:' + size + 'px;box-shadow:none;' +
      (img ? 'background:#fff;box-shadow:inset 0 0 0 .5px rgba(60,64,67,.18)' : 'background:' + ((provider && provider.brand_color) || 'var(--surface-2)')),
  });
  if (img) tile.append(img);
  else tile.append(h('span', { style: 'color:#fff;font-weight:700;font-size:' + Math.round(size * 0.4) + 'px' }, String((provider && (provider.display_name || provider.id)) || '?').slice(0, 1)));
  return tile;
}
