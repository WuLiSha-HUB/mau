// ═══════════════════════════════════════════
// tests/unit/chat-blocks.test.mjs —— 契约块型结构面回归网（A182）
//
// 覆盖（契约 §12.5 的**结构面**）：
//   ① 声明表 11 行（persist 8 + live 3）——实现面：persist 8 件 + live 2 件 + `toolrun` 复用工具卡件 + `empty` 不产元素
//   ② 行的形态 / 行语义 / data-type 全部由 `BLOCK_DECL` 表驱动（渲染件不自拼类名）
//   ③ 形态契约（2026-10-05 气泡化改判 · 莎定）——**persist 八类全气泡 / live 三件朴素**；
//      间隙文本（后端 `gap_text`）收包即归一为 `text`（落点 `persist.js::TYPE_ALIAS`），本网按前端分型口径断言
//   ④ 取用口回落语义（未声明 type → 朴素件 + assistant 行 + reply 刻度）
//
// 边界：**不测块内载荷语义**（各 type 的字段渲染是否好看 / 完整）——那是 A198「逐 type 核对」的范围；
//       本网守的是「声明面 ↔ 实现面不漂移」与「结构契约不破」。
// ═══════════════════════════════════════════

import { beforeAll, describe, expect, it } from 'vitest';
import { bootChatPage } from './chat-env.mjs';

beforeAll(async () => {
    await bootChatPage();
});

const PERSIST_TYPES = ['user', 'text', 'reason', 'toolcard', 'retry', 'error', 'inject_report', 'roundsum'];
const LIVE_TYPES = ['replysse', 'thinksse'];

/// 块体类例外——完成态思考自持 `.chat-think`、工具族自持 `.chat-tool`（`BLOCK_DECL` 未建模这两族块体；
/// 形态判据不受影响——`form:'plain'` 语义 =「不作对话内容读」，与二者一致，故非渲染缺陷）。
/// `thinksse` 走通用路径（2026-10-06 临时最小态——无内部结构，仅 `.chat-plain` 载体）。
/// 🔴 这是**显式登记**而非放宽断言：新增例外必须来此登记，否则测试变红。
/// 口径统一（块体族是否入表 / `body` 列是否改全类名）归 A198「逐 type 核对」。
const BODY_EXCEPTIONS = {
    'reason': 'chat-think',
    'toolcard': 'chat-tool',
    'toolrun': 'chat-tool'
};

/** 断言——行元素符合 `BLOCK_DECL` 声明（行类 / data-type / 块体类） */
function expectRowContract(node, type) {
    const decl = window.BLOCK_DECL[type];
    expect(decl, type).toBeTruthy();
    expect(node.classList.contains('chat-row'), type + ' 行基类').toBe(true);
    const rowTokens = decl.row.split(' ');
    for (const tok of rowTokens) {
        expect(node.classList.contains(tok), type + ' 行语义 ' + tok).toBe(true);
    }
    expect(node.getAttribute('data-type'), type + ' data-type').toBe(type);
    const exception = BODY_EXCEPTIONS[type];
    if (exception) {
        expect(node.querySelectorAll('.' + exception).length, type + ' 块体类 ' + exception).toBeGreaterThan(0);
        return;
    }
    const form = window.formClass(type);
    const holder = node.querySelectorAll('.' + form)[0];
    expect(holder, type + ' 形态类 ' + form).toBeTruthy();
    const bodyTokens = window.bodyClass(type).split(' ');
    for (const tok of bodyTokens) {
        expect(holder.classList.contains(tok), type + ' 块体语义 ' + tok).toBe(true);
    }
}

