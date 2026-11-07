// tests/unit/panel-roots-apply.test.mjs —— 根池「应用给猫」（2026-11-07）
// 覆盖：按钮位于「保存（重启生效）」左侧 · 弹层逐猫比对（一样 / 缺 / 读取失败）· 只写不一致者且载荷只带 cat+enabledRoots · 全一致时不出写入
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { expect, test, beforeAll } from 'vitest';
import { installDom, installMockEventSource, installMockFetch } from './mock-env.js';

// 加载无导出脚本到全局（vm.runInThisContext——var/function 声明挂全局）
async function runGlobalScript(url) {
  const code = await readFile(url, 'utf-8');
  vm.runInThisContext(code, { filename: url.toString() });
}

// 串行 promise 链刷新（openRootsApply 为 workspace → cats → 逐猫串行，层数随猫数增长）
async function flush(n) {
  let p = Promise.resolve();
  for (let i = 0; i < n; i++) { p = p.then(() => new Promise((resolve) => setTimeout(resolve, 0))); }
  return p;
}

let calls = [];

beforeAll(async () => {
  const html = await readFile(new URL('../../index.html', import.meta.url), 'utf-8');
  installDom(html);
  installMockEventSource();
  installMockFetch();
  await runGlobalScript(new URL('../../js/ui-common.js', import.meta.url));
  await runGlobalScript(new URL('../../js/app.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel-apis.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel-catcfg.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel-packs.js', import.meta.url));
  await flush(2);
});

test('根池区——「应用给猫」在左、「重启宿主」在右（同一按钮行，保存居中）', () => {
  const apply = document.getElementById('rootsApplyCats');
  const save = document.getElementById('rootsSave');
  const restart = document.getElementById('rootsRestart');
  expect(apply).not.toBeNull();
  expect(save).not.toBeNull();
  expect(restart).not.toBeNull();
  expect(apply.textContent.trim()).toBe('应用给猫');
  expect(restart.textContent.trim()).toBe('重启宿主');
  expect(save.parentElement).toBe(apply.parentElement);
  expect(save.parentElement).toBe(restart.parentElement);
  const kids = Array.from(save.parentElement.children);
  expect(kids.indexOf(apply)).toBe(kids.indexOf(save) - 1);
  expect(kids.indexOf(restart)).toBe(kids.indexOf(save) + 1);
});

test('重启按钮——确认后 POST /api/v1/host-restart（mode=host），取消则不发', async () => {
  const realConfirm = window.confirm;
  try {
    // 取消——不发请求
    window.confirm = () => false;
    calls = [];
    globalThis.fetch = async (url, opt) => { calls.push({ url: url, body: opt && opt.body ? JSON.parse(opt.body) : null }); return { json: async () => ({ ok: true }) }; };
    document.getElementById('rootsRestart').click();
    await flush(4);
    expect(calls.length).toBe(0);

    // 确认——发 mode=host 的登记请求
    window.confirm = () => true;
    calls = [];
    document.getElementById('rootsRestart').click();
    await flush(4);
    expect(calls.length).toBe(1);
    expect(calls[0].url).toBe('/api/v1/host-restart');
    expect(calls[0].body.mode).toBe('host');
    expect(document.getElementById('rootsMsg').textContent).toContain('已登记重启');
  } finally {
    window.confirm = realConfirm;
  }
});

test('应用给猫——比对三态（一样 / 缺 / 读取失败），只写不一致者', async () => {
  globalThis.fetch = async (url, opt) => {
    let body = null;
    if (opt && opt.body) { body = JSON.parse(opt.body); }
    calls.push({ url: url, body: body });
    if (url === '/api/v1/workspace') {
      return { json: async () => ({ ok: true, roots: [{ id: 'workspace' }, { id: 'ccbp' }, { id: 'mau' }] }) };
    }
    if (url === '/api/v1/cats') {
      return { json: async () => ({ cats: [{ id: 'a', name: '甲' }, { id: 'b', name: '乙' }, { id: 'c', name: '丙' }] }) };
    }
    if (url.indexOf('/api/v1/cat-config?cat=a') === 0) {
      // 与根池全域一致（顺序无关）
      return { json: async () => ({ ok: true, enabledRoots: ['ccbp', 'mau', 'workspace'] }) };
    }
    if (url.indexOf('/api/v1/cat-config?cat=b') === 0) {
      return { json: async () => ({ ok: true, enabledRoots: ['workspace'] }) };
    }
    if (url.indexOf('/api/v1/cat-config?cat=c') === 0) {
      return { json: async () => ({ ok: false, error: 'cat.cfg 不存在' }) };
    }
    return { json: async () => ({ ok: true }) };
  };

  calls = [];
  window.openRootsApply();
  await flush(30);

  expect(document.getElementById('rootsApplyModal').style.display).toBe('flex');
  const cbs = document.querySelectorAll('#rootsApplyList input.roots-apply-cb');
  expect(cbs.length).toBe(3);
  expect(cbs[0].disabled).toBe(true);    // 一样 → 禁用且不勾
  expect(cbs[0].checked).toBe(false);
  expect(cbs[1].checked).toBe(true);     // 缺根 → 默认勾选
  expect(cbs[1].disabled).toBe(false);
  expect(cbs[2].disabled).toBe(true);    // 配置读不到 → 禁用

  const list = document.getElementById('rootsApplyList').textContent;
  expect(list).toContain('· 一样');
  expect(list).toContain('缺 ccbp/mau');
  expect(list).toContain('配置读取失败（不可套用）');
  expect(document.getElementById('rootsApplyDesc').textContent).toContain('ccbp / mau / workspace');

  calls = [];
  window.applyRootsToCats();
  await flush(20);
  const posts = calls.filter((c) => c.url === '/api/v1/cat-config');
  expect(posts.length).toBe(1);
  expect(posts[0].body).toEqual({ cat: 'b', enabledRoots: ['ccbp', 'mau', 'workspace'] });
  expect(document.getElementById('rootsApplyMsg').textContent).toContain('应用完成——写入 1 只');
});

test('应用给猫——全集只有 workspace 且猫为空白名单时判「一样」，不出写入', async () => {
  globalThis.fetch = async (url) => {
    if (url === '/api/v1/workspace') {
      return { json: async () => ({ ok: true, roots: [{ id: 'workspace' }] }) };
    }
    if (url === '/api/v1/cats') {
      return { json: async () => ({ cats: [{ id: 'a', name: '甲' }] }) };
    }
    if (url.indexOf('/api/v1/cat-config?cat=a') === 0) {
      // 空 / null 按「仅常驻根」归一（与后端 ValidateEnabledRoots 同源）
      return { json: async () => ({ ok: true, enabledRoots: null }) };
    }
    return { json: async () => ({ ok: true }) };
  };
  window.openRootsApply();
  await flush(30);
  const cbs = document.querySelectorAll('#rootsApplyList input.roots-apply-cb');
  expect(cbs.length).toBe(1);
  expect(cbs[0].disabled).toBe(true);
  expect(document.getElementById('rootsApplyList').textContent).toContain('· 一样');
  expect(document.getElementById('rootsApplyMsg').textContent).toContain('全部猫已与根池一致');
});

test('应用给猫——弹层关闭按钮收起', () => {
  document.getElementById('rootsApplyCancel').click();
  expect(document.getElementById('rootsApplyModal').style.display).toBe('none');
});
