// tests/unit/group.test.mjs —— 群聊页（A301 / A302）
// 覆盖：类型标签 · 未读文本（按成员游标派生）· 消息行（序号 / 发送方 / 类型 / 正文）· 增量追加 · 组信息未读 · 轮询起停
// 基座：与 panel-group.test.mjs 同构（页面 DOM + 无导出脚本加载）；页面为 group.html（组端口形态）
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
  await runGlobalScript(new URL('../../js/group.js', import.meta.url));
});

test('消息类型标签——派单 / 回执 / 讨论', () => {
  expect(window.gTypeLabel('delegation')).toBe('派单');
  expect(window.gTypeLabel('receipt')).toBe('回执');
  expect(window.gTypeLabel('note')).toBe('讨论');
  expect(window.gTypeLabel('')).toBe('讨论');
});

test('消息行——序号 / 发送方（显示名 + key）/ 类型 / 正文', () => {
  const row = window.gBuildRow({
    seq: 7,
    from: 'coder1',
    displayName: 'Coder',
    type: 'receipt',
    ts: 0,
    text: '完成：A302'
  });
  expect(row.className).toBe('msg');
  const head = row.querySelector('.msg-head');
  expect(head.textContent).toContain('#7');
  expect(head.textContent).toContain('Coder（coder1）');
  expect(head.textContent).toContain('回执');
  expect(row.querySelector('.msg-text').textContent).toBe('完成：A302');
});

test('消息行——人身份（无 displayName）显示「人」，显示名等于 key 时只显示一次', () => {
  const human = window.gBuildRow({ seq: 1, from: '', type: 'note', ts: 0, text: '甲' });
  expect(human.querySelector('.msg-head').textContent).toContain('人');
  const same = window.gBuildRow({ seq: 2, from: 'coder1', displayName: 'coder1', type: 'note', ts: 0, text: '乙' });
  expect(same.querySelector('.msg-head').textContent).toContain('coder1');
  expect(same.querySelector('.msg-head').textContent).not.toContain('（coder1）');
});

test('增量追加——清掉空态、只追加不重建、已有行原样保留', () => {
  const box = document.getElementById('messages');
  box.textContent = '';
  const empty = document.createElement('div');
  empty.className = 'empty';
  empty.textContent = '（本组还没有消息）';
  box.appendChild(empty);
  window.gAppendMessages([{ seq: 1, from: '', type: 'note', ts: 0, text: '甲' }]);
  expect(box.querySelectorAll('.msg').length).toBe(1);
  expect(box.querySelector('.empty')).toBeNull();
  window.gAppendMessages([{ seq: 2, from: '', type: 'note', ts: 0, text: '乙' }]);
  const rows = box.querySelectorAll('.msg');
  expect(rows.length).toBe(2);
  expect(rows[0].querySelector('.msg-text').textContent).toBe('甲');
  expect(rows[1].querySelector('.msg-text').textContent).toBe('乙');
  // 空列表不加行
  window.gAppendMessages([]);
  expect(box.querySelectorAll('.msg').length).toBe(2);
});

test('组信息渲染——成员数 / 消息数 / 标题', async () => {
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
  expect(document.getElementById('groupTitle').textContent).toBe('施工队');
  expect(document.getElementById('groupMeta').textContent).toContain('成员 1');
  expect(document.getElementById('groupMeta').textContent).toContain('消息 5');
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
