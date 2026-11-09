// tests/unit/group.test.mjs —— 群聊页（A301 / A302 / A311）
// 覆盖：类型标签 · 消息行结构（对话页 text 泡 + 首行 meta + MD 正文）· 人 / 猫分侧 ·
//       增量追加 · 组信息 · 轮询起停 · 可见性联动
// 基座：与 panel-group.test.mjs 同构（页面 DOM + 无导出脚本加载）；页面为 group.html（组端口形态）
// A311：群聊页复用对话页布局——本用例同时加载 md.js / scroll.js（group.js 的运行时依赖）
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { expect, test, beforeAll } from 'vitest';
import { installDom, installMockFetch, installMockClipboard } from './mock-env.js';

// 加载无导出脚本到全局（vm.runInThisContext——var/function 声明挂 globalThis）
async function runGlobalScript(url) {
  const code = await readFile(url, 'utf-8');
  vm.runInThisContext(code, { filename: url.toString() });
}

beforeAll(async () => {
  const html = await readFile(new URL('../../group.html', import.meta.url), 'utf-8');
  installDom(html, 'http://127.0.0.1:9000');
  installMockFetch();
  installMockClipboard();
  await runGlobalScript(new URL('../../js/ui-common.js', import.meta.url));
  await runGlobalScript(new URL('../../js/chat/lib/dom.js', import.meta.url));
  await runGlobalScript(new URL('../../js/chat/lib/md.js', import.meta.url));
  await runGlobalScript(new URL('../../js/chat/lib/md-copy.js', import.meta.url));
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
  expect(human.querySelector('.msg-meta').textContent).toContain('默认用户');
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

test('A313 消息操作——猫消息发送方名字可点，点击把该猫填进 @ 选择器（不发送）', () => {
  window.gFillTargets([
    { key: 'coder1', displayName: 'Coder' },
    { key: 'majordomo', displayName: '雾理莎' }
  ]);
  const toSel = document.getElementById('toSel');
  toSel.value = '';
  const row = window.gBuildRow({ seq: 3, from: 'coder1', displayName: 'Coder', type: 'note', ts: 0, text: '甲' });
  const who = row.querySelector('.msg-meta .mm-who');
  expect(who.className).toContain('mm-who-pick');
  expect(who.getAttribute('role')).toBe('button');
  who.click();
  expect(toSel.value).toBe('coder1');
  expect(document.getElementById('groupMsg').textContent).toContain('已填入 @coder1');
  // 人（user 泡）不挂入口——点名字无 @ 语义
  const human = window.gBuildRow({ seq: 4, from: '', type: 'note', ts: 0, text: '乙' });
  expect(human.querySelector('.msg-meta .mm-who-pick')).toBeNull();
});

test('A313 @ 快捷填入——发送方不在组内成员时只提示，不改选择器', () => {
  window.gFillTargets([{ key: 'coder1', displayName: 'Coder' }]);
  const toSel = document.getElementById('toSel');
  toSel.value = '';
  window.gPickTarget('outsider');
  expect(toSel.value).toBe('');
  expect(document.getElementById('groupMsg').textContent).toContain('不在组内成员');
});

test('A313 消息操作——单条复制走前端剪贴板，复制原文（不含 meta）', async () => {
  globalThis.__clipboardWrites.length = 0;
  const text = '第一行\n\n| a | b |\n| - | - |\n| 1 | 2 |';
  const row = window.gBuildRow({ seq: 5, from: 'coder1', type: 'note', ts: 0, text: text });
  // 表格 / 代码块复制按钮由 md-copy 挂载（与对话页同一件）
  expect(row.querySelectorAll('.md-block .md-copy-btn').length).toBe(1);
  const btn = row.querySelector('.msg-copy-btn');
  expect(btn).not.toBeNull();
  expect(btn.textContent).toBe('');
  expect(btn.getAttribute('aria-label')).toBe('复制本条原文');
  btn.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(globalThis.__clipboardWrites).toEqual([text]);
  expect(btn.className).toContain('done');
});

test('使用者署名——组 info 的 userName 投影到人消息；缺省回落「默认用户」', async () => {
  globalThis.fetch = async () => ({
    json: async () => ({
      ok: true,
      userName: '莎',
      group: { name: '施工队', enabled: true, msgCount: 0, lastSeq: 0, members: [] }
    })
  });
  window.gLoadInfo();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(window.gSenderText({ from: '', text: '甲' })).toBe('莎');
  // 猫消息不受影响（显示名优先，与 key 不同时附 key）
  expect(window.gSenderText({ from: 'coder1', displayName: 'Coder' })).toBe('Coder（coder1）');
  // 组 info 未给 userName → 保持上一次值（页面不自行清空）
  globalThis.fetch = async () => ({
    json: async () => ({ ok: true, group: { name: '施工队', enabled: true, msgCount: 0, lastSeq: 0, members: [] } })
  });
  window.gLoadInfo();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(window.gSenderText({ from: '', text: '乙' })).toBe('莎');
});

test('使用者署名——info 晚于消息首屏到达时回填既有行（同一节点，不重建）', async () => {
  window.gUserName = '默认用户';
  window.gRenderMessages([{ seq: 1, from: '', type: 'note', ts: 0, text: '甲' }]);
  const who = document.querySelector('.chat-row.user .msg-meta .mm-who');
  expect(who.textContent).toBe('默认用户');
  globalThis.fetch = async () => ({
    json: async () => ({
      ok: true,
      userName: '莎',
      group: { name: '施工队', enabled: true, msgCount: 1, lastSeq: 1, members: [] }
    })
  });
  window.gLoadInfo();
  await new Promise((resolve) => setTimeout(resolve, 0));
  // 同一节点被就地改写——不重建行（滚动位置与既有引用都不动）
  expect(document.querySelector('.chat-row.user .msg-meta .mm-who')).toBe(who);
  expect(who.textContent).toBe('莎');
});

// A319 球列——管理员在前（同级保持名单原序）；管理员球挂 👑
test('A319 球列——管理员排前，非管理员在后（同序），皇冠只挂管理员', () => {
  const el = document.getElementById('groupCats');
  el.textContent = '';
  window.gCatNodes = {};
  window.gRenderCats([
    { key: 'c1', displayName: '丙', admin: false, running: false, themeColor: 0, runState: 'idle', context: 0 },
    { key: 'a1', displayName: '甲', admin: true, running: true, themeColor: 1, runState: 'run', context: 1200 },
    { key: 'b1', displayName: '乙', admin: false, running: true, themeColor: 2, runState: 'idle', context: 500 },
    { key: 'a2', displayName: '丁', admin: true, running: true, themeColor: 3, runState: 'think', context: 3480 }
  ]);
  const names = Array.prototype.map.call(el.children, (n) => n.querySelector('.gc-name').textContent);
  expect(names).toEqual(['甲', '丁', '丙', '乙']);
  // 皇冠：管理员 block / 非管理员 none
  expect(el.children[0].querySelector('.gc-crown').style.display).toBe('block');
  expect(el.children[1].querySelector('.gc-crown').style.display).toBe('block');
  expect(el.children[2].querySelector('.gc-crown').style.display).toBe('none');
  // 前文折 K + 未运行显示破折号
  expect(el.children[0].querySelector('.gc-tok').textContent).toBe('1.2K');
  expect(el.children[2].querySelector('.gc-tok').textContent).toBe('—');
});

// A319 球列——重复刷新不重建节点（同一成员保持同一 DOM 节点）
test('A319 球列——重复渲染复用节点，不重建', () => {
  const el = document.getElementById('groupCats');
  const first = el.children[0];
  window.gRenderCats([
    { key: 'c1', displayName: '丙', admin: false, running: false, themeColor: 0, runState: 'idle', context: 0 },
    { key: 'a1', displayName: '甲', admin: true, running: true, themeColor: 1, runState: 'reply', context: 900 },
    { key: 'b1', displayName: '乙', admin: false, running: true, themeColor: 2, runState: 'idle', context: 500 },
    { key: 'a2', displayName: '丁', admin: true, running: true, themeColor: 3, runState: 'think', context: 3480 }
  ]);
  expect(el.children[0]).toBe(first);
  // 运行态刷新落点——灯点跟着改
  expect(first.querySelector('.gc-dot').className).toContain('reply');
  expect(first.querySelector('.gc-tok').textContent).toBe('900');
});

// A320 气泡着色——只写描边变量（不染底色）；刻度取主题色靠 scroll.js 复制 --gc-color
test('A320 气泡着色——只写行内 --gc-color，不再挂底色标记', () => {
  const row = window.gBuildRow({ seq: 3, from: 'a1', displayName: '甲', type: 'note', ts: 0, text: 'x' });
  window.gThemeIdx = { a1: 3 };
  window.gApplyCatTheme(row, 'a1');
  expect(row.style.getPropertyValue('--gc-color')).toBe('#c586c0');
  expect(row.getAttribute('data-cat-theme')).toBeNull();
  // 未在映射内的来源——回落 0 号白
  const other = window.gBuildRow({ seq: 4, from: 'zz', displayName: '乙', type: 'note', ts: 0, text: 'y' });
  window.gApplyCatTheme(other, 'zz');
  expect(other.style.getPropertyValue('--gc-color')).toBe('#ffffff');
});
