// tests/unit/panel-config-sections.test.mjs —— 配置区大项收起 / 展开（2026-10-01 易用性轮）
// 覆盖：侧栏顺序与默认页签 · 六大项顺序与 data-cfg 键 · 默认态（首两项展开 / 后四项收起）· 记录优先于默认态
//       · 头一行含标题 + 说明 · 点击切换 collapsed 与箭头 · 收起态正文全隐藏（含带 inline display 的行）· 记录读写
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { expect, test, beforeAll } from 'vitest';
import { installDom, installMockEventSource, installMockFetch } from './mock-env.js';

// 加载无导出脚本到全局（vm.runInThisContext——var/function 声明挂 globalThis）
async function runGlobalScript(url) {
  const code = await readFile(url, 'utf-8');
  vm.runInThisContext(code, { filename: url.toString() });
}

beforeAll(async () => {
  const html = await readFile(new URL('../../index.html', import.meta.url), 'utf-8');
  installDom(html);
  installMockEventSource();
  installMockFetch();
  // 预置记录：restart 页面默认展开，记录说收起——用来验证「记录优先于默认态」
  localStorage.setItem('cfg-sec:restart', '1');
  // 样式入 DOM——收起态隐藏判据走真实级联（生产由 <link> 加载，测试环境需显式注入）
  const css = await readFile(new URL('../../css/style.css', import.meta.url), 'utf-8');
  const styleEl = document.createElement('style');
  styleEl.textContent = css;
  document.head.appendChild(styleEl);
  // 主面板脚本全量加载（段13 收起/展开接线在 app.js）
  await runGlobalScript(new URL('../../js/ui-common.js', import.meta.url));
  await runGlobalScript(new URL('../../js/app.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel-apis.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel-catcfg.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel-packs.js', import.meta.url));
});

test('侧栏顺序——配置居首且为默认页签', () => {
  // 首项为「当前用户」（用户态项，无 data-tab）——页签序列从第二项起（2026-10-01 用户态轮）
  const all = document.querySelectorAll('#sidebar button');
  expect(all[0].id).toBe('sidebarUser');
  const btns = document.querySelectorAll('#sidebar button[data-tab]');
  expect(btns[0].getAttribute('data-tab')).toBe('config');
  expect(btns[1].getAttribute('data-tab')).toBe('cats');
  expect(btns[0].classList.contains('active')).toBe(true);
  expect(document.getElementById('tab-config').classList.contains('active')).toBe(true);
  expect(document.getElementById('tab-cats').classList.contains('active')).toBe(false);
});

test('配置区六大项——顺序 + data-cfg 键（A299 QQ Bot 池撤除）', () => {
  const secs = document.querySelectorAll('#tab-config > div > .cfg-sec');
  expect(secs.length).toBe(6);
  const titles = [];
  const keys = [];
  for (const sec of secs) {
    titles.push(sec.querySelector('.cfg-sec-title').textContent.trim());
    keys.push(sec.getAttribute('data-cfg'));
  }
  expect(titles).toEqual(['🔁 重启生效', 'LLM API 池', '新猫默认模板', '加载包池', '外观池', '⚡ 立即生效']);
  expect(keys).toEqual(['restart', 'apis', 'tpl', 'packs', 'appearance', 'config']);
});

test('落态总览——apis 默认展开 / 后四项默认收起 / restart 按记录收起', () => {
  const states = [];
  const marks = [];
  for (const sec of document.querySelectorAll('#tab-config > div > .cfg-sec')) {
    states.push(sec.classList.contains('collapsed'));
    marks.push(sec.querySelector('.cfg-sec-mark').textContent);
  }
  // restart 有记录（预置 '1'）——按记录收起；其余按页面默认（apis 展开 / 后四项收起）
  expect(states).toEqual([true, false, true, true, true, true]);
  expect(marks).toEqual(['▸', '▾', '▸', '▸', '▸', '▸']);
});

test('记录优先于默认态——restart 页面默认展开，记录说收起 → 落收起', () => {
  const sec = document.querySelector('#tab-config .cfg-sec[data-cfg="restart"]');
  expect(sec.classList.contains('collapsed')).toBe(true);
  expect(sec.querySelector('.cfg-sec-mark').textContent).toBe('▸');
});

test('每个大项的头是首个子元素——收起态只留这一行', () => {
  for (const sec of document.querySelectorAll('#tab-config > div > .cfg-sec')) {
    const head = sec.querySelector('.cfg-sec-head');
    expect(head).not.toBeNull();
    expect(head.parentElement).toBe(sec);
    expect(sec.children[0]).toBe(head);
    expect(sec.querySelector('.cfg-sec-title').textContent.trim().length).toBeGreaterThan(0);
    expect(sec.querySelector('.cfg-sec-desc').textContent.trim().length).toBeGreaterThan(0);
  }
});

test('点击头部——collapsed 切换 + 箭头翻转 + 正文全隐藏（含带 inline display 的行）', () => {
  const view = document.defaultView;
  for (const sec of document.querySelectorAll('#tab-config > div > .cfg-sec')) {
    const head = sec.querySelector('.cfg-sec-head');
    const mark = sec.querySelector('.cfg-sec-mark');
    const body = [];
    for (const child of sec.children) {
      if (!child.classList.contains('cfg-sec-head')) { body.push(child); }
    }
    expect(body.length).toBeGreaterThan(0);
    const wasCollapsed = sec.classList.contains('collapsed');
    const show = (visible) => {
      for (const el of body) {
        if (visible) { expect(view.getComputedStyle(el).display).not.toBe('none'); }
        else { expect(view.getComputedStyle(el).display).toBe('none'); }
      }
    };
    show(!wasCollapsed);
    head.click();
    expect(sec.classList.contains('collapsed')).toBe(!wasCollapsed);
    expect(mark.textContent).toBe(wasCollapsed ? '▾' : '▸');
    show(wasCollapsed);
    head.click();
    expect(sec.classList.contains('collapsed')).toBe(wasCollapsed);
    expect(mark.textContent).toBe(wasCollapsed ? '▸' : '▾');
    show(!wasCollapsed);
  }
});

test('展开态记录——点击写入 + 读取三态', () => {
  const sec = document.querySelector('#tab-config .cfg-sec[data-cfg="apis"]');
  const head = sec.querySelector('.cfg-sec-head');
  const before = sec.classList.contains('collapsed');
  head.click();
  expect(sec.classList.contains('collapsed')).toBe(!before);
  expect(localStorage.getItem('cfg-sec:apis')).toBe(!before ? '1' : '0');
  head.click();
  expect(sec.classList.contains('collapsed')).toBe(before);
  expect(localStorage.getItem('cfg-sec:apis')).toBe(before ? '1' : '0');
  localStorage.setItem('cfg-sec:probe', '1');
  expect(window.cfgSecLoad('probe')).toBe(true);
  localStorage.setItem('cfg-sec:probe', '0');
  expect(window.cfgSecLoad('probe')).toBe(false);
  localStorage.setItem('cfg-sec:probe', 'x');
  expect(window.cfgSecLoad('probe')).toBeNull();
  expect(window.cfgSecLoad('')).toBeNull();
});
