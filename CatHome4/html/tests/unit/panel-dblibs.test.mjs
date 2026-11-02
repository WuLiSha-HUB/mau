// tests/unit/panel-dblibs.test.mjs —— 猫配置弹层「库权限」勾选面（A275）
// 覆盖：库池渲染（条目数 / 坏库标记 / 池外已配置项不静默丢弃）· 清单不可取时如实告警 ·
//       收集 · 保存载荷（可取才提交 dbLibs——不可取时缺省保留磁盘现值，防静默清空）
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
});

/** 渲染后的复选框清单 */
function boxes() {
  return Array.from(document.querySelectorAll('#catCfgDbLibs input[type=checkbox].db-lib-cb'));
}

test('库池渲染——条目数可见，已配置项勾选，未配置项不勾', () => {
  window.renderDbLibChecks(
    [{ id: 'smriti', entries: 12, broken: false }, { id: 'l3historytest', entries: 1, broken: false }],
    ['smriti'],
    true
  );
  const list = boxes();
  expect(list.length).toBe(2);
  expect(list[0].getAttribute('data-lib')).toBe('smriti');
  expect(list[0].checked).toBe(true);
  expect(list[1].getAttribute('data-lib')).toBe('l3historytest');
  expect(list[1].checked).toBe(false);
  expect(document.getElementById('catCfgDbLibs').textContent).toContain('smriti · 12 条');
});

test('坏库——显式标「⚠ 坏库」（不按正常库静默渲染）', () => {
  window.renderDbLibChecks([{ id: 'brokenlib', entries: 0, broken: true }], [], true);
  expect(document.getElementById('catCfgDbLibs').textContent).toContain('brokenlib · 0 条 ⚠ 坏库');
});

test('池外已配置项——如实列出「（未登记）」且保持勾选（不静默丢弃）', () => {
  window.renderDbLibChecks([{ id: 'smriti', entries: 0, broken: false }], ['gone-lib'], true);
  const list = boxes();
  expect(list.length).toBe(2);
  const stale = list.find((b) => b.getAttribute('data-lib') === 'gone-lib');
  expect(stale).toBeTruthy();
  expect(stale.checked).toBe(true);
  expect(document.getElementById('catCfgDbLibs').textContent).toContain('gone-lib（未登记）');
});

test('清单不可取——告警替代勾选面（不渲染成「无库」）', () => {
  window.renderDbLibChecks([], ['smriti'], false);
  expect(boxes().length).toBe(0);
  expect(document.getElementById('catCfgDbLibs').textContent).toContain('库清单不可取');
});

test('库池为空且未配置——显式占位「尚无库」', () => {
  window.renderDbLibChecks([], [], true);
  expect(document.getElementById('catCfgDbLibs').textContent).toContain('尚无库');
});

test('收集——按勾选产出库 id 数组', () => {
  window.renderDbLibChecks(
    [{ id: 'smriti', entries: 0, broken: false }, { id: 'l3historytest', entries: 0, broken: false }],
    ['l3historytest'],
    true
  );
  expect(window.collectDbLibChecks()).toEqual(['l3historytest']);
});

test('保存载荷——可取时含 dbLibs；不可取时不提交（缺省保留磁盘现值）', async () => {
  let captured = null;
  globalThis.fetch = async (url, init) => {
    captured = JSON.parse(init.body);
    return { json: async () => ({ ok: true }) };
  };
  window.catCfgTarget = 'test-cat';

  window.catCfgDbLibsAvailable = true;
  window.renderDbLibChecks([{ id: 'smriti', entries: 3, broken: false }], ['smriti'], true);
  window.saveCatCfg();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(captured.dbLibs).toEqual(['smriti']);

  captured = null;
  window.catCfgDbLibsAvailable = false;
  window.renderDbLibChecks([], ['smriti'], false);
  window.saveCatCfg();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(captured).toBeTruthy();
  expect(Object.prototype.hasOwnProperty.call(captured, 'dbLibs')).toBe(false);
});
