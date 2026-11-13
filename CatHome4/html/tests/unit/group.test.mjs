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

test('消息行——A339 结构：头像 + 消息头（名称 / 类型徽标 / 序号）+ MD 正文气泡', () => {
  const row = window.gBuildRow({
    seq: 7,
    from: 'coder1',
    displayName: 'Coder',
    type: 'receipt',
    ts: 0,
    text: '完成：A302'
  });
  expect(row.className).toBe('chat-row assistant grp-row');
  expect(row.getAttribute('data-type')).toBe('text');
  expect(row.querySelector('.grp-avatar').textContent).toBe('C');
  const bubble = row.querySelector('.chat-bubble');
  expect(bubble).not.toBeNull();
  // 需求①：头行与气泡分离——头在气泡之外（.chat-bubble 内不再有头）
  const headEl = row.querySelector('.grp-body > .grp-head');
  expect(headEl).not.toBeNull();
  expect(bubble.querySelector('.grp-head')).toBeNull();
  const head = headEl.textContent;
  expect(head).toContain('#7');
  expect(head).toContain('Coder（coder1）');
  expect(head).toContain('回执');
  expect(headEl.querySelector('.grp-kind').getAttribute('data-kind')).toBe('receipt');
  expect(bubble.querySelector('.md-block').textContent).toBe('完成：A302');
});

test('A339 小时间戳——HH:mm 进头行，完整时刻挂 title', () => {
  expect(window.gShortTime(0)).toBe('');
  const ts = new Date(2026, 10, 13, 9, 5, 0).getTime();
  const row = window.gBuildRow({ seq: 1, from: 'coder1', type: 'note', ts: ts, text: 'x' });
  const t = row.querySelector('.grp-head .grp-time');
  expect(t.textContent).toBe('09:05');
  expect(t.getAttribute('title')).toContain('2026-11-13 09:05:00');
});

test('消息行——人靠右（user 泡）· 显示名等于 key 时只显示一次', () => {
  const human = window.gBuildRow({ seq: 1, from: '', type: 'note', ts: 0, text: '甲' });
  expect(human.className).toBe('chat-row user grp-row');
  expect(human.getAttribute('data-type')).toBe('user');
  expect(human.querySelector('.grp-head').textContent).toContain('默认用户');
  const same = window.gBuildRow({ seq: 2, from: 'coder1', displayName: 'coder1', type: 'note', ts: 0, text: '乙' });
  expect(same.querySelector('.grp-head').textContent).toContain('coder1');
  expect(same.querySelector('.grp-head').textContent).not.toContain('（coder1）');
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
  const head = row.querySelector('.grp-head').textContent;
  expect(head).toContain('派单');
  expect(head).toContain('→ @Coder');
  expect(head).toContain('关联 #7');
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
  const btn = document.getElementById('grpAtBtn');
  window.gTarget = '';
  const row = window.gBuildRow({ seq: 3, from: 'coder1', displayName: 'Coder', type: 'note', ts: 0, text: '甲' });
  const who = row.querySelector('.grp-head .grp-who');
  expect(who.className).toContain('g-who-pick');
  expect(who.getAttribute('role')).toBe('button');
  who.click();
  expect(window.gTarget).toBe('coder1');
  expect(btn.textContent).toBe('@ Coder');
  // 常规动作不写提示文本（2026-11-13 莎定——外围只留气泡）
  expect(document.getElementById('groupMsg').textContent).toBe('');
  // 人（user 泡）不挂入口——点名字无 @ 语义
  const human = window.gBuildRow({ seq: 4, from: '', type: 'note', ts: 0, text: '乙' });
  expect(human.querySelector('.grp-head .g-who-pick')).toBeNull();
});

