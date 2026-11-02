// tests/unit/panel-tpl-apply.test.mjs —— 新猫默认模板（四列四大类 + 保存后套用询问）（2026-10-01；2026-10-08 增默认目录白名单）
// 覆盖：四列结构（默认人设 / 默认工具 / 默认加载包和前文 / 默认目录白名单）· 「保存并应用」整行按钮在区标题下、四列上 · 底部横条=刷新+回执 · 子类可收起
//       · 模板面特权组整组隐藏（每猫面保持可见不可配）
//       · 保存后弹层：一致者自动勾选 / 不一致不勾 / 读取失败禁用 · 套用载荷（模板五字段 + 保留猫的 API/QQBot）
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { expect, test, beforeAll } from 'vitest';
import { installDom, installMockEventSource, installMockFetch } from './mock-env.js';

// 加载无导出脚本到全局（vm.runInThisContext——var/function 声明挂 globalThis）
async function runGlobalScript(url) {
  const code = await readFile(url, 'utf-8');
  vm.runInThisContext(code, { filename: url.toString() });
}

function tick() {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

let calls = [];

beforeAll(async () => {
  const html = await readFile(new URL('../../index.html', import.meta.url), 'utf-8');
  installDom(html);
  installMockEventSource();
  installMockFetch();
  // 样式入 DOM——收起态隐藏判据走真实级联（生产由 <link> 加载，测试环境需显式注入）
  const css = await readFile(new URL('../../css/style.css', import.meta.url), 'utf-8');
  const styleEl = document.createElement('style');
  styleEl.textContent = css;
  document.head.appendChild(styleEl);
  await runGlobalScript(new URL('../../js/ui-common.js', import.meta.url));
  await runGlobalScript(new URL('../../js/app.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel-apis.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel-catcfg.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel-packs.js', import.meta.url));
  await tick();
});

test('新猫模板——基础角色并入默认人设列；保存横条在网格之外', () => {
  const cols = document.querySelector('.tpl-cols');
  expect(cols).not.toBeNull();
  const subs = [];
  for (const el of cols.children) {
    if (el.classList.contains('cfg-sec')) { subs.push(el); }
  }
  expect(subs.length).toBe(4);
  const titles = [];
  const keys = [];
  for (const s of subs) {
    titles.push(s.querySelector('.cfg-sec-title').textContent.trim());
    keys.push(s.getAttribute('data-cfg'));
  }
  expect(titles).toEqual(['默认人设', '默认工具', '默认加载包和前文', '默认目录白名单']);
  expect(keys).toEqual(['tpl-persona', 'tpl-tools', 'tpl-packs', 'tpl-roots']);

  // 基础角色段并入默认人设列（与默认人设同列）——原 .tpl-base 容器退役
  expect(subs[0].contains(document.getElementById('tplBaseRole'))).toBe(true);
  expect(subs[0].contains(document.getElementById('tplPersona'))).toBe(true);
  expect(document.querySelector('.tpl-base')).toBeNull();

  // 四列内容归属
  expect(subs[1].contains(document.getElementById('tplTools'))).toBe(true);
  expect(subs[2].contains(document.getElementById('tplPacks'))).toBe(true);
  expect(subs[2].contains(document.getElementById('tplInject'))).toBe(true);
  expect(subs[3].contains(document.getElementById('tplRoots'))).toBe(true);

  // 保存并应用——整行按钮位于区标题之下、三列之上（2026-10-01 体验轮）
  const applyBar = document.querySelector('.tpl-applybar');
  expect(applyBar).not.toBeNull();
  expect(cols.contains(applyBar)).toBe(false);
  expect(applyBar.contains(document.getElementById('tplSave'))).toBe(true);
  expect(document.getElementById('tplSave').textContent.trim()).toBe('保存并应用');
  const sec = document.querySelector('.cfg-sec[data-cfg="tpl"]');
  const kids = Array.prototype.slice.call(sec.children);
  expect(kids.indexOf(applyBar)).toBeLessThan(kids.indexOf(cols));

  // 底部横条——保存按钮已上移，只留刷新 + 回执，且在三列之外
  const bar = document.querySelector('.tpl-savebar');
  expect(bar).not.toBeNull();
  expect(cols.contains(bar)).toBe(false);
  expect(bar.contains(document.getElementById('tplSave'))).toBe(false);
  expect(bar.contains(document.getElementById('tplRefresh'))).toBe(true);
  expect(bar.contains(document.getElementById('tplMsg'))).toBe(true);
});

test('模板面——特权组整组隐藏（组头与工具项都不出）；每猫面保持可见不可配', () => {
  const allTools = [
    { name: 'text-read', group: 'TextCat' },
    { name: 'config-get', group: 'ConfigCat', privileged: true },
    { name: 'majordomo-cmd', group: 'MajordomoCat', privileged: true }
  ];
  const box = document.getElementById('tplTools');

  // 模板面（hide）——特权组连组头一起不出，普通组照常
  window.renderGroupedChecks('tplTools', allTools, {}, 'hide');
  expect(box.textContent).toContain('text-read');
  expect(box.textContent).not.toContain('ConfigCat');
  expect(box.textContent).not.toContain('config-get');
  expect(box.textContent).not.toContain('majordomo-cmd');

  // 每猫面（off）——特权项可见不可配，保留说明性标记
  window.renderGroupedChecks('tplTools', allTools, {}, 'off');
  expect(box.textContent).toContain('ConfigCat');
  expect(box.textContent).toContain('config-get · 特权面');
});

test('新猫模板——网格版式契约（左列人设 + 包 1fr / 右列工具 2fr 跨两行 / 第三行白名单整行）', async () => {
  // jsdom 不解析 grid 版式——判据落在样式文件契约（生产由 <link> 加载同一份）
  const css = await readFile(new URL('../../css/style.css', import.meta.url), 'utf-8');
  expect(css).toContain('grid-template-columns: minmax(0, 1fr) minmax(0, 2fr)');
  expect(css).toContain('.tpl-cols > [data-cfg="tpl-persona"] { grid-column: 1; grid-row: 1; }');
  expect(css).toContain('.tpl-cols > [data-cfg="tpl-packs"] { grid-column: 1; grid-row: 2; }');
  expect(css).toContain('.tpl-cols > [data-cfg="tpl-tools"] { grid-column: 2; grid-row: 1 / span 2; }');
  expect(css).toContain('.tpl-cols > [data-cfg="tpl-roots"] { grid-column: 1 / -1; grid-row: 3; }');
});

test('三大类各自可收起——复用配置区大项机制（类 + 箭头 + 正文隐藏）', () => {
  const view = document.defaultView;
  // 父级展开——子类在可见态下验证（jsdom 对非渲染子树一律回初值，不解析作者样式）
  const parent = document.querySelector('.cfg-sec[data-cfg="tpl"]');
  if (parent.classList.contains('collapsed')) { parent.querySelector('.cfg-sec-head').click(); }
  expect(parent.classList.contains('collapsed')).toBe(false);
  const sub = document.querySelector('.tpl-cols > .cfg-sec');
  const head = sub.querySelector('.cfg-sec-head');
  const mark = sub.querySelector('.cfg-sec-mark');
  const textarea = sub.querySelector('textarea');
  const label = sub.querySelector('label');
  expect(sub.classList.contains('collapsed')).toBe(false);
  expect(view.getComputedStyle(textarea).display).not.toBe('none');
  head.click();
  expect(sub.classList.contains('collapsed')).toBe(true);
  expect(mark.textContent).toBe('▸');
  expect({ label: view.getComputedStyle(label).display, textarea: view.getComputedStyle(textarea).display })
    .toEqual({ label: 'none', textarea: 'none' });
  head.click();
  expect(sub.classList.contains('collapsed')).toBe(false);
  expect(mark.textContent).toBe('▾');
  expect(view.getComputedStyle(textarea).display).not.toBe('none');
});

test('一致性判据——五项全同才算一致（工具名 / 加载包 / 启用根按集合比，前文 List 按序列比）', () => {
  document.getElementById('tplPersona').value = 'P1';
  window.renderGroupedChecks('tplTools', ['text-read', 'cs-build'], { 'text-read': true, 'cs-build': true }, 'off');
  window.renderTplPacks([{ key: 'overwork', desc: '收工加载包' }], ['overwork']);
  window.tplInjectList = ['ccbp:L1/Tree.md'];
  window.renderRootChecks('tplRoots', [
    { id: 'workspace', path: 'D:\\ws', writable: true, fixedRoot: true },
    { id: 'ccbp', path: 'D:\\ccbp', writable: true }
  ], ['workspace'], '');

  // 工具名顺序不同 → 集合相同 → 一致
  expect(window.isCatMatchingTpl({
    persona: 'P1', toolNames: 'cs-build,text-read', packs: ['overwork'], injectList: ['ccbp:L1/Tree.md'], enabledRoots: ['workspace']
  })).toBe(true);

  // 启用根多一根 → 不一致；未配置（空）与「仅常驻根」等价 → 一致
  expect(window.isCatMatchingTpl({
    persona: 'P1', toolNames: 'text-read,cs-build', packs: ['overwork'], injectList: ['ccbp:L1/Tree.md'], enabledRoots: ['workspace', 'ccbp']
  })).toBe(false);
  expect(window.isCatMatchingTpl({
    persona: 'P1', toolNames: 'text-read,cs-build', packs: ['overwork'], injectList: ['ccbp:L1/Tree.md'], enabledRoots: []
  })).toBe(true);

  // 前文 List 顺序不同 → 不一致
  expect(window.isCatMatchingTpl({
    persona: 'P1', toolNames: 'text-read,cs-build', packs: ['overwork'], injectList: ['ccbp:L2/Job/CSharp', 'ccbp:L1/Tree.md']
  })).toBe(false);

  // 人设不同 → 不一致
  expect(window.isCatMatchingTpl({
    persona: '别的', toolNames: 'text-read,cs-build', packs: ['overwork'], injectList: ['ccbp:L1/Tree.md']
  })).toBe(false);

  // 加载包不同 → 不一致
  expect(window.isCatMatchingTpl({
    persona: 'P1', toolNames: 'text-read,cs-build', packs: [], injectList: ['ccbp:L1/Tree.md']
  })).toBe(false);
});

test('套用弹层——一致者自动勾选 / 不一致不勾 / 读取失败禁用', async () => {
  globalThis.fetch = async (url) => {
    if (url === '/api/v1/cats') {
      return { json: async () => ({ cats: [{ id: 'a', name: '甲' }, { id: 'b', name: '乙' }, { id: 'c', name: '丙' }] }) };
    }
    if (url.indexOf('/api/v1/cat-config?cat=a') === 0) {
      return { json: async () => ({ ok: true, persona: 'P1', toolNames: 'cs-build,text-read', packs: ['overwork'], injectList: ['ccbp:L1/Tree.md'], qqbotEnable: true, enabledRoots: ['workspace'], qqbotId: '' }) };
    }
    if (url.indexOf('/api/v1/cat-config?cat=b') === 0) {
      return { json: async () => ({ ok: true, persona: '别的', toolNames: '', packs: [], injectList: [], qqbotEnable: false, enabledRoots: ['workspace'], qqbotId: '' }) };
    }
    return { json: async () => ({ ok: false, error: 'cat.cfg 不存在' }) };
  };
  window.openTplApply();
  await tick();
  await tick();
  expect(document.getElementById('tplApplyModal').style.display).toBe('flex');
  const cbs = document.querySelectorAll('#tplApplyList input.tpl-apply-cb');
  expect(cbs.length).toBe(3);
  expect(cbs[0].checked).toBe(true);
  expect(cbs[1].checked).toBe(false);
  expect(cbs[2].disabled).toBe(true);
  expect(cbs[2].checked).toBe(false);
  expect(document.getElementById('tplApplyList').textContent).toContain('与模板一致');
  expect(document.getElementById('tplApplyList').textContent).toContain('配置读取失败（不可套用）');
});

test('一键套用——载荷含模板五字段（含目录白名单）；不带 apiConfigId', async () => {
  // 模板端启用根显式渲染——workspace 系统根恒勾选（套用载荷取模板值，不取该猫现值）
  window.renderRootChecks('tplRoots', [
    { id: 'workspace', path: 'D:\\ws', writable: true, fixedRoot: true },
    { id: 'ccbp', path: 'D:\\ccbp', writable: true }
  ], ['workspace'], '');
  calls = [];
  globalThis.fetch = async (url, opt) => {
    let body = null;
    if (opt && opt.body) { body = JSON.parse(opt.body); }
    calls.push({ url: url, body: body });
    return { json: async () => ({ ok: true }) };
  };
  window.applyTplToCats();
  await tick();
  const posts = calls.filter((c) => c.url === '/api/v1/cat-config');
  expect(posts.length).toBe(1);
  expect(posts[0].body).toEqual({
    cat: 'a',
    persona: 'P1',
    toolNames: 'text-read,cs-build',
    packs: ['overwork'],
    injectList: ['ccbp:L1/Tree.md'],
    enabledRoots: ['workspace']
  });
  expect(posts[0].body.apiConfigId).toBeUndefined();
  expect(document.getElementById('tplApplyMsg').textContent).toContain('套用完成——成功 1 只');
});

test('套用弹层——关闭按钮收起弹层', () => {
  document.getElementById('tplApplyCancel').click();
  expect(document.getElementById('tplApplyModal').style.display).toBe('none');
});
