// tests/unit/panel-token.test.mjs —— Token 用量统计区（A390）
// 覆盖：合计条渲染 · 每猫卡片与按日明细 · 空猫占位 · 结构签名（同数据零重建 / 数值变化重建）· 信息行（坏行出声）
// 基座：与 panel-dblibs.test.mjs 同构（index.html DOM + 主面板脚本加载）
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
  await runGlobalScript(new URL('../../js/ui-common.js', import.meta.url));
  await runGlobalScript(new URL('../../js/app.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel-token.js', import.meta.url));
});

/** 一份两猫快照——甲猫带一天明细，乙猫无记录，另带一条坏行 */
function sample(badLines, promptA) {
  return {
    ok: true,
    refreshedAt: 1794914288289,
    scanMs: 1,
    badLines: badLines,
    totals: { requests: 6, prompt: promptA + 234, completion: 56, cacheHit: 1000, miss: 234 },
    cats: [
      {
        catKey: 'c1',
        displayName: '甲猫',
        requests: 4,
        prompt: promptA,
        completion: 40,
        cacheHit: 900,
        miss: 100,
        days: [{ day: '20261117', requests: 4, prompt: promptA, completion: 40, cacheHit: 900, miss: 100, lastTs: 1794914282881 }]
      },
      { catKey: 'c2', displayName: '乙猫', requests: 2, prompt: 234, completion: 16, cacheHit: 100, miss: 134, days: [] }
    ]
  };
}

/** 页签进入态——确保 section 存在（index.html 骨架断言） */
test('index.html 含用量页签与统计区骨架', () => {
  expect(document.querySelector('#sidebar button[data-tab="token"]')).toBeTruthy();
  expect(document.getElementById('tab-token')).toBeTruthy();
  expect(document.getElementById('tokenRefresh')).toBeTruthy();
  expect(document.getElementById('tokenCats')).toBeTruthy();
});

test('合计条与每猫卡片——数值千分位、按日明细、空猫占位', () => {
  window.renderTokenStats(sample(1, 1000));
  const totals = document.getElementById('tokenTotals').textContent;
  expect(totals).toContain('请求数');
  expect(totals).toContain('1,234');
  expect(totals).toContain('1,000');
  const catsEl = document.getElementById('tokenCats');
  expect(catsEl.children.length).toBe(2);
  expect(catsEl.children[0].textContent).toContain('甲猫');
  expect(catsEl.children[0].textContent).toContain('c1');
  expect(catsEl.children[0].textContent).toContain('20261117');
  expect(catsEl.children[1].textContent).toContain('乙猫');
  expect(catsEl.children[1].textContent).toContain('暂无用量记录');
  const info = document.getElementById('tokenInfo').textContent;
  expect(info).toContain('2 只猫');
  expect(info).toContain('坏行 1 条');
});

test('结构签名——同数据二次渲染不重建节点（稳态零 DOM 变动）', () => {
  window.renderTokenStats(sample(0, 1000));
  const firstCard = document.getElementById('tokenCats').children[0];
  window.renderTokenStats(sample(0, 1000));
  expect(document.getElementById('tokenCats').children[0]).toBe(firstCard);
});

test('数值变化——重建节点（新数据生效）', () => {
  window.renderTokenStats(sample(0, 1000));
  const firstCard = document.getElementById('tokenCats').children[0];
  window.renderTokenStats(sample(0, 2000));
  expect(document.getElementById('tokenCats').children[0]).not.toBe(firstCard);
  expect(document.getElementById('tokenTotals').textContent).toContain('2,234');
});

test('空快照——给出「暂无任何猫的用量记录」占位', () => {
  window.renderTokenStats({ ok: true, refreshedAt: 1, scanMs: 0, badLines: 0, totals: { requests: 0, prompt: 0, completion: 0, cacheHit: 0, miss: 0 }, cats: [] });
  expect(document.getElementById('tokenCats').textContent).toContain('暂无任何猫的用量记录');
});