test('A313 @ 快捷填入——发送方不在组内成员时只提示，不改目标', () => {
  window.gFillTargets([{ key: 'coder1', displayName: 'Coder' }]);
  window.gTarget = '';
  window.gPickTarget('outsider');
  expect(window.gTarget).toBe('');
  // 失败出声（唯一保留的提示通道）——文本 + .err 才显示
  const msg = document.getElementById('groupMsg');
  expect(msg.textContent).toContain('不在组内成员');
  expect(msg.classList.contains('err')).toBe(true);
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
  const who = document.querySelector('.chat-row.user .grp-head .grp-who');
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
  expect(document.querySelector('.chat-row.user .grp-head .grp-who')).toBe(who);
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

// A341——类型筛选 × 猫筛选叠加（2026-11-13 莎定）
test('A341 筛选——类型切换只留该类型，猫筛选只留该猫，两层叠加取交集', () => {
  const box = document.getElementById('chatMsgs');
  const t0 = new Date(2026, 10, 13, 9, 0, 0).getTime();
  box.textContent = '';
  window.gRenderMessages([
    { seq: 1, from: 'c1', displayName: '丙', type: 'note', ts: t0, text: '丙-讨论' },
    { seq: 2, from: 'c1', displayName: '丙', type: 'receipt', ts: t0 + 1000, text: '丙-回执' },
    { seq: 3, from: 'a1', displayName: '甲', type: 'delegation', ts: t0 + 2000, text: '甲-派单' },
    { seq: 4, from: '', type: 'note', ts: t0 + 3000, text: '人-讨论' }
  ]);
  const visible = () => Array.prototype.filter
    .call(box.querySelectorAll('.chat-row'), (r) => !r.classList.contains('grp-hidden'))
    .map((r) => r.getAttribute('data-kind') + ':' + (r.getAttribute('data-from') || 'human'));

  expect(visible().length).toBe(4);
  // 类型：回执
  window.gSetFilterKind('receipt');
  expect(visible()).toEqual(['receipt:c1']);
  // 叠加：甲 + 回执 → 空，并出声
  window.gToggleCatFilter('a1');
  expect(visible()).toEqual([]);
  expect(box.querySelector('.group-filter-empty')).not.toBeNull();
  // 叠加：甲 + 派单 → 命中一条；空态提示被摘掉
  window.gSetFilterKind('delegation');
  expect(visible()).toEqual(['delegation:a1']);
  expect(box.querySelector('.group-filter-empty')).toBeNull();
  // 取消猫筛选（再点同球）→ 派单仅甲那条
  window.gToggleCatFilter('a1');
  expect(window.gFilterCat).toBe('');
  // 回到全部
  window.gSetFilterKind('all');
  expect(visible().length).toBe(4);
});

test('A341 球列选中态——点球挂 .sel，换球转移，再点取消', () => {
  const el = document.getElementById('groupCats');
  el.textContent = '';
  window.gCatNodes = {};
  window.gRenderCats([
    { key: 'c1', displayName: '丙', admin: false, running: true, themeColor: 1, runState: 'idle', context: 0 },
    { key: 'a1', displayName: '甲', admin: true, running: true, themeColor: 2, runState: 'idle', context: 0 }
  ]);
  window.gFilterCat = '';
  const items = el.querySelectorAll('.gc-item');
  // 管理员球排前：items[0] = a1（甲），items[1] = c1（丙）
  items[1].click();
  expect(window.gFilterCat).toBe('c1');
  expect(items[1].classList.contains('sel')).toBe(true);
  items[0].click();
  expect(window.gFilterCat).toBe('a1');
  expect(items[1].classList.contains('sel')).toBe(false);
  expect(items[0].classList.contains('sel')).toBe(true);
  items[0].click();
  expect(window.gFilterCat).toBe('');
  expect(items[0].classList.contains('sel')).toBe(false);
  // 复位，避免影响后续用例
  window.gFilterCat = '';
  window.gSetFilterKind('all');
});

test('A341 时间分割线随筛选重算——隐藏行不参与相邻判定', () => {
  const box = document.getElementById('chatMsgs');
  const t0 = new Date(2026, 10, 13, 9, 0, 0).getTime();
  box.textContent = '';
  window.gRenderMessages([
    { seq: 1, from: 'a1', displayName: '甲', type: 'note', ts: t0, text: '甲1' },
    { seq: 2, from: 'c1', displayName: '丙', type: 'note', ts: t0 + 10 * 60 * 1000, text: '丙1' },
    { seq: 3, from: 'a1', displayName: '甲', type: 'note', ts: t0 + 120 * 60 * 1000, text: '甲2' }
  ]);
  // 全量：丙1 → 甲2 跨 110 分钟 → 一条分割线
  expect(box.querySelectorAll('.grp-divider').length).toBe(1);
  // 只看甲：甲1 → 甲2 跨 120 分钟 → 仍一条（且不因隐藏行错位）
  window.gToggleCatFilter('a1');
  expect(box.querySelectorAll('.grp-divider').length).toBe(1);
  window.gToggleCatFilter('a1');
  window.gSetFilterKind('all');
});

// A340 补充（2026-11-13 莎定）——类型徽标排在显示名之前
test('A340 消息头——类型徽标排在显示名之前', () => {
  const row = window.gBuildRow({ seq: 1, from: 'coder1', displayName: 'Coder', type: 'delegation', ts: 0, text: 'x' });
  const head = row.querySelector('.grp-head');
  expect(head.children[0].className).toContain('grp-kind');
  expect(head.children[1].className).toContain('grp-who');
});

// A340 补充（2026-11-13 莎定）——时间分割线：相邻两行 ts 差 > 1h 时插一条（纯前端渲染，重算幂等）
test('A340 时间分割线——相邻 > 1h 插一条，恰好 1h 不插', () => {
  const box = document.getElementById('chatMsgs');
  // 筛选态复位（用例独立：筛选会隐藏行、连带影响相邻判定）
  window.gFilterCat = '';
  window.gFilterKind = 'all';
  const t0 = new Date(2026, 10, 13, 9, 0, 0).getTime();
  box.textContent = '';
  window.gRenderMessages([
    { seq: 1, from: 'coder1', type: 'note', ts: t0, text: '甲' },
    { seq: 2, from: 'coder1', type: 'note', ts: t0 + 10 * 60 * 1000, text: '乙' },
    { seq: 3, from: 'coder1', type: 'note', ts: t0 + 90 * 60 * 1000, text: '丙' }
  ]);
  const divs = box.querySelectorAll('.grp-divider');
  expect(divs.length).toBe(1);
  expect(divs[0].textContent).toContain('10:30');
  // 首行之前不插
  expect(box.firstElementChild.classList.contains('chat-row')).toBe(true);
  // 重算幂等——不叠线
  window.gApplyDividers();
  expect(box.querySelectorAll('.grp-divider').length).toBe(1);
  // 恰好 1h（不严格大于）不插
  box.textContent = '';
  window.gRenderMessages([
    { seq: 1, from: 'coder1', type: 'note', ts: t0, text: '甲' },
    { seq: 2, from: 'coder1', type: 'note', ts: t0 + 3600000, text: '乙' }
  ]);
  expect(box.querySelectorAll('.grp-divider').length).toBe(0);
});

// A342——滚动带：关闭合并后每条独立成刻度；刻度色取行内 --gc-color（= 气泡描边色同源机制）
test('A342 滚动带——合并关闭后每条一行一段，人侧行挂气泡线色', () => {
  const mk = (type) => {
    const d = document.createElement('div');
    d.className = 'chat-row';
    d.setAttribute('data-type', type);
    return { row: d, role: 'reply', type: type };
  };
  // 群聊页：body 引导里 chatScrollInit(false) 把 bandFoldEnabled 置 false
  window.bandFoldEnabled = false;
  expect(window.bandFoldSegs([mk('text'), mk('text'), mk('text')]).length).toBe(3);
  // 打开合并（对话页行为）——连续同 data-type 折叠
  window.bandFoldEnabled = true;
  expect(window.bandFoldSegs([mk('text'), mk('text'), mk('user')]).length).toBe(2);
  // 人侧行同样挂线色变量——刻度与气泡共用同一色源
  const human = window.gBuildRow({ seq: 1, from: '', type: 'note', ts: 0, text: '甲' });
  expect(human.style.getPropertyValue('--gc-color')).toBe('var(--ch-bubble-line-user)');
  const cat = window.gBuildRow({ seq: 2, from: 'a1', type: 'note', ts: 0, text: '乙' });
  expect(cat.style.getPropertyValue('--gc-color').length).toBeGreaterThan(0);
  window.bandFoldEnabled = false;
});
test('@ 目标浮层——按钮开合、列表含「无 @」与成员、选中态与标签同步', () => {
  window.gFillTargets([
    { key: 'coder1', displayName: 'Coder', admin: false, subscriber: false },
    { key: 'majordomo', displayName: '雾理莎', admin: true, subscriber: true }
  ]);
  const pop = document.getElementById('grpAtPopover');
  const btn = document.getElementById('grpAtBtn');
  const list = document.getElementById('grpAtList');
  expect(pop.classList.contains('on')).toBe(false);
  btn.click();
  expect(pop.classList.contains('on')).toBe(true);
  expect(list.querySelectorAll('.grp-at-row').length).toBe(3);
  expect(list.children[0].getAttribute('data-key')).toBe('');
  expect(list.textContent).toContain('★ 🔔 雾理莎');
  // 点行选中 → 目标写入、浮层收起、按钮标签同步
  list.children[2].click();
  expect(window.gTarget).toBe('majordomo');
  expect(pop.classList.contains('on')).toBe(false);
  expect(btn.textContent).toBe('@ 雾理莎');
  // 再开 → 点「无 @」清空
  btn.click();
  list.children[0].click();
  expect(window.gTarget).toBe('');
  expect(btn.textContent).toBe('@ 无');
  // 成员名单变化把当前目标挤掉 → 回落不 @（不静默留着无效目标）
  window.gPickTarget('coder1');
  window.gFillTargets([{ key: 'majordomo', displayName: '雾理莎' }]);
  expect(window.gTarget).toBe('');
});
