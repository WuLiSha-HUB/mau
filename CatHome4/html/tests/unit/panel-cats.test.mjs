// tests/unit/panel-cats.test.mjs —— 主面板多猫行（2026-10-01 主面板轮）
// 覆盖：列结构（ID 列撤除 / A299 QQ Bot 列撤除）· 名称行内编辑（POST cat-config displayName；未改动 / 清空不提交）
//       · LLM API / 思考强度下拉（change 即提交 cat-config 单字段）· 目录白名单无权目录展示 + 专用弹层入口 · 工具清单 / 前文注入单按钮
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { expect, test, beforeAll } from 'vitest';
import { installDom, installMockEventSource, installMockFetch } from './mock-env.js';

// 加载无导出脚本到全局（vm.runInThisContext——var/function 声明挂 globalThis）
async function runGlobalScript(url) {
  const code = await readFile(url, 'utf-8');
  vm.runInThisContext(code, { filename: url.toString() });
}

// 一拍——等 mock fetch 的 promise 链走完
function tick() {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

const CAT_ROW = {
  id: '33333333-3333-3333-3333-333333333333',
  name: '测试猫',
  running: false,
  port: -1,
  apiConfigId: '',
  qqbotId: '',
  enabledRoots: ['ccbp']
};

const ROOTS = [
  { id: 'workspace', path: 'D:\\Mau\\WorkSpace', writable: true },
  { id: 'ccbp', path: 'D:\\Mau\\CatCatBigParty', writable: true },
  { id: 'mau', path: 'D:\\Mau\\mau', writable: true }
];

let calls = [];

// 抓取 fetch——记录 url + body；回执固定 ok
function captureFetch() {
  calls = [];
  globalThis.fetch = async (url, opt) => {
    let body = null;
    if (opt && opt.body) { body = JSON.parse(opt.body); }
    calls.push({ url: url, body: body });
    return { json: async () => ({ ok: true }) };
  };
}

function callsTo(url) {
  return calls.filter((c) => c.url === url);
}

// 渲染单行——清空表体后调 renderCatRow（池数据由用例预设）
function renderRow(cat) {
  window.catApiOptions = [{ apiConfigId: 'aaaaaaaa-0000-0000-0000-000000000000', displayName: '主端点', isDefault: true }];
  window.catAllRoots = ROOTS;
  document.querySelector('#catsTable tbody').textContent = '';
  window.renderCatRow(cat);
  return document.querySelector('#catsTable tbody tr');
}

beforeAll(async () => {
  const html = await readFile(new URL('../../index.html', import.meta.url), 'utf-8');
  installDom(html);
  installMockEventSource();
  installMockFetch();
  await runGlobalScript(new URL('../../js/ui-common.js', import.meta.url));
  await runGlobalScript(new URL('../../js/app.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel.js', import.meta.url));
  await runGlobalScript(new URL('../../js/panel-catcfg.js', import.meta.url));
  await tick();
});

test('主面板行——9 列（会话状态列前置 + ID 列已撤 + A299 QQ Bot 列撤除）；名称可编辑、API/思考强度是下拉、工具清单 / 前文注入是按钮', () => {
  const tr = renderRow(CAT_ROW);
  expect(tr.children.length).toBe(9);
  expect(tr.children[1].querySelector('input').value).toBe('测试猫');
  expect(tr.children[2].querySelector('select')).not.toBeNull();
  // 思考强度列——紧随 LLM API；首项「跟随全局」+ 三档
  expect(tr.children[3].querySelector('select').value).toBe('');
  expect(tr.children[5].querySelector('button').textContent).toBe('工具清单');
  // 前文注入列——紧随工具清单，样式与工具清单按钮同源（btn-mini）
  const injectBtn = tr.children[6].querySelector('button');
  expect(injectBtn.textContent).toBe('前文注入');
  expect(injectBtn.className).toBe('btn-mini');
  expect(tr.children[7].textContent).toBe('-');
  // ID 不再出现在任何单元格
  const texts = [];
  for (let i = 0; i < tr.children.length; i = i + 1) { texts.push(tr.children[i].textContent); }
  expect(texts.join('|').indexOf(CAT_ROW.id)).toBe(-1);
});

test('目录白名单列——显示无权目录（池 ∖ 启用根），点「设置」开专用弹层', async () => {
  const tr = renderRow(CAT_ROW);   // enabledRoots = ['ccbp'] → 无权仅 mau（workspace 为系统根不计）
  const td = tr.children[4];
  expect(td.textContent).toContain('无权: mau');
  expect(td.textContent.indexOf('ccbp')).toBe(-1);
  td.querySelector('button').onclick();
  await tick();
  expect(document.getElementById('catRootsModal').style.display).toBe('flex');
  document.getElementById('catRootsModal').style.display = 'none';
});

test('目录白名单列——启用根为空 = 空白名单（非系统根全部无权）', () => {
  const tr = renderRow(Object.assign({}, CAT_ROW, { enabledRoots: [] }));
  const td = tr.children[4];
  expect(td.textContent).toContain('无权: ccbp, mau');
});

test('名称行内编辑——改动提交 POST cat-config displayName；未改动 / 清空不提交', async () => {
  captureFetch();
  const tr = renderRow(CAT_ROW);
  const input = tr.children[1].querySelector('input');

  input.dispatchEvent(new Event('change'));
  await tick();
  expect(callsTo('/api/v1/cat-config').length).toBe(0);

  input.value = '  ';
  input.dispatchEvent(new Event('change'));
  expect(input.value).toBe('测试猫');
  await tick();
  expect(callsTo('/api/v1/cat-config').length).toBe(0);

  input.value = '新名字';
  input.dispatchEvent(new Event('change'));
  await tick();
  const posts = callsTo('/api/v1/cat-config');
  expect(posts.length).toBe(1);
  expect(posts[0].body.cat).toBe(CAT_ROW.id);
  expect(posts[0].body.displayName).toBe('新名字');
});

test('LLM API 下拉——change 即提交 cat-config 单字段（空值 = 清空语义）', async () => {
  captureFetch();
  const tr = renderRow(Object.assign({}, CAT_ROW, {
    apiConfigId: 'aaaaaaaa-0000-0000-0000-000000000000'
  }));
  const apiSel = tr.children[2].querySelector('select');
  apiSel.value = '';
  apiSel.dispatchEvent(new Event('change'));
  await tick();
  const posts = callsTo('/api/v1/cat-config');
  expect(posts.length).toBe(1);
  expect(posts[0].body.cat).toBe(CAT_ROW.id);
  expect(posts[0].body.apiConfigId).toBe('');
});

test('思考强度列——change 即提交 cat-config reasoningEffort 单字段（空 = 跟随全局）', async () => {
  captureFetch();
  const tr = renderRow(Object.assign({}, CAT_ROW, { reasoningEffort: 'max' }));
  const sel = tr.children[3].querySelector('select');
  expect(sel.value).toBe('max');
  sel.value = '';
  sel.dispatchEvent(new Event('change'));
  await tick();
  const posts = callsTo('/api/v1/cat-config');
  expect(posts.length).toBe(1);
  expect(posts[0].body.cat).toBe(CAT_ROW.id);
  expect(posts[0].body.reasoningEffort).toBe('');
  // 单字段写——未提交字段不出现在 payload（后端缺省即保留）
  expect(posts[0].body.apiConfigId).toBe(undefined);
});

test('会话状态列——phase 徽标 + 七态名 + Note 标 + 前文（token/条/字符）/ 轮次 · 消息 · 待处理', () => {
  const row = Object.assign({}, CAT_ROW, {
    running: true, port: 8081, phase: 'LlmRunning', runState: 'think', round: 3, msgCount: 8, pending: 1,
    noteActive: true, context: 12345, contextCount: 42, contextChars: 45678, lastContextChangeAt: 0
  });
  const tr = renderRow(row);
  const td = tr.children[0];
  const badge = td.querySelector('.session-badge');
  expect(badge).not.toBeNull();
  expect(badge.className).toContain('ph-LlmRunning');
  expect(badge.textContent).toBe('LlmRunning');
  // 七态名（吸收 A225c）——独立徽标，class 带态名
  const rs = td.querySelector('.cat-runstate');
  expect(rs).not.toBeNull();
  expect(rs.textContent).toBe('think');
  expect(rs.className).toContain('think');
  expect(td.textContent).toContain('📋 Note');
  // 前文行（吸收 A225b 口径）——K 位计数 + 条数 + 字符
  expect(td.textContent).toContain('前文 12.35K token 42 条');
  expect(td.textContent).toContain('45.68K 字符');
  expect(td.textContent).toContain('轮次 3 · 消息 8 · 待处理 1');
  // 顺序——参数在前，前文变动在后
  expect(td.textContent.indexOf('前文')).toBeLessThan(td.textContent.indexOf('轮次'));
});

test('会话状态列——命中率行（吸收 A225b/A225d：🎯率 + Hit / Miss / Down）', () => {
  const row = Object.assign({}, CAT_ROW, {
    running: true, port: 8081, phase: 'Idle', round: 1, msgCount: 2, pending: 0,
    context: 1000, contextCount: 3, contextChars: 2000, lastContextChangeAt: 0,
    sessionPrompt: 100000, sessionMiss: 20000, sessionCompletion: 5000, sessionRate: 0.8
  });
  const tr = renderRow(row);
  const td = tr.children[0];
  const rate = td.querySelector('.cat-state-rate');
  expect(rate).not.toBeNull();
  expect(rate.textContent).toContain('🎯80.00%');
  expect(rate.textContent).toContain('Hit');
  expect(rate.textContent).toContain('Miss');
  expect(rate.textContent).toContain('Down');
  // Hit = prompt − miss = 80K
  expect(rate.textContent).toContain('80.00K');
  // 无会话数据（未跑过轮）→ 不出命中率行
  expect(renderRow(CAT_ROW).children[0].querySelector('.cat-state-rate')).toBeNull();
});

test('会话状态列——静默猫显示「静默」标（无 phase 徽标）', () => {
  const tr = renderRow(CAT_ROW);
  const td = tr.children[0];
  expect(td.textContent).toContain('静默');
  expect(td.querySelector('.session-badge')).toBeNull();
});

test('整行点击——内容区开会话详情；按钮 / 输入控件不触发', async () => {
  captureFetch();
  const row = Object.assign({}, CAT_ROW, { running: true, port: 8081, phase: 'Idle' });
  const tr = renderRow(row);
  // 内容区（会话状态单元格）→ 打开详情弹层
  tr.children[0].dispatchEvent(new Event('click', { bubbles: true }));
  await tick();
  expect(document.getElementById('catDetailModal').style.display).toBe('flex');
  expect(callsTo('/api/v1/cat-detail?cat=' + CAT_ROW.id).length).toBe(1);
  document.getElementById('catDetailModal').style.display = 'none';
  // 名称输入框 → 不触发
  tr.children[1].querySelector('input').dispatchEvent(new Event('click', { bubbles: true }));
  await tick();
  expect(document.getElementById('catDetailModal').style.display).toBe('none');
  // 工具清单按钮 → 不触发
  tr.children[5].querySelector('button').dispatchEvent(new Event('click', { bubbles: true }));
  await tick();
  expect(document.getElementById('catDetailModal').style.display).toBe('none');
  document.getElementById('catToolsModal').style.display = 'none';
  // 前文注入按钮 → 不触发详情；开前文注入弹层
  tr.children[6].querySelector('button').dispatchEvent(new Event('click', { bubbles: true }));
  await tick();
  expect(document.getElementById('catDetailModal').style.display).toBe('none');
  expect(document.getElementById('catInjectModal').style.display).toBe('flex');
  document.getElementById('catInjectModal').style.display = 'none';
});

test('Type 标签与分组——applyCatState 按 type 分区（组标题行）+ 名称格 Type 标', () => {
  const cats = [
    { id: 'm', name: '管家', special: true, running: false, port: -1 },
    { id: 'a', name: '甲', type: '开发', running: false, port: -1 },
    { id: 'b', name: '乙', type: '开发', running: false, port: -1 },
    { id: 'c', name: '丙', running: false, port: -1 }
  ];
  window.lastCatSig = '';
  window.applyCatState(cats);
  const body = document.querySelector('#catsTable tbody');
  const groupRows = body.querySelectorAll('tr.cat-group-row');
  // 三组：majordomo（special 置顶）/ 开发 / 默认
  expect(groupRows.length).toBe(3);
  expect(groupRows[0].textContent).toContain('majordomo');
  expect(groupRows[1].textContent).toContain('开发');
  expect(groupRows[1].textContent).toContain('（2）');
  expect(groupRows[2].textContent).toContain('默认');
  // 行序——special 组第一行，其后开发组两行，再默认组
  const dataRows = body.querySelectorAll('tr[data-cat]');
  expect(dataRows.length).toBe(4);
  expect(dataRows[0].getAttribute('data-cat')).toBe('m');
  expect(dataRows[1].getAttribute('data-cat')).toBe('a');
  // 名称格 Type 标（空 type 不出标）
  expect(dataRows[1].children[1].querySelector('.cat-type-tag').textContent).toBe('开发');
  expect(dataRows[3].children[1].querySelector('.cat-type-tag')).toBeNull();
});

test('猫卡实时——applyCatState 数据未变跳过重绘 / 行内编辑时不打断', () => {
  window.lastCatSig = '';
  window.applyCatState([{ id: 'x', name: '甲', running: false, port: -1 }]);
  const body = document.querySelector('#catsTable tbody');
  const firstRow = body.querySelector('tr[data-cat]');
  // 同一数据再推——签名相同，跳过重绘（节点仍是同一枚）
  window.applyCatState([{ id: 'x', name: '甲', running: false, port: -1 }]);
  expect(body.querySelector('tr[data-cat]')).toBe(firstRow);
  // 数据变化 → 重绘
  window.applyCatState([{ id: 'x', name: '甲改', running: false, port: -1 }]);
  expect(body.querySelector('tr[data-cat]')).not.toBe(firstRow);
  // 行内编辑进行中——聚焦输入框时跳过（不打断输入）
  const input = body.querySelector('tr[data-cat] input');
  input.focus();
  const rowInEdit = body.querySelector('tr[data-cat]');
  window.applyCatState([{ id: 'x', name: '另一改', running: false, port: -1 }]);
  expect(body.querySelector('tr[data-cat]')).toBe(rowInEdit);
  input.blur();
});