describe('块型渲染——结构契约', () => {
    it('persist 八类——空载荷不抛 + 行契约齐备', () => {
        for (const t of PERSIST_TYPES) {
            const fn = window.PERSIST_RENDERERS[t];
            expect(typeof fn, t + ' 渲染函数').toBe('function');
            const node = fn({});
            expect(node, t + ' 产出').toBeTruthy();
            expectRowContract(node, t);
        }
    });

    it('live 件——空载荷不抛 + 行契约齐备（replysse / thinksse；toolrun 复用工具卡渲染件）', () => {
        for (const t of LIVE_TYPES) {
            const fn = window.LIVE_RENDERERS[t];
            expect(typeof fn, t + ' 渲染函数').toBe('function');
            const node = fn({});
            expect(node, t + ' 产出').toBeTruthy();
            expectRowContract(node, t);
        }
        // toolrun——live.js 逐卡调持久区渲染件（两区同源；进行中 / 结果态由载荷 result 有无分支）
        const card = window.buildToolBlock({ name: 'time', arguments: '{}', toolIndex: 1, toolTotal: 1 }, null, 'toolrun');
        expect(card).toBeTruthy();
        expectRowContract(card, 'toolrun');
    });

    it('实现面覆盖声明表（新增 type 必须声明面与实现面齐备）', () => {
        const declared = Object.keys(window.BLOCK_DECL).length;
        // 实现面 = persist 表 + live 表 + toolrun（live.js 逐卡复用工具卡渲染件；empty 不入声明表——不产元素）
        const special = ['toolrun'];
        const implemented = Object.keys(window.PERSIST_RENDERERS).length + Object.keys(window.LIVE_RENDERERS).length + special.length;
        expect(implemented).toBe(declared);
    });

    it('persist 真实调用路径——第二参恒为条目对象（防参数语义重载回归）', () => {
        // 回归锚（2026-10-06 真机抓获）：toolcard 曾把第二参当行身份字符串，persistRender 喂条目对象后
        // data-type 变 "[object Object]" 且形态回落——测试必须走 persistRender 真实调用形态，不用单参直调
        const card = window.persistRender({
            type: 'toolcard',
            msgIndex: 1,
            payload: { name: 'time', arguments: '{}', result: 'ok', toolIndex: 1, toolTotal: 1, order: '-1' }
        });
        expect(card.getAttribute('data-type')).toBe('toolcard');
        expect(card.classList.contains('tool')).toBe(true);
        expect(card.querySelectorAll('.chat-bubble').length).toBe(1);
        const txt = window.persistRender({ type: 'text', msgIndex: 3, payload: { text: 'hi' } });
        expect(txt.getAttribute('data-type')).toBe('text');
        expect(txt.querySelector('.node-actions')).toBeTruthy();
    });
});

describe('形态契约', () => {
    it('persist 十类全气泡，live 三件朴素（A219 起；A235 增 roundsummary）', () => {
        const bubbles = Object.keys(window.BLOCK_DECL).filter((t) => window.BLOCK_DECL[t].form === 'bubble');
        expect(bubbles.sort()).toEqual(['error', 'inject_report', 'reason', 'retry', 'roundsum', 'roundsummary', 'systemautomessage', 'text', 'toolcard', 'user']);
        const plains = Object.keys(window.BLOCK_DECL).filter((t) => window.BLOCK_DECL[t].form === 'plain');
        expect(plains.sort()).toEqual(['replysse', 'thinksse', 'toolrun']);
    });

    it('取用口——形态 / 块体 / 行 / 刻度四口语义', () => {
        expect(window.formClass('user')).toBe('chat-bubble');
        expect(window.formClass('toolcard')).toBe('chat-bubble');
        expect(window.formClass('toolrun')).toBe('chat-plain');
        expect(window.bodyClass('error')).toBe('chat-bubble error');
        expect(window.bodyClass('user')).toBe('chat-bubble');
        expect(window.tickRole('user')).toBe('user');
        expect(window.tickRole('roundsum')).toBe('sum');
    });

    it('未声明 type 回落——朴素件 + assistant 行 + reply 刻度（不出气泡）', () => {
        expect(window.formClass('nope')).toBe('chat-plain');
        expect(window.bodyClass('nope')).toBe('chat-plain');
        expect(window.tickRole('nope')).toBe('reply');
        expect(window.tickRole('')).toBe('reply');
        const row = window.blockRow('nope');
        expect(row.classList.contains('assistant')).toBe(true);
        expect(row.getAttribute('data-type')).toBe('nope');
    });
});

