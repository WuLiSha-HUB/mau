// tests/unit/panel-channel.test.mjs —— HTTP 注入通道池管理面（A405 2026-11-24）
// 覆盖：列表渲染（掩码 / 绑定猫 / 未配置）· 新建载荷与明文 Key 一次性弹层 · 编辑载荷 · 重置 Key · 关闭弹层清空明文
// 基座：与 panel-packs.test.mjs 同构（index.html DOM + 主面板脚本全量加载）
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { expect, test, beforeAll } from 'vitest';
import { installDom, installMockEventSource, installMockFetch } from './mock-env.js';

// 加载无导出脚本到全局（vm.runInThisContext——var/function 声明挂 globalThis）
async function runGlobalScript(url) {
  const code = await readFile(url, 'utf-8');
  vm.runInThisContext(code, { filename: url.toString() });
}

// 等一轮微任务——fetch 链上 .then 落地
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

beforeAll(async () => {
  const html = await readFile(new URL('../../index.html', import.meta.url), 'utf-8');
  installDom(html);
  installMockEventSource();
  installMockFetch();
  await runGlobalScript(new URL('../../js/ui-common.js', import.meta.url));
  await runGlobalScript(new URL('../../js/app.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel-channel.js', import.meta.url));
});

test('列表渲染——通道名 / 绑定猫 / Key 掩码 / 未配置态', async () => {
  globalThis.fetch = async () => ({
    json: async () => ({
      ok: true,
      items: [
        { channelId: 'aaaaaaaa-1111', displayName: 'ww-seat1', catKey: 'c1', catName: '雾理莎', key: 'ch_ab****', hasKey: true },
        { channelId: 'bbbbbbbb-2222', displayName: 'ww-seat2', catKey: 'c2', catName: '', key: '', hasKey: false }
      ]
    })
  });
  window.loadChannels();
  await flush();
  const rows = document.querySelectorAll('#chnTable tbody > tr');
  expect(rows.length).toBe(2);
  expect(rows[0].textContent).toContain('ww-seat1');
  expect(rows[0].textContent).toContain('雾理莎');
  expect(rows[0].textContent).toContain('ch_ab****');
  expect(rows[1].textContent).toContain('（未配置）');
});

test('绑定猫下拉——取 /api/v1/cats（key + 显示名）', async () => {
  globalThis.fetch = async () => ({
    json: async () => ({ cats: [{ id: 'c1', name: '雾理莎' }, { id: 'c2', name: 'Coder' }] })
  });
  window.loadChannelCats();
  await flush();
  const options = document.querySelectorAll('#chnNewCat option');
  expect(options.length).toBe(2);
  expect(options[0].value).toBe('c1');
  expect(options[0].textContent).toContain('雾理莎');
});

test('新建——载荷 displayName/catKey 且回执 Key 进一次性弹层', async () => {
  let captured = null;
  let capturedUrl = '';
  globalThis.fetch = async (url, opt) => {
    capturedUrl = String(url);
    if (opt && opt.body) { captured = JSON.parse(opt.body); }
    return { json: async () => ({ ok: true, channelId: 'cccccccc-3333', key: 'ch_plain_once' }) };
  };
  document.getElementById('chnNewName').value = 'ww-seat3';
  document.getElementById('chnNewCat').value = 'c1';
  window.channelSubmit();
  await flush();
  expect(capturedUrl).toBe('/api/v1/channels');
  expect(captured).toEqual({ displayName: 'ww-seat3', catKey: 'c1' });
  expect(document.getElementById('chnKeyModal').style.display).toBe('flex');
  expect(document.getElementById('chnKeyValue').value).toBe('ch_plain_once');
  expect(document.getElementById('chnKeyTitle').textContent).toContain('ww-seat3');
});

test('关闭弹层——明文清空且遮罩隐藏', async () => {
  document.getElementById('chnKeyValue').value = 'ch_plain_once';
  window.closeChannelKey();
  expect(document.getElementById('chnKeyValue').value).toBe('');
  expect(document.getElementById('chnKeyModal').style.display).toBe('none');
});

test('编辑——载荷走 /edit 且带 channelId（Key 不变）', async () => {
  const calls = [];
  globalThis.fetch = async (url, opt) => {
    calls.push({ url: String(url), body: opt && opt.body ? JSON.parse(opt.body) : null });
    return { json: async () => ({ ok: true }) };
  };
  window.startChannelEdit({ channelId: 'dddddddd-4444', displayName: 'ww-seat3', catKey: 'c1' });
  expect(document.getElementById('chnAddBtn').textContent).toBe('保存');
  document.getElementById('chnNewCat').value = 'c2';
  window.channelSubmit();
  await flush();
  // 首个调用是提交本身，其后是成功后的列表刷新（loadChannels）
  expect(calls[0].url).toBe('/api/v1/channels/edit');
  expect(calls[0].body).toEqual({ displayName: 'ww-seat3', catKey: 'c2', channelId: 'dddddddd-4444' });
  // 编辑成功不弹 Key 弹层（Key 不变）
  expect(document.getElementById('chnKeyModal').style.display).toBe('none');
});

test('重置 Key——载荷走 /rotate 且新 Key 进弹层（旧 Key 由后端作废）', async () => {
  const calls = [];
  globalThis.fetch = async (url, opt) => {
    calls.push({ url: String(url), body: opt && opt.body ? JSON.parse(opt.body) : null });
    return { json: async () => ({ ok: true, key: 'ch_rotated' }) };
  };
  globalThis.confirm = () => true;
  window.rotateChannelKey({ channelId: 'eeeeeeee-5555', displayName: 'ww-seat1' });
  await flush();
  expect(calls[0].url).toBe('/api/v1/channels/rotate');
  expect(calls[0].body).toEqual({ channelId: 'eeeeeeee-5555' });
  expect(document.getElementById('chnKeyValue').value).toBe('ch_rotated');
  expect(document.getElementById('chnKeyTitle').textContent).toContain('重置');
});
