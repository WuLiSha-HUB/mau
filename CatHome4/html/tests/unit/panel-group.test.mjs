// tests/unit/panel-group.test.mjs —— 配队（Group）管理页（A300）
// 覆盖：类型标签 · 组卡片渲染（组名 / 成员显示名 + key / 消息数）· 空态 · 成员勾选渲染与收集 · 保存载荷 · 空成员拒绝
// 基座：与 panel-packs.test.mjs 同构（index.html DOM + 主面板脚本加载）
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
  await runGlobalScript(new URL('../../js/panel-group.js', import.meta.url));
});

test('消息类型标签——派单 / 回执 / 其他', () => {
  expect(window.groupTypeLabel('delegation')).toBe('派单');
  expect(window.groupTypeLabel('receipt')).toBe('回执');
  expect(window.groupTypeLabel('note')).toBe('其他');
  expect(window.groupTypeLabel('')).toBe('其他');
});

test('组卡片渲染——组名 / 成员显示名与 key / 端口 / 消息数', () => {
  window.applyGroups([{
    name: '施工队',
    enabled: true,
    running: true,
    port: 9000,
    project: 'CH4',
    createdAt: Date.now(),
    lastSeq: 3,
    msgCount: 2,
    lastAt: Date.now(),
    members: [{ key: 'majordomo', displayName: '雾理莎' }]
  }]);
  const cards = document.querySelectorAll('#groupCards > div');
  expect(cards.length).toBe(1);
  expect(cards[0].textContent).toContain('施工队');
  expect(cards[0].textContent).toContain('雾理莎');
  expect(cards[0].textContent).toContain('majordomo');
  expect(cards[0].textContent).toContain('消息 2 条');
  expect(cards[0].textContent).toContain('运行中');
  expect(cards[0].textContent).toContain('端口 9000');
});

test('空列表——空态提示', () => {
  window.applyGroups([]);
  expect(document.querySelectorAll('#groupCards > div').length).toBe(0);
  expect(document.getElementById('groupMsg').textContent).toContain('暂无配队');
});

test('成员勾选渲染——全猫列出、已选中的勾上', () => {
  window.renderGroupMembers(
    [{ id: 'majordomo', name: '雾理莎', special: true }, { id: 'coder1', name: 'Coder' }],
    [{ key: 'coder1', displayName: 'Coder' }]
  );
  const boxes = document.querySelectorAll('#groupCfgMembers input.group-member-cb');
  expect(boxes.length).toBe(2);
  expect(boxes[0].checked).toBe(false);
  expect(boxes[1].checked).toBe(true);
  expect(window.collectGroupMembers()).toEqual(['coder1']);
});

test('保存载荷——name / project / 静默阈值 / memberKeys / adminKeys / subscriberKeys（cat key）', async () => {
  let captured = null;
  globalThis.fetch = async (url, opt) => {
    if (opt && opt.body) { captured = JSON.parse(opt.body); }
    return { json: async () => ({ ok: true }) };
  };
  document.getElementById('groupCfgName').value = '施工队';
  document.getElementById('groupCfgProject').value = 'CH4';
  document.getElementById('groupCfgIdle').value = '120';
  window.renderGroupMembers([{ id: 'majordomo', name: '雾理莎' }], [], ['majordomo'], []);
  document.querySelector('#groupCfgMembers input.group-member-cb').checked = true;
  document.querySelector('#groupCfgMembers input.group-admin-cb').checked = true;
  window.saveGroupCfg();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(captured).toEqual({ name: '施工队', project: 'CH4', memberKeys: ['majordomo'], adminKeys: ['majordomo'], subscriberKeys: [], subscribeIdleSec: 120 });
});

test('三列勾选渲染——成员 / 管理员 / 订阅各自的初始态与收集口', () => {
  window.renderGroupMembers(
    [{ id: 'majordomo', name: '雾理莎' }, { id: 'coder1', name: 'Coder' }],
    [{ key: 'majordomo' }, { key: 'coder1' }],
    ['majordomo'],
    ['coder1']
  );
  const admins = document.querySelectorAll('#groupCfgMembers input.group-admin-cb');
  const subs = document.querySelectorAll('#groupCfgMembers input.group-sub-cb');
  expect(admins.length).toBe(2);
  expect(subs.length).toBe(2);
  expect(admins[0].checked).toBe(true);
  expect(admins[1].checked).toBe(false);
  expect(subs[0].checked).toBe(false);
  expect(subs[1].checked).toBe(true);
  expect(window.collectGroupAdmins()).toEqual(['majordomo']);
  expect(window.collectGroupSubscribers()).toEqual(['coder1']);
});

test('保存拦截——组名为空 / 未勾成员均不发请求', async () => {
  let called = false;
  globalThis.fetch = async () => { called = true; return { json: async () => ({ ok: true }) }; };
  window.renderGroupMembers([{ id: 'majordomo', name: '雾理莎' }], []);
  document.getElementById('groupCfgName').value = '';
  window.saveGroupCfg();
  document.getElementById('groupCfgName').value = '施工队';
  window.saveGroupCfg();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(called).toBe(false);
});
