// ═══════════════════════════════════════════
// tests/unit/chat-live-stream.test.mjs —— 流式呈现机制（2026-10-06）
//
// 覆盖（契约 §12.5 呈现口径）：
//   ① 前缀增量——新全集以「已显 + 待吐」为前缀时只入队尾巴，**行不重建**（增量而非全量）
//   ② 打字机——入队不即显；走步按窗口吐字，单步增量夹逼在 [1, STREAM_FRAME_MAX]，窗口内追平
//   ③ 前缀三态——续接（尾巴增长）· 重排（已显前缀命中、尾巴改写，不回收已吐字）· 重置（全不命中 → 清区从头吐）
//   ④ 进行中光标——流式段期间恒挂 `streaming`，离段（empty / 换他件）即摘
//   ⑤ 挂载面——`STREAM_TYPES` 白名单（thinksse + replysse）同挂同一套效果；toolrun / empty 仍整体替换
//
// 环境口径：jsdom 无帧回调 → 生产降级为「当场铺完」；本网**装 rAF 桩**（回调收进队列、不自动跑），
//          使打字机中间态可控——走步由用例直喂时刻（`streamPump(ms)`），收口走 `streamDrain()`。
// ═══════════════════════════════════════════

import { beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { bootChatPage, clearAreas } from './chat-env.mjs';

/// 帧回调桩队列——stream.js 见 rAF 存在即「排帧」（不落铺完路径），回调由用例掌控
let rafQueue = [];
let rafSeq = 0;

beforeAll(async () => {
    await bootChatPage();
    globalThis.requestAnimationFrame = function (fn) {
        rafSeq = rafSeq + 1;
        rafQueue.push(fn);
        return rafSeq;
    };
    globalThis.cancelAnimationFrame = function () { };
});

beforeEach(() => {
    rafQueue = [];
    // 状态复位——empty 走整体替换路径（停泵 + 状态归零），再清区（用例间隔离）
    window.liveApply({ type: 'empty', context: '' });
    clearAreas();
});

/// 临时面板当前正文（挂载件写入点 = 块体内文本节点）
function panelText() {
    return document.getElementById('chatLivePanel').textContent;
}

const TOOLRUN_ONE = '[{"name":"time","arguments":"{}","toolIndex":1,"toolTotal":1}]';

describe('流式呈现机制——前缀增量', () => {
    it('首段入队不即显；走步推进后逐字上身（行一次建成、后续复用）', () => {
        const panel = document.getElementById('chatLivePanel');
        window.liveApply({ type: 'thinksse', context: '甲' });
        const row = panel.querySelector('.chat-row');
        expect(row, '行已建成').toBeTruthy();
        expect(panelText(), '入队未吐').toBe('');
        expect(rafQueue.length, '已排帧').toBe(1);

        const t0 = window.streamNow();
        window.streamPump(t0 + 20);
        expect(panelText()).toBe('甲');
        window.streamPump(t0 + 40);
        expect(panelText(), '吐完不重复写').toBe('甲');
    });

    it('续接——新全集只把尾巴入队（已显保持、行复用）', () => {
        const panel = document.getElementById('chatLivePanel');
        window.liveApply({ type: 'thinksse', context: '甲' });
        window.streamDrain();
        const row = panel.querySelector('.chat-row');

        window.liveApply({ type: 'thinksse', context: '甲乙丙' });
        expect(panelText(), '已显不被回收').toBe('甲');
        window.streamDrain();
        expect(panelText()).toBe('甲乙丙');
        expect(panel.querySelector('.chat-row'), '行未重建（增量而非全量）').toBe(row);
    });

    it('重排——已显前缀命中、尾巴改写：按已显重排队列', () => {
        window.liveApply({ type: 'thinksse', context: '甲乙丙' });
        window.streamPump(window.streamNow() + 20);
        expect(panelText(), '已吐 1 字').toBe('甲');

        window.liveApply({ type: 'thinksse', context: '甲丁' });
        expect(panelText(), '已吐字不回收').toBe('甲');
        window.streamDrain();
        expect(panelText()).toBe('甲丁');
    });

    it('重置——前缀全不命中（新一轮）：清区后从头吐', () => {
        window.liveApply({ type: 'thinksse', context: '旧内容' });
        window.streamDrain();
        expect(panelText()).toBe('旧内容');

        window.liveApply({ type: 'thinksse', context: '新一轮' });
        expect(panelText(), '清区').toBe('');
        window.streamDrain();
        expect(panelText()).toBe('新一轮');
    });
});

describe('流式呈现机制——打字机节拍', () => {
    it('大积压线性铺开：单步 1..STREAM_FRAME_MAX 字、窗口内追平（不是一两步灌满）', () => {
        const panel = document.getElementById('chatLivePanel');
        const t0 = window.streamNow();
        window.liveApply({ type: 'thinksse', context: '字'.repeat(100) });

        let prev = 0;
        let drainedAt = -1;
        for (let i = 1; i <= 30; i = i + 1) {
            window.streamPump(t0 + i * 16);
            const now = panelText().length;
            expect(now - prev, '单步增量上限').toBeLessThanOrEqual(window.STREAM_FRAME_MAX);
            expect(now, '单调不减').toBeGreaterThanOrEqual(prev);
            prev = now;
            if (now === 100 && drainedAt < 0) {
                drainedAt = i;
            }
        }
        expect(prev, '全部上身').toBe(100);
        expect(drainedAt, '有打字过程（非瞬间灌满）').toBeGreaterThan(6);
        expect(drainedAt, '在刷新窗口内追平').toBeLessThanOrEqual(20);
    });

    it('进行中光标——流式段期间恒挂 streaming；离段（empty / 换他件）即摘', () => {
        const panel = document.getElementById('chatLivePanel');
        window.liveApply({ type: 'thinksse', context: '甲' });
        const body = panel.querySelector('.chat-plain');
        expect(body.classList.contains('streaming'), '进段即挂').toBe(true);
        window.streamDrain();
        expect(body.classList.contains('streaming'), '吐完仍挂（段未结束——段间空档仍是思考中）').toBe(true);

        window.liveApply({ type: 'empty', context: '' });
        expect(panel.querySelectorAll('.chat-row').length).toBe(0);
        window.liveApply({ type: 'thinksse', context: '乙' });
        expect(panel.querySelector('.chat-plain').classList.contains('streaming'), '新段挂上').toBe(true);
        window.liveApply({ type: 'toolrun', context: TOOLRUN_ONE });
        expect(panel.querySelector('.streaming'), '离段（换他件）无光标').toBeNull();
    });
});

describe('挂载面——白名单两件同挂', () => {
    it('STREAM_TYPES 声明面 = thinksse + replysse', () => {
        expect(Object.keys(window.STREAM_TYPES).sort()).toEqual(['replysse', 'thinksse']);
    });

    it('replysse 同挂——前缀增量 + 行复用 + 段期光标（与 thinksse 同一份机制）', () => {
        const panel = document.getElementById('chatLivePanel');
        window.liveApply({ type: 'replysse', context: '回复' });
        const row = panel.querySelector('.chat-row');
        expect(panelText(), '入队未吐').toBe('');
        expect(panel.querySelector('.chat-plain').classList.contains('streaming'), '进段即挂').toBe(true);
        window.streamDrain();
        expect(panelText()).toBe('回复');

        window.liveApply({ type: 'replysse', context: '回复流继续。' });
        expect(panelText(), '已显不被回收').toBe('回复');
        expect(panel.querySelector('.chat-row'), '行未重建').toBe(row);
        window.streamDrain();
        expect(panelText()).toBe('回复流继续。');
    });

    it('两件互不串状态——thinksse → replysse 换型重建、各自从空起吐', () => {
        const panel = document.getElementById('chatLivePanel');
        window.liveApply({ type: 'thinksse', context: '想' });
        window.streamDrain();
        const row1 = panel.querySelector('.chat-row');

        window.liveApply({ type: 'replysse', context: '答' });
        expect(panelText(), '换型清区').toBe('');
        expect(panel.querySelector('.chat-row'), '换型重建行').not.toBe(row1);
        window.streamDrain();
        expect(panelText()).toBe('答');
    });
});

describe('非挂载件——整体替换不受影响', () => {
    it('toolrun 即时落卡；切回挂载件从空起吐（无打字机残留）', () => {
        const panel = document.getElementById('chatLivePanel');
        window.liveApply({ type: 'thinksse', context: '思' });
        window.streamDrain();

        window.liveApply({ type: 'toolrun', context: TOOLRUN_ONE });
        expect(panel.querySelectorAll('.chat-row').length).toBe(1);
        expect(panelText().length, '卡已落文本（逐工具渲染件）').toBeGreaterThan(0);

        window.liveApply({ type: 'thinksse', context: '再想' });
        expect(panelText(), '重建后从空起吐').toBe('');
        window.streamDrain();
        expect(panelText()).toBe('再想');
    });

    it('empty——清区且停泵（无残余积压）', () => {
        const panel = document.getElementById('chatLivePanel');
        window.liveApply({ type: 'thinksse', context: '甲' });
        window.liveApply({ type: 'empty', context: '' });
        expect(panel.querySelectorAll('.chat-row').length).toBe(0);
        expect(window.streamState.queue).toBe('');
        window.streamPump(1000);
        expect(panelText(), '停泵后不再吐字').toBe('');
    });
});

describe('断线收尾——chatLiveAbort（A206）', () => {
    it('清临时区并停打字机——半句残影与进行中光标不留到重连', () => {
        const panel = document.getElementById('chatLivePanel');
        window.liveApply({ type: 'replysse', context: '半句话' });
        window.streamDrain();
        expect(panelText()).toBe('半句话');
        window.chatLiveAbort();
        expect(panelText(), '面板已清').toBe('');
        expect(document.getElementById('chatinput').getAttribute('data-live'), '流式态标记已摘').toBe('empty');
        expect(window.streamStats(), '无活跃流式段').toBe(null);
    });
});

describe('统计出口口径（2026-10-06 微调 · 莎定）', () => {
    it('字符 / 行数取已吐文本——积压不计入（读数随打字机增长）', () => {
        window.liveApply({ type: 'replysse', context: '甲\n乙丙丁' });
        window.streamPump(window.streamState.stepBase);
        let st = window.streamStats();
        expect(st.chars, '积压未吐 → 0').toBe(0);
        expect(st.lines, '空段不报行数').toBe(0);

        window.streamPump(window.streamState.stepBase + 20);
        st = window.streamStats();
        expect(st.chars, '已吐部分上身').toBeGreaterThan(0);
        expect(st.chars, '未吐部分不计入').toBeLessThan(5);

        window.streamDrain();
        st = window.streamStats();
        expect(st.chars, '吐完 = 全文').toBe(5);
        expect(st.lines, '换行 1 + 1').toBe(2);
    });

    it('段用时——自本段第一次成形起算（0.1s 分度由显示件落）', () => {
        window.liveApply({ type: 'replysse', context: '甲' });
        const st = window.streamStats();
        expect(st.type).toBe('replysse');
        expect(st.elapsedMs).toBeGreaterThanOrEqual(0);
        expect(window.liveStatsText(st)).toBe('line: 0\nchar: 0\ntime: 0.0s');
    });
});

describe('临时区增量传输（A362）——后端 delta 与本地累积', () => {
    it('整段 + 增量拼回全文——呈现链仍拿全集（与原整段行为一致）', () => {
        const panel = document.getElementById('chatLivePanel');
        window.liveApply({ type: 'replysse', context: '甲乙' });
        window.streamDrain();
        expect(panelText()).toBe('甲乙');

        window.liveApply({ type: 'replysse', mode: 'delta', base: 2, context: '丙丁' });
        window.streamDrain();
        expect(panelText(), '增量累积成全文').toBe('甲乙丙丁');
    });

    it('连续多次增量——每次只带尾巴，累积不回退', () => {
        const panel = document.getElementById('chatLivePanel');
        window.liveApply({ type: 'thinksse', context: '想' });
        window.liveApply({ type: 'thinksse', mode: 'delta', base: 1, context: '一' });
        window.liveApply({ type: 'thinksse', mode: 'delta', base: 2, context: '二' });
        window.streamDrain();
        expect(panelText()).toBe('想一二');
    });

    it('基线不符——保留已收内容 + 可见缺口标记（不丢历史、不静默）', () => {
        const warns = [];
        const oldWarn = window.warn;
        window.warn = function (msg) {
            warns.push(String(msg));
        };
        window.liveTransferStats.baseMiss = 0;
        window.liveApply({ type: 'thinksse', context: '甲' });
        window.liveApply({ type: 'thinksse', mode: 'delta', base: 9, context: '乙' });
        window.warn = oldWarn;
        window.streamDrain();
        const text = panelText();
        expect(warns.length, '首次失配出声').toBeGreaterThan(0);
        expect(text.indexOf('甲'), '已收内容不被丢弃').toBeGreaterThanOrEqual(0);
        expect(text.indexOf('乙'), '尾巴续接上').toBeGreaterThanOrEqual(0);
        expect(text.indexOf('缺口'), '缺口有可见标记').toBeGreaterThanOrEqual(0);
        expect(window.liveTransferStats.baseMiss, '失配已计数').toBeGreaterThan(0);
    });

    it('传输统计——整段 / 增量帧分别计数', () => {
        window.liveTransferStats.fullFrames = 0;
        window.liveTransferStats.deltaFrames = 0;
        window.liveApply({ type: 'replysse', context: '甲' });
        window.liveApply({ type: 'replysse', mode: 'delta', base: 1, context: '乙' });
        window.streamDrain();
        const st = window.liveTransferStats;
        expect(st.fullFrames, '整段 1 帧').toBe(1);
        expect(st.deltaFrames, '增量 1 帧').toBe(1);
        expect(panelText(), '累积 = 全文').toBe('甲乙');
    });

    it('type 变更却只带增量——出声并重建（帧序异常可见）', () => {
        const warns = [];
        const oldWarn = window.warn;
        window.warn = function (msg) {
            warns.push(String(msg));
        };
        window.liveTransferStats.typeMiss = 0;
        window.liveApply({ type: 'thinksse', context: '想' });
        window.liveApply({ type: 'replysse', mode: 'delta', base: 1, context: '答' });
        window.warn = oldWarn;
        window.streamDrain();
        expect(warns.length, '出声').toBeGreaterThan(0);
        expect(panelText()).toBe('答');
    });
});
