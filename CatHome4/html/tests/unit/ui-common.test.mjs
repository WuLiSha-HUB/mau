// ═══════════════════════════════════════════
// tests/unit/ui-common.test.mjs —— A377：端点 host 段（ui.host 全局配置 + 页面 host 回落）
//
// 定位：打开对话页 / 群聊页的端点由 `uiEndpoint(port)` 拼装（原为硬编码 127.0.0.1）。
//       判据三条——① 未配置回落当前页面 host（本地 / 远程 tailscale 各自成立）
//                  ② ui.host 配置生效（远程访问填机器地址）
//                  ③ 配置项被清除后回到页面 host（A364 清空即删除落盘值，响应里不再有该键）
// ═══════════════════════════════════════════

import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { expect, test, beforeAll } from 'vitest';
import { installDom } from './mock-env.js';

// 加载无导出脚本到全局（vm.runInThisContext——var/function 声明挂 globalThis）
async function runGlobalScript(url) {
    const code = await readFile(url, 'utf-8');
    vm.runInThisContext(code, { filename: url.toString() });
}

beforeAll(async () => {
    const dom = installDom('<html><body></body></html>', 'http://panel.local:8080');
    // 页面 host 回落的依赖面——jsdom 的 location 挂到 globalThis（installDom 只挂 document / navigator）
    Object.defineProperty(globalThis, 'location', { value: dom.window.location, configurable: true });
    await runGlobalScript(new URL('../../js/ui-common.js', import.meta.url));
});

test('端点 host——未配置时回落当前页面 host', () => {
    window.applyUiConfig([]);
    expect(window.uiEndpoint(9000)).toBe('http://panel.local:9000/');
});

test('端点 host——ui.host 配置生效（含首尾空白裁剪）', () => {
    window.applyUiConfig([{ key: 'ui.host', value: '  cat.example  ' }]);
    expect(window.uiEndpoint(9000)).toBe('http://cat.example:9000/');
});

test('端点 host——配置项被清除后回到页面 host', () => {
    window.applyUiConfig([{ key: 'ui.host', value: 'cat.example' }]);
    window.applyUiConfig([{ key: 'ui.font_scale', value: '120' }]);
    expect(window.uiEndpoint(9001)).toBe('http://panel.local:9001/');
});
