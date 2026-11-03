// tests/unit/group.test.mjs —— 群聊页（A301 / A302 / A311）
// 覆盖：类型标签 · 消息行结构（对话页 text 泡 + 首行 meta + MD 正文）· 人 / 猫分侧 ·
//       增量追加 · 组信息 · 轮询起停 · 可见性联动
// 基座：与 panel-group.test.mjs 同构（页面 DOM + 无导出脚本加载）；页面为 group.html（组端口形态）
// A311：群聊页复用对话页布局——本用例同时加载 md.js / scroll.js（group.js 的运行时依赖）
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { expect, test, beforeAll } from 'vitest';
import { installDom, installMockFetch } from './mock-env.js';

// 加载无导出脚本到全局（vm.runInThisContext——var/function 声明挂 globalThis）
async function runGlobalScript(url) {
  const code = await readFile(url, 'utf-8');
  vm.runInThisContext(code, { filename: url.toString() });
}

beforeAll(async () => {
  const html = await readFile(new URL('../../group.html', import.meta.url), 'utf-8');
  installDom(html, 'http://127.0.0.1:9000');
  installMockFetch();
  await runGlobalScript(new URL('../../js/ui-common.js', import.meta.url));
  await runGlobalScript(new URL('../../js/chat/lib/md.js', import.meta.url));
  await runGlobalScript(new URL('../../js/chat/fx/scroll.js', import.meta.url));
  await runGlobalScript(new URL('../../js/group.js', import.meta.url));
});

test('消息类型标签——派单 / 回执 / 讨论', () => {
  expect(window.gTypeLabel('delegation')).toBe('派单');
  expect(window.gTypeLabel('receipt')).toBe('回执');
  expect(window.gTypeLabel('note')).toBe('讨论');
  expect(window.gTypeLabel('')).toBe('讨论');
});

test('消息行——对话页 text 泡结构：首行 meta（类型 / 来源 / 序号）+ MD 正文', () => {
  const row = window.gBuildRow({
    seq: 7,
    from: 'coder1',
    displayName: 'Coder',
    type: 'receipt',
    ts: 0,
    text: '完成：A302'
  });
  expect(row.className).toBe('chat-row assistant');
  expect(row.getAttribute('data-type')).toBe('text');
  const bubble = row.querySelector('.chat-bubble');
  expect(bubble).not.toBeNull();
  const meta = bubble.querySelector('.msg-meta');
  expect(meta.textContent).toContain('#7');
  expect(meta.textContent).toContain('Coder（coder1）');
  expect(meta.textContent).toContain('回执');
  expect(bubble.querySelector('.md-block').textContent).toBe('完成：A302');
});

test('消息行——人靠右（user 泡）· 显示名等于 key 时只显示一次', () => {
  const human = window.gBuildRow({ seq: 1, from: '', type: 'note', ts: 0, text: '甲' });
  expect(human.className).toBe('chat-row user');
  expect(human.getAttribute('data-type')).toBe('user');
  expect(human.querySelector('.msg-meta').textContent).toContain('人');
  const same = window.gBuildRow({ seq: 2, from: 'coder1', displayName: 'coder1', type: 'note', ts: 0, text: '乙' });
  expect(same.querySelector('.msg-meta').textContent).toContain('coder1');
  expect(same.querySelector('.msg-meta').textContent).not.toContain('（coder1）');
});

test('消息行——派单目标与关联单号进首行；MD 渲染出结构标签', () => {
  const row = window.gBuildRow({
    seq: 9,
    from: 'majordomo',
    displayName: '雾理莎',
    to: 'coder1',
    toDisplayName: 'Coder',
    type: 'delegation',
    refs: '7',
    ts: 0,
    text: '**加急** 处理 A310'
  });
  const meta = row.querySelector('.msg-meta').textContent;
  expect(meta).toContain('派单');
  expect(meta).toContain('→ @Coder');
  expect(meta).toContain('关联 #7');
  expect(row.querySelector('.md-block strong').textContent).toBe('加急');
});

test('增量追加——清掉空态、只追加不重建、已有行原样保留', () => {
  const box = document.getElementById('chatMsgs');
  box.textContent = '';
  const empty = document.createElement('div');
  empty.className = 'chat-plain group-empty';
  empty.textContent = '（本组还没有消息）';
  box.appendChild(empty);
  window.gAppendMessages([{ seq: 1, from: '', type: 'note', ts: 0, text: '甲' }]);
  expect(box.querySelectorAll('.chat-row').length).toBe(1);
  expect(box.querySelector('.group-empty')).toBeNull();
  window.gAppendMessages([{ seq: 2, from: '', type: 'note', ts: 0, text: '乙' }]);
  const rows = box.querySelectorAll('.chat-row');
  expect(rows.length).toBe(2);
  expect(rows[0].querySelector('.md-block').textContent).toBe('甲');
  expect(rows[1].querySelector('.md-block').textContent).toBe('乙');
  // 空列表不加行
  window.gAppendMessages([]);
  expect(box.querySelectorAll('.chat-row').length).toBe(2);
});

test('组信息渲染——现状简介（状态 / 成员数 / 消息数 / 项目）与标题', async () => {
  globalThis.fetch = async () => ({
    json: async () => ({
      ok: true,
      group: {
        name: '施工队',
        enabled: true,
        project: 'CH4',
        msgCount: 5,
        lastSeq: 5,
        members: [{ key: 'majordomo', displayName: '雾理莎' }]
      }
    })
  });
  window.gLoadInfo();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(document.getElementById('chatTitle').textContent).toBe('施工队');
  const info = document.getElementById('chatInfo').textContent;
  expect(info).toContain('运行中');
  expect(info).toContain('成员 1');
  expect(info).toContain('消息 5');
  expect(info).toContain('项目 CH4');
});

test('组信息渲染——停用组显示「已关闭」', async () => {
  globalThis.fetch = async () => ({
    json: async () => ({
      ok: true,
      group: { name: '歇班组', enabled: false, msgCount: 0, lastSeq: 0, members: [] }
    })
  });
  window.gLoadInfo();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(document.getElementById('chatInfo').textContent).toContain('已关闭');
});

test('轮询起停——启动幂等、停止归零', () => {
  window.gStopPoll();
  window.gStartPoll();
  const first = window.gPollTimer;
  expect(first).not.toBe(0);
  window.gStartPoll();
  expect(window.gPollTimer).toBe(first);
  window.gStopPoll();
  expect(window.gPollTimer).toBe(0);
});

test('可见性联动——隐藏停轮询，回到前台重启', () => {
  window.gStopPoll();
  window.gStartPoll();
  Object.defineProperty(document, 'hidden', { value: true, configurable: true });
  window.gOnVisibilityChange();
  expect(window.gPollTimer).toBe(0);
  Object.defineProperty(document, 'hidden', { value: false, configurable: true });
  window.gOnVisibilityChange();
  expect(window.gPollTimer).not.toBe(0);
  window.gStopPoll();
});
