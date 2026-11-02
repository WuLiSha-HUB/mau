// tests/unit/panel-inline-edit.test.mjs —— 配置页行内编辑（2026-10-01 行内编辑轮）
// 覆盖：API 池——可编辑列格内直改、配置 ID 只读、行内「编辑」按钮撤除
//       · 未改动 / 清空 = 不改动（不发请求，清空回落原值）· 改动提交整行载荷（掩码列未改动送空串）
//       · 新猫默认模板·默认前文 List——条目行内可改 + 路径校验 + 拖拽柄独占 draggable
// A299：QQ Bot 池随 QQ 域整段撤除——相关用例同步移除
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

const API_ROW = {
  apiConfigId: '11111111-1111-1111-1111-111111111111',
  displayName: '主端点',
  apiType: 'deepseek',
  endpoint: 'https://a.example/v1',
  defaultModel: 'deepseek-v4-flash',
  isDefault: true,
  apiKey: 'sk-ab****yz',
  hasKey: true
};

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

// 按端点过滤调用记录（提交成功后列表会重载，calls 里会混入列表请求）
function callsTo(url) {
  return calls.filter((c) => c.url === url);
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
  await runGlobalScript(new URL('../../js/panel-packs.js', import.meta.url));
  await tick();   // 首屏加载（loadApis / loadTpl）落地
});

test('编辑模式机件已撤——取消编辑按钮与行内编辑函数都不存在', () => {
  expect(document.getElementById('apiCancelEdit')).toBeNull();
  expect(typeof window.startApiEdit).toBe('undefined');
  expect(typeof window.cancelApiEdit).toBe('undefined');
});

test('API 池——可编辑列是输入框、配置 ID 是纯文本、行内无「编辑」按钮', () => {
  window.renderApis([API_ROW]);
  const tr = document.querySelector('#apisTable tbody tr');
  expect(tr.children.length).toBe(9);
  expect(tr.children[0].querySelector('input')).toBeNull();   // 最左默认列——状态 + 动作，无输入框
  expect(tr.children[1].querySelector('input').value).toBe('主端点');
  expect(tr.children[2].querySelector('input')).toBeNull();
  expect(tr.children[2].textContent).toBe(API_ROW.apiConfigId);
  expect(tr.children[3].querySelector('input').value).toBe('deepseek');
  expect(tr.children[4].querySelector('input').value).toBe('https://a.example/v1');
  expect(tr.children[5].querySelector('input').value).toBe('deepseek-v4-flash');
  expect(tr.children[6].querySelector('input').value).toBe('sk-ab****yz');
  // 最右测试列——未测态是单个「测试」按钮（行内零报告内容，结果全走弹窗）
  expect(tr.children[8].querySelector('input')).toBeNull();
  expect(tr.children[8].querySelector('button').textContent).toBe('测试');
  const labels = [];
  for (const b of tr.querySelectorAll('button')) { labels.push(b.textContent); }
  expect(labels.indexOf('编辑')).toBe(-1);
  expect(labels.indexOf('删除')).toBeGreaterThanOrEqual(0);
});

test('API 池——最左默认列：默认行紫星 + 已是默认；非默认行灰星 + 设为默认按钮', () => {
  window.renderApis([API_ROW]);
  const tdOn = document.querySelector('#apisTable tbody tr').children[0];
  expect(tdOn.querySelector('span').className).toBe('api-star-on');
  expect(tdOn.querySelector('span').textContent).toBe('★');
  expect(tdOn.textContent).toContain('已是默认');
  expect(tdOn.querySelector('button')).toBeNull();

  window.renderApis([Object.assign({}, API_ROW, { isDefault: false })]);
  const tdOff = document.querySelector('#apisTable tbody tr').children[0];
  expect(tdOff.querySelector('span').className).toBe('api-star-off');
  expect(tdOff.querySelector('button').textContent).toBe('设为默认');
  expect(tdOff.textContent).not.toContain('已是默认');
});