describe('系统注入块（A219——systemautomessage）', () => {
    it('渲染——独立块型；行变体 sysmsg；气泡一件', () => {
        const node = window.persistRender({ type: 'systemautomessage', msgIndex: 2, payload: { text: '[systemauto]注入', source: 'systemauto' } });
        expect(node.classList.contains('sysmsg')).toBe(true);
        expect(node.getAttribute('data-type')).toBe('systemautomessage');
        expect(node.querySelectorAll('.chat-bubble').length).toBe(1);
    });

    it('人工 user 块——不带行变体类', () => {
        const human = window.persistRender({ type: 'user', msgIndex: 1, payload: { text: '人工' } });
        expect(human.classList.contains('user')).toBe(true);
        expect(human.classList.contains('sysmsg')).toBe(false);
    });

    it('刻度角色——systemautomessage 走 user-sys；user / text 不变', () => {
        const sys = window.persistRender({ type: 'systemautomessage', msgIndex: 3, payload: { text: 'x', source: 'sleep' } });
        expect(window.scrollTickRole(sys)).toBe('user-sys');
        const human2 = window.persistRender({ type: 'user', msgIndex: 4, payload: { text: 'y' } });
        expect(window.scrollTickRole(human2)).toBe('user');
        const text = window.persistRender({ type: 'text', msgIndex: 5, payload: { text: 'z' } });
        expect(window.scrollTickRole(text)).toBe('reply');
    });

    it('独立渲染——题头 `System · 字符 · 行 · 来源` + 两档结构（压缩档默认）', () => {
        const node = window.persistRender({ type: 'systemautomessage', msgIndex: 1, payload: { text: 'a\nb\nc', source: 'sleep' } });
        expect(node.querySelector('.sy-label').textContent).toBe('System');
        const stat = node.querySelector('.sy-stat').textContent;
        expect(stat).toContain('5 字符');
        expect(stat).toContain('3 行');
        expect(stat).toContain('sleep');
        expect(node.querySelector('.sy-peek')).toBeTruthy();
        expect(node.querySelector('.sy-full')).toBeTruthy();
        expect(node.querySelector('.chat-sys').classList.contains('full')).toBe(false);
    });

    it('切档——sysToggle 切 .full（独立于 Think 的 thinkToggle / .chat-think）', () => {
        const node = window.persistRender({ type: 'systemautomessage', msgIndex: 1, payload: { text: 'a\nb\nc', source: 'timer' } });
        const box = node.querySelector('.chat-sys');
        expect(node.querySelector('.chat-think')).toBeNull();
        window.sysToggle(box);
        expect(box.classList.contains('full')).toBe(true);
        window.sysToggle(box);
        expect(box.classList.contains('full')).toBe(false);
    });
});

describe('text 块操作条（P6b 复归 · 2026-10-06）', () => {
    it('带 msgIndex——挂操作条（回滚按钮 + 悬浮提示；分支已删——A326）', () => {
        const node = window.buildTextBlock({ text: 'hi' }, { type: 'text', msgIndex: 7 });
        const bar = node.querySelector('.node-actions');
        expect(bar).toBeTruthy();
        expect(node.querySelectorAll('.node-btn').length).toBe(1);
        expect(node.querySelector('.node-btn-rollback').textContent).toBe('⟲ 回滚');
        expect(node.querySelector('.node-btn-rollback').title).toContain('回滚');
        expect(node.querySelector('.node-btn-fork')).toBe(null);
    });

    it('msgIndex 缺失 / -1——不挂操作条（独立块与旧块零回归）', () => {
        expect(window.buildTextBlock({ text: 'hi' }, { type: 'text', msgIndex: -1 }).querySelector('.node-actions')).toBe(null);
        expect(window.buildTextBlock({ text: 'hi' }, {}).querySelector('.node-actions')).toBe(null);
    });

    it('回滚按钮——确认后经指令总线投递 session.rollback <msgIndex>', () => {
        const sent = [];
        window.confirm = () => true;
        window.fetch = (url, opt) => {
            sent.push(JSON.parse(opt.body).text);
            return Promise.resolve({ json: () => Promise.resolve({ ok: true }) });
        };
        const node = window.buildTextBlock({ text: 'hi' }, { type: 'text', msgIndex: 7 });
        node.querySelector('.node-btn-rollback').click();
        expect(sent).toEqual(['session.rollback 7']);
    });
});

