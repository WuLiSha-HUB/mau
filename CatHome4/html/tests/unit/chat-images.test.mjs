// ═══════════════════════════════════════════
// tests/unit/chat-images.test.mjs —— 对话图片包裹回归网（2026-10-06 位置放宽）
//
// 覆盖：
//   ① 解析口 `imgSplit`——包裹可在消息**任意位置**（行粒度）；命中返回 { items, before, after }
//   ② 渲染面——text 泡（前段 MD + 图片组 + 后段 MD，顺序即原文顺序）· user 泡同源
//   ③ 严格门保留——内部杂行 / 缺段尾 / 零条目不成立；前一处残缺继续向后找合法包裹
//   ④ 零回归——无包裹走原路径
//
// 边界：不测取图端点（宿主契约）；本网守「解析判据 + 渲染顺序」。
// ═══════════════════════════════════════════

import { beforeAll, describe, expect, it } from 'vitest';
import { bootChatPage } from './chat-env.mjs';

beforeAll(async () => {
    await bootChatPage();
});

const MID = 'A200 完成。\n\n[image-open]\n图片276-1：ccbp:x.png\n[image-end]\n\n以下是说明。';
const HEAD = '[image-open]\n图片92-1：ccbp:a.png\n图片92-2：ccbp:b.png\n[image-end]\n\n正文一\n正文二';

describe('图片包裹解析（imgSplit）', () => {
    it('包裹在中间——before / after 各自取出', () => {
        const r = window.imgSplit(MID);
        expect(r.items.length).toBe(1);
        expect(r.items[0].ref).toBe('276-1');
        expect(r.items[0].path).toBe('ccbp:x.png');
        expect(r.before).toBe('A200 完成。');
        expect(r.after).toBe('以下是说明。');
    });

    it('包裹在开头——before 为空，after 为正文', () => {
        const r = window.imgSplit(HEAD);
        expect(r.items.length).toBe(2);
        expect(r.before).toBe('');
        expect(r.after).toBe('正文一\n正文二');
    });

    it('无包裹——原样返回（before = 原文，items 空）', () => {
        const r = window.imgSplit('普通文本\n第二行');
        expect(r.items.length).toBe(0);
        expect(r.before).toBe('普通文本\n第二行');
        expect(r.after).toBe('');
    });

    it('严格门——内部杂行 / 零条目不成立', () => {
        expect(window.imgSplit('[image-open]\n说明行\n图片1-1：ccbp:a.png\n[image-end]').items.length).toBe(0);
        expect(window.imgSplit('[image-open]\n\n图片1-1：ccbp:a.png\n[image-end]').items.length).toBe(0);
        expect(window.imgSplit('[image-open]\n[image-end]\n正文').items.length).toBe(0);
    });

    it('前一处残缺——继续向后找合法包裹', () => {
        const text = '[image-open]\n图片1-1：ccbp:a.png\n没有段尾\n\n[image-open]\n图片2-1：ccbp:b.png\n[image-end]\n尾部';
        const r = window.imgSplit(text);
        expect(r.items.length).toBe(1);
        expect(r.items[0].ref).toBe('2-1');
        expect(r.before).toContain('没有段尾');
        expect(r.after).toBe('尾部');
    });

    it('代码围栏内的包裹——照样出图，紧邻围栏行一并吞掉', () => {
        const text = '前文\n\n```\n[image-open]\n图片1-1：ccbp:a.png\n[image-end]\n```\n\n后文';
        const r = window.imgSplit(text);
        expect(r.items.length).toBe(1);
        expect(r.items[0].ref).toBe('1-1');
        expect(r.before).toBe('前文');
        expect(r.after).toBe('后文');
    });

    it('围栏外的包裹——前后段各自内容原样保留', () => {
        const text = '```\ncode\n```\n\n[image-open]\n图片3-1：ccbp:c.png\n[image-end]\n尾部';
        const r = window.imgSplit(text);
        expect(r.items.length).toBe(1);
        expect(r.items[0].ref).toBe('3-1');
        expect(r.before).toContain('code');
        expect(r.after).toBe('尾部');
    });

    it('包裹后紧跟代码块（包裹在围栏外）——真代码块的围栏行不被吞', () => {
        const text = '[image-open]\n图片2-1：ccbp:b.png\n[image-end]\n\n```js\nvar a = 1;\n```';
        const r = window.imgSplit(text);
        expect(r.items.length).toBe(1);
        expect(r.before).toBe('');
        expect(r.after).toBe('```js\nvar a = 1;\n```');
    });
});

describe('图片包裹渲染', () => {
    it('text 泡——中间包裹 → 前段 MD + 图片组 + 后段 MD（顺序即原文）', () => {
        const row = window.buildTextBlock({ text: MID });
        const bubble = row.querySelector('.chat-bubble');
        expect(bubble.querySelectorAll('.chat-imgs .chat-img').length).toBe(1);
        const blocks = bubble.querySelectorAll('.md-block');
        expect(blocks.length).toBe(2);
        expect(blocks[0].textContent).toContain('A200 完成。');
        expect(blocks[1].textContent).toContain('以下是说明。');
    });

    it('text 泡——开头包裹 → 仅图片组 + 单个 MD 块', () => {
        const row = window.buildTextBlock({ text: HEAD });
        const bubble = row.querySelector('.chat-bubble');
        expect(bubble.querySelectorAll('.chat-imgs .chat-img').length).toBe(2);
        const blocks = bubble.querySelectorAll('.md-block');
        expect(blocks.length).toBe(1);
        expect(blocks[0].textContent).toContain('正文一');
    });

    it('图片 URL——受控根寻址走取图端点单一出口', () => {
        const row = window.buildTextBlock({ text: MID });
        const img = row.querySelector('.chat-imgs img');
        expect(img.getAttribute('src')).toBe('/api/v1/cache-image/ccbp:x.png');
    });

    it('user 泡——同源三段式（前段 + 图片组 + 后段）', () => {
        const row = window.buildUserBlock({ text: MID });
        const bubble = row.querySelector('.chat-bubble');
        expect(bubble.querySelectorAll('.chat-imgs .chat-img').length).toBe(1);
        const texts = bubble.querySelectorAll('.chat-user-text');
        expect(texts.length).toBe(2);
        expect(texts[0].textContent).toContain('A200 完成。');
        expect(texts[1].textContent).toContain('以下是说明。');
    });

    it('text 泡——围栏内包裹照样出图，不留空代码块、不吞后段正文', () => {
        const text = '```\n[image-open]\n图片1-1：ccbp:a.png\n[image-end]\n```\n\n**正文**';
        const row = window.buildTextBlock({ text: text });
        const bubble = row.querySelector('.chat-bubble');
        expect(bubble.querySelectorAll('.chat-imgs .chat-img').length).toBe(1);
        expect(bubble.querySelectorAll('pre').length).toBe(0);
        expect(bubble.querySelectorAll('.md-block').length).toBe(1);
        expect(bubble.querySelector('strong').textContent).toBe('正文');
    });

    it('零回归——无包裹走原路径（单 MD 块 / user 纯文本）', () => {
        const row = window.buildTextBlock({ text: '普通回复' });
        const bubble = row.querySelector('.chat-bubble');
        expect(bubble.querySelectorAll('.chat-imgs').length).toBe(0);
        expect(bubble.querySelectorAll('.md-block').length).toBe(1);
        const urow = window.buildUserBlock({ text: '普通提问' });
        expect(urow.textContent).toContain('普通提问');
    });
});