test('API 池——未改动 / 清空都不发请求（清空回落原值）；改动提交整行', async () => {
  captureFetch();
  window.renderApis([API_ROW]);
  const tr = document.querySelector('#apisTable tbody tr');
  const nameInput = tr.children[1].querySelector('input');
  const keyInput = tr.children[6].querySelector('input');

  nameInput.dispatchEvent(new Event('change'));
  await tick();
  expect(callsTo('/api/v1/llm-apis/edit').length).toBe(0);

  nameInput.value = '   ';
  nameInput.dispatchEvent(new Event('change'));
  expect(nameInput.value).toBe('主端点');
  await tick();
  expect(callsTo('/api/v1/llm-apis/edit').length).toBe(0);

  keyInput.value = '';
  keyInput.dispatchEvent(new Event('change'));
  expect(keyInput.value).toBe('sk-ab****yz');
  await tick();
  expect(callsTo('/api/v1/llm-apis/edit').length).toBe(0);

  nameInput.value = '备用端点';
  nameInput.dispatchEvent(new Event('change'));
  await tick();
  const edits = callsTo('/api/v1/llm-apis/edit');
  expect(edits.length).toBe(1);
  expect(edits[0].body).toEqual({
    apiConfigId: API_ROW.apiConfigId,
    displayName: '备用端点',
    apiType: 'deepseek',
    endpoint: 'https://a.example/v1',
    defaultModel: 'deepseek-v4-flash',
    apiKey: ''
  });
});

test('API 池——Key 列真改动才送新值', async () => {
  captureFetch();
  window.renderApis([API_ROW]);
  const tr = document.querySelector('#apisTable tbody tr');
  const keyInput = tr.children[6].querySelector('input');
  keyInput.value = 'sk-brand-new-key';
  keyInput.dispatchEvent(new Event('change'));
  await tick();
  const edits = callsTo('/api/v1/llm-apis/edit');
  expect(edits.length).toBe(1);
  expect(edits[0].body.apiKey).toBe('sk-brand-new-key');
});

test('新猫默认模板·默认前文——条目行内可改；未改动 / 清空 / 非法路径都不改列表', () => {
  window.tplInjectList = ['ccbp:L1/Tree.md', 'ccbp:L1/Wisdom.md'];
  window.renderTplInject();
  const rows = document.querySelectorAll('#tplInject > div');
  expect(rows.length).toBe(2);
  // 拖拽柄独占 draggable（行级 draggable 会吃掉输入框内拖选）
  expect(rows[0].getAttribute('draggable')).toBeNull();
  expect(rows[0].querySelector('span').getAttribute('draggable')).toBe('true');

  const inp = rows[0].querySelector('input');
  expect(inp.value).toBe('ccbp:L1/Tree.md');

  inp.dispatchEvent(new Event('change'));
  expect(window.tplInjectList[0]).toBe('ccbp:L1/Tree.md');

  inp.value = '';
  inp.dispatchEvent(new Event('change'));
  expect(inp.value).toBe('ccbp:L1/Tree.md');
  expect(window.tplInjectList[0]).toBe('ccbp:L1/Tree.md');

  inp.value = '不是完整路径';
  inp.dispatchEvent(new Event('change'));
  expect(inp.value).toBe('ccbp:L1/Tree.md');
  expect(window.tplInjectList[0]).toBe('ccbp:L1/Tree.md');
  expect(document.getElementById('tplMsg').textContent).toContain('路径拒绝');

  inp.value = 'ccbp:L2/Job/CSharp';
  inp.dispatchEvent(new Event('change'));
  expect(window.tplInjectList[0]).toBe('ccbp:L2/Job/CSharp');
  expect(window.tplInjectList.length).toBe(2);
  const again = document.querySelectorAll('#tplInject > div')[0].querySelector('input');
  expect(again.value).toBe('ccbp:L2/Job/CSharp');
});

test('新猫默认模板·默认前文——移除按钮仍生效', () => {
  window.tplInjectList = ['ccbp:L1/Tree.md', 'ccbp:L1/Wisdom.md'];
  window.renderTplInject();
  const rm = document.querySelectorAll('#tplInject > div')[0].querySelector('button');
  rm.click();
  expect(window.tplInjectList).toEqual(['ccbp:L1/Wisdom.md']);
  expect(document.querySelectorAll('#tplInject > div').length).toBe(1);
});