describe('轮末结算载荷（2026-10-06 · 莎定）——四项分片', () => {
    it('命中率 → Hit → Miss → Down；Hit = prompt − miss；数值走 num-frac 小数分片', () => {
        const node = window.buildRoundSumBlock({
            data: {
                prompt: 1000000, completion: 50000, cacheHit: 900000, miss: 100000,
                toolCount: 0, requests: 0, elapsedMs: 1000, phases: {}
            }
        });
        const tok = node.querySelector('.rs-tok');
        expect(tok.querySelector('.ci-rate').textContent).toBe('90.00%');
        expect(tok.querySelector('.ci-hit').textContent).toBe('Hit');
        expect(tok.querySelector('.ci-miss').textContent).toBe('Miss');
        expect(tok.querySelector('.ci-down').textContent).toBe('Down');
        expect(tok.textContent).toContain('（🎯90.00%）    Hit900.00K   Miss100.00K   Down50.00K');
        const fracs = Array.from(tok.querySelectorAll('.num-frac')).map((n) => n.textContent);
        expect(fracs).toEqual(['.00', '.00', '.00', '.00']);
        // 弱化件（2026-10-06 · 莎定；同日微调：K/M 单位随整数原色）——括号（2）· 百分号（1）包 `.rs-dim`
        expect(tok.querySelectorAll('.rs-dim').length).toBe(3);
    });
});

// ── 行级标记——失败 / timeback 域区间（2026-10-11） ──────────
describe('行级标记——失败 / timeback 域区间', () => {
    it('失败工具卡——行加 .err + data-tool', () => {
        const node = window.buildToolBlock({ name: 'cs-build', arguments: '{}', result: 'ERR|BUILD_TIMEOUT|x' }, {}, 'toolcard');
        expect(node.classList.contains('err')).toBe(true);
        expect(node.getAttribute('data-tool')).toBe('cs-build');
    });

    it('正常工具卡——无 .err（原色）', () => {
        const node = window.buildToolBlock({ name: 'file-tree', arguments: '{}', result: 'ok' }, {}, 'toolcard');
        expect(node.classList.contains('err')).toBe(false);
    });

    it('timeback 域区间——start..back 之间（不含两端）加 .tb-scope', () => {
        const box = document.getElementById('chatMsgs');
        box.textContent = '';
        const mk = (name, res) => window.buildToolBlock({ name: name, arguments: '{}', result: res || 'ok' }, {}, 'toolcard');
        box.appendChild(mk('timeback-start'));
        box.appendChild(mk('file-tree'));
        box.appendChild(mk('cs-build'));
        box.appendChild(mk('timeback-back'));
        box.appendChild(mk('time-list'));
        window.tbScopeMark();
        expect(box.children[0].classList.contains('tb-scope')).toBe(false);
        expect(box.children[1].classList.contains('tb-scope')).toBe(true);
        expect(box.children[2].classList.contains('tb-scope')).toBe(true);
        expect(box.children[3].classList.contains('tb-scope')).toBe(false);
        expect(box.children[4].classList.contains('tb-scope')).toBe(false);
    });

    it('timeback 失败回执不算发起——不开区间（A330）', () => {
        const box = document.getElementById('chatMsgs');
        box.textContent = '';
        const mk = (name, res) => window.buildToolBlock({ name: name, arguments: '{}', result: res || 'ok' }, {}, 'toolcard');
        box.appendChild(mk('timeback-start', 'ERR|TIMEBACK_PROFILE|域类型门禁——域内不放行该工具'));
        box.appendChild(mk('file-tree'));
        window.tbScopeMark();
        expect(box.children[0].classList.contains('err')).toBe(true);
        expect(box.children[0].classList.contains('tb-scope')).toBe(false);
        expect(box.children[1].classList.contains('tb-scope')).toBe(false);
    });
});
