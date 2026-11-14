// ═══════════════════════════════════════════
// tests/unit/chat-fx.test.mjs —— 独立功能件（fx）回归网（A182）
//
// 覆盖（设计 §三 组 1 判据「各 fx 件」）：
//   ① 声明表 `FX_FEATURES` 五件齐备 + 字段面完整（id / name / inputs / outputs / init）
//   ② `fxBoot` —— 入口查空出声 + 一件抛错不拖累其余（异常隔离）
//   ③ controls —— 轮进行态判据与四按钮派生
//   ④ pending —— 退役（A233 迁入 state 投影——契约 §12.2 ①；渲染断言见 chat-trunk）
//   ⑤ cmdIntent —— 适用判据 / 解码接线 / 展开块挂载 / 未识别上报
//   ⑥ scroll / pet —— 入口就位与幂等（几何与素材面属运行态验收，jsdom 无布局不可测）
//
// 边界：jsdom 无布局引擎——**凡依赖几何 / 素材时序的行为不在本网**（运行态验收承担）；
//       本网守「声明面 ↔ 实现面」「启动链不静默失败」「纯逻辑分支」。
// ═══════════════════════════════════════════

import { beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { bootChatPage, clearAreas } from './chat-env.mjs';

beforeAll(async () => {
    await bootChatPage();
});

beforeEach(() => {
    clearAreas();
});

/** 捕获 console.warn 执行一段逻辑 */
function captureWarn(body) {
    const warned = [];
    const orig = console.warn;
    console.warn = function () { warned.push(Array.prototype.join.call(arguments, ' ')); };
    try {
        body();
    } finally {
        console.warn = orig;
    }
    return warned;
}

// ── ① / ② 声明表与启动 ───────────────────────
describe('声明表与启动（fx/registry）', () => {
    it('五件齐备且字段面完整', () => {
        const table = window.FX_FEATURES;
        expect(table.length).toBe(5);
        expect(table.map((f) => f.id).sort()).toEqual(['cmdIntent', 'controls', 'liveStats', 'pet', 'scroll']);
        for (const f of table) {
            expect(typeof f.name, f.id).toBe('string');
            expect(Array.isArray(f.inputs), f.id + ' inputs').toBe(true);
            expect(Array.isArray(f.outputs), f.id + ' outputs').toBe(true);
            expect(f.inputs.length, f.id + ' 输入源非空').toBeGreaterThan(0);
            expect(typeof f.id, 'id').toBe('string');
            expect(f.id.length, 'id 非空').toBeGreaterThan(0);
        }
    });

    it('init 名可解析或显式 null（不留悬空入口）', () => {
        for (const f of window.FX_FEATURES) {
            if (f.init === null) {
                continue;
            }
            expect(typeof window[f.init], f.id + ' 入口 ' + f.init).toBe('function');
        }
    });

    it('入口查空出声——表内填了不存在的入口名时告警且不中断', () => {
        const table = window.FX_FEATURES;
        const ghost = { id: 'ghost', name: '幽灵件', inputs: ['x'], outputs: ['y'], init: 'fxNoSuchInit' };
        table.push(ghost);
        let warned = [];
        try {
            warned = captureWarn(() => { window.fxBoot(); });
        } finally {
            table.pop();
        }
        expect(warned.some((w) => w.indexOf('独立功能未启动：ghost') >= 0)).toBe(true);
    });

    it('异常隔离——一件 init 抛错不拖累其余（探针件照常执行）', () => {
        const table = window.FX_FEATURES;
        let probeCalls = 0;
        window.fxProbeInit = function () { probeCalls = probeCalls + 1; };
        const bad = { id: 'bad', name: '坏件', inputs: ['x'], outputs: ['y'], init: 'fxBadInit' };
        const probe = { id: 'probe', name: '探针件', inputs: ['x'], outputs: ['y'], init: 'fxProbeInit' };
        window.fxBadInit = function () { throw new Error('boom'); };
        table.splice(1, 0, bad, probe);
        let warned = [];
        try {
            warned = captureWarn(() => { window.fxBoot(); });
        } finally {
            table.splice(1, 2);
        }
        expect(warned.some((w) => w.indexOf('独立功能启动失败：bad') >= 0)).toBe(true);
        expect(probeCalls).toBe(1);
    });
});

// ── ③ controls ───────────────────────────────
describe('按钮态（fx/controls）', () => {
    it('轮进行态判据——空 / idle 为否，其余为是', () => {
        expect(window.fxControlsRunning(null)).toBe(false);
        expect(window.fxControlsRunning({})).toBe(false);
        expect(window.fxControlsRunning({ runState: '' })).toBe(false);
        expect(window.fxControlsRunning({ runState: 'idle' })).toBe(false);
        expect(window.fxControlsRunning({ runState: 'run' })).toBe(true);
        expect(window.fxControlsRunning({ runState: 'tool' })).toBe(true);
    });

    it('元素缺失不抛（防御式）', () => {
        const node = document.getElementById('chatPause');
        const parent = node.parentNode;
        parent.removeChild(node);
        try {
            expect(() => { window.fxControlsApply({ runState: 'run' }); }).not.toThrow();
        } finally {
            parent.appendChild(node);
        }
    });
});

// ── ④b 系统注入不打断输入（A219）──────────────
describe('系统注入不打断输入（A219）', () => {
    it('系统注入块——不切流式（输入区仍在原位）', () => {
        window.chatLiveShow(false);
        window.persistAppend([{ type: 'systemautomessage', payload: { text: '[systemauto]（系统自动）x', source: 'systemauto' } }], true);
        // 临时区未切走——输入区仍在原位（不打断输入）
        expect(window.liveShown).toBe(false);
    });

    it('人工 user 块——切流式（原语义不回归）', () => {
        window.chatLiveShow(false);
        window.persistAppend([{ type: 'user', payload: { text: '我发的' } }], true);
        expect(window.liveShown).toBe(true);
    });

    it('roundsum——切回输入（放行输入；与谁触发本轮无关）', () => {
        window.chatLiveShow(true);
        window.persistAppend([{ type: 'roundsum', payload: { type: 'roundsum', data: {} } }], true);
        expect(window.liveShown).toBe(false);
    });
});

// ── ⑤ cmdIntent ──────────────────────────────
describe('命令解码显示（fx/cmd-intent）', () => {
    it('适用判据——仅 powershell / powershell7', () => {
        expect(window.fxCmdIntentApplies('powershell')).toBe(true);
        expect(window.fxCmdIntentApplies('powershell7')).toBe(true);
        expect(window.fxCmdIntentApplies('text-read')).toBe(false);
        expect(window.fxCmdIntentApplies('')).toBe(false);
    });

    it('解码——非适用工具返回 null（调用方回落骨架）', () => {
        expect(window.fxCmdIntentDecode('text-read', '{"path":"a"}')).toBeNull();
    });

    it('解码——适用工具返回 brief / detail', () => {
        const d = window.fxCmdIntentDecode('powershell', JSON.stringify({ command: 'git status' }));
        expect(d).toBeTruthy();
        expect(typeof d.brief).toBe('string');
        expect(d.brief.length).toBeGreaterThan(0);
        expect(typeof d.detail).toBe('string');
    });

    it('展开块挂载——非空解码结果追加 .cmd-intent', () => {
        const host = window.el('div', 'detail');
        window.fxCmdIntentAttach(host, { detail: '读取文件 a.txt' });
        expect(host.querySelectorAll('.cmd-intent').length).toBe(1);
        expect(host.textContent).toContain('读取文件 a.txt');
    });

    it('展开块挂载——空解码结果零动作', () => {
        const host = window.el('div', 'detail');
        window.fxCmdIntentAttach(host, null);
        expect(host.childNodes.length).toBe(0);
    });
});

// ── ⑥ scroll / pet ───────────────────────────
describe('滚动带与桌宠——入口就位（几何 / 素材面归运行态验收）', () => {
    it('滚动带——入口声明且重复启动幂等', () => {
        expect(typeof window.chatScrollInit).toBe('function');
        expect(() => { window.chatScrollInit(); window.chatScrollInit(); }).not.toThrow();
    });

    it('滚动带——连续同 data-type 折叠成段，异类断段（A222）', () => {
        const mk = (type) => {
            const r = window.el('div', 'chat-row');
            r.setAttribute('data-type', type);
            return { row: r, type: type };
        };
        const items = [mk('text'), mk('text'), mk('toolcard'), mk('toolcard'), mk('toolcard'), mk('text')];
        const segs = window.bandFoldSegs(items);
        expect(segs.map((s) => s.type)).toEqual(['text', 'toolcard', 'text']);
        expect(segs.map((s) => s.row)).toEqual([items[0].row, items[2].row, items[5].row]);
        expect(segs.map((s) => s.last)).toEqual([items[1].row, items[4].row, items[5].row]);
    });

    it('滚动带——段内任一条 .err 整段标失败（A222）', () => {
        const mk = (cls) => {
            const r = window.el('div', 'chat-row tool' + (cls ? ' ' + cls : ''));
            r.setAttribute('data-type', 'toolcard');
            return { row: r, type: 'toolcard' };
        };
        const a = mk('');
        const b = mk('err');
        const c = mk('');
        const segs = window.bandFoldSegs([a, b, c]);
        expect(segs.length).toBe(1);
        expect(segs[0].type).toBe('toolcard');
        expect(segs[0].err).toBe(true);
        expect(segs[0].last).toBe(c.row);
    });

    it('桌宠——入口声明 + 连接态置位不抛', () => {
        for (const n of ['chatPetInit', 'chatPetSync', 'chatPetSetOffline', 'chatPetSay']) {
            expect(typeof window[n], n).toBe('function');
        }
        expect(() => { window.chatPetSetOffline(true); window.chatPetSetOffline(false); }).not.toThrow();
    });

    it('桌宠——皮肤空 / 无匹配不回落默认素材（A251）', () => {
        expect(window.chatPetNormalizeSkin('')).toBe('');
        expect(window.chatPetNormalizeSkin(null)).toBe('');
        expect(window.chatPetNormalizeSkin('no-such-skin')).toBe('');
    });

    it('桌宠——显隐开关按空值关掉容器（A251）', () => {
        const box = document.getElementById('chatPet');
        window.chatPetSetVisible(false);
        expect(box.style.display).toBe('none');
        window.chatPetSetVisible(true);
        expect(box.style.display).toBe('');
    });
});

// ── ⑦ liveStats（流式统计行——契约 §12.8 · 2026-10-06 莎定）────
describe('流式统计行（fx/live-stats）', () => {
    it('文案组装——无活跃段空串；有段三行 label: value（整数直出 + 用时一位小数）', () => {
        expect(window.liveStatsText(null)).toBe('');
        expect(window.liveStatsText({ type: 'thinksse', chars: 340, lines: 12, elapsedMs: 5000 }))
            .toBe('line: 12\nchar: 340\ntime: 5.0s');
    });

    it('用时口径——0.1 秒为最小分度（不足 1s 段照常显示，无「—」占位）', () => {
        expect(window.liveStatsText({ type: 'replysse', chars: 5, lines: 1, elapsedMs: 400 }))
            .toBe('line: 1\nchar: 5\ntime: 0.4s');
        expect(window.liveStatsText({ type: 'replysse', chars: 5, lines: 1, elapsedMs: 1250 }))
            .toBe('line: 1\nchar: 5\ntime: 1.3s');
    });

    it('刷新落盘——无活跃段隐藏 / 有活跃段写入并显示；元素缺失不抛', () => {
        const el = document.getElementById('chatLiveStats');
        window.liveApply({ type: 'empty', context: '' });
        window.liveStatsRefresh();
        expect(el.style.display).toBe('none');

        window.streamState.type = 'thinksse';
        window.streamState.revealed = '甲\n乙';
        // 段用时基准取当前刻——`streamNow()` 为 performance 基准（页面加载起算，值可小于 2000ms），
        // 写 `streamNow() - 2000` 会让 `startedAt` 落到负值、被实现的「>0 表示段已开始」判据挡掉（存量红：
        // 该用例此前依赖时钟足够大）。用时格式化口径由 `liveStatsText` 专项用例覆盖，此处只验刷新落盘与显隐。
        window.streamState.startedAt = window.streamNow();
        window.liveStatsRefresh();
        expect(el.style.display).toBe('');
        expect(el.textContent).toBe('line: 2\nchar: 3\ntime: 0.0s');

        const parent = el.parentNode;
        parent.removeChild(el);
        try {
            expect(() => { window.liveStatsRefresh(); }).not.toThrow();
        } finally {
            parent.appendChild(el);
        }
        window.liveApply({ type: 'empty', context: '' });
        window.liveStatsRefresh();
    });

    it('刷新节拍——单一 100ms 心跳（10 次/秒；机制侧不推送）', () => {
        expect(window.LIVE_STATS_TICK_MS).toBe(100);
    });

    it('轮询取值——有活跃段即显示（line / char 取已吐文本），离段即隐藏', () => {
        const el = document.getElementById('chatLiveStats');
        window.liveApply({ type: 'thinksse', context: '第一行\n第二行' });
        window.liveStatsRefresh();
        expect(el.style.display, '有活跃段即显示（未吐字也属活跃段）').toBe('');
        window.streamDrain();
        window.liveStatsRefresh();
        expect(el.textContent).toContain('line: 2');
        expect(el.textContent).toContain('char: 7');

        window.liveApply({ type: 'empty', context: '' });
        window.liveStatsRefresh();
        expect(el.style.display).toBe('none');
    });
});

// ── ⑦ 资产基址（A362）────────────────────────
describe('桌宠素材 URL——资产基址（A362）', () => {
    it('无基址时保持相对路径（同源，行为不变）', () => {
        window.__ASSET_BASE__ = '';
        window.chatPetSkin = 'default';
        const url = window.chatPetUrl('loop-idle');
        expect(url.indexOf('pet/default/loop-idle.webp')).toBe(0);
    });

    it('有基址时指向 console 端口（跨端口共享同一份缓存）', () => {
        window.__ASSET_BASE__ = 'http://127.0.0.1:8080/';
        window.chatPetSkin = 'default';
        const url = window.chatPetUrl('loop-idle');
        expect(url.indexOf('http://127.0.0.1:8080/pet/default/loop-idle.webp')).toBe(0);
        window.__ASSET_BASE__ = '';
    });
});

describe('缓存键归一（A362 · 防剪枝误删）', () => {
    it('带基址的绝对 URL 与同源相对 URL 归一为同一键', () => {
        window.__ASSET_BASE__ = 'http://127.0.0.1:8080/';
        const abs = window.chatPetUrl('loop-idle');
        window.__ASSET_BASE__ = '';
        const rel = window.chatPetUrl('loop-idle');
        expect(abs.indexOf('http://127.0.0.1:8080/pet/'), '绝对形态带基址').toBe(0);
        expect(rel.indexOf('http'), '同源形态为相对').toBe(-1);
        expect(window.chatPetUrlTail(abs), '归一后同键').toBe(window.chatPetUrlTail(rel));
        delete window.__ASSET_BASE__;
    });
});
