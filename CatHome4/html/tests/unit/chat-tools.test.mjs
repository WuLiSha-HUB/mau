// ═══════════════════════════════════════════
// tests/unit/chat-tools.test.mjs —— 工具卡声明层 + 骨架层回归网（A200）
//
// 覆盖：
//   ① 声明面 ↔ 落位面不漂移——`TOOL_DECL`（各件自注册）与 `TOOL_SKELETONS`（落位表）键集必须一致，
//      且同为运行态工具池 78 件（基准 = 宿主 `ToolOrderTable` 全量登记表）
//   ② 骨架分派——登记工具走对应骨架；未登记工具走形态探测回落（禁止空白）
//   ③ 专属骨架——`info`（分类摊平）/ `catinfo`（每猫一行）
//   ④ 折叠行四级——前缀（图标 / 批次 / 失败）· 声明层 headline · 骨架兜底 · 规模后缀
//   ⑤ 声明层覆盖生效——输入段自然语言意图行 / 输出段自然语言化
//
// 边界：不测运行态观感（配色 / 间距）——那是真机验收面；本网守「声明与骨架的实现面不漂移」。
// ═══════════════════════════════════════════

import { beforeAll, describe, expect, it } from 'vitest';
import { bootChatPage } from './chat-env.mjs';

beforeAll(async () => {
    await bootChatPage();
});

// 工具池基准——72 件（按工具组；新增工具须同批登记落位表 + 落件 + 本表）
const POOL = [
    // TextCat 7
    'text-read', 'text-read_lines', 'text-read_between', 'text-write', 'text-append', 'text-replace', 'text-grep',
    // FileCat 6
    'file-tree', 'file-find', 'file-move', 'file-delete', 'file-copy', 'file-version',
    // CsCat 13
    'cs-check', 'cs-build', 'cs-list', 'cs-read', 'cs-find_ref', 'cs-find',
    'cs-patch', 'cs-member', 'cs-comment', 'cs-dead', 'cs-comment_check', 'cs-format', 'cs-test',
    // MauCat 4
    'mau-verify', 'mau-gen', 'mau-proj', 'mau-setup',
    // ConfigCat 6
    'config-list', 'config-get', 'config-set', 'config-reset', 'config-cat-get', 'config-cat-set',
    // BrowserCat 6
    'browser-open', 'browser-read', 'browser-eval', 'browser-shot', 'browser-tabs', 'browser-headful',
    // PsCat 2 · TempToolCat 2
    'powershell', 'powershell7', 'temp-info', 'temp-exec',
    // Majordomo 5
    'restart-full', 'restart-incr', 'restart-host', 'majordomo-cmd', 'majordomo-catinfo',
    // WebCat 4 · VisionCat 2
    'web-search', 'web-ds-search', 'web-fetch', 'web-fetch-jobs', 'image-analyze', 'image-inject',
    // AudioCat 2 · PyCat 3
    'audio-play', 'audio-speak', 'py-run', 'py-list', 'py-service',
    // DbCat 5（A274）
    'db-put', 'db-query', 'db-get', 'db-update', 'db-delete',
    // 内置 11
    'info', 'Note', 'time', 'random', 'pack', 'host-reload', 'host-flows', 'sleep', 'timer', 'timeback-start', 'timeback-back'
].sort();

/** 工具卡载荷——参数 JSON + 结果原文 */
function card(name, args, result, extra) {
    const p = { name: name, arguments: JSON.stringify(args || {}) };
    if (result !== undefined) {
        p.result = result;
    }
    if (extra) {
        Object.assign(p, extra);
    }
    return p;
}

/** 折叠行元素 */
function summaryOf(payload) {
    const node = window.buildToolBlock(payload, 'toolcard');
    return node.querySelector('summary.tn');
}

describe('声明层——覆盖与注册', () => {
    it('落位表 = 工具池 78 件', () => {
        expect(Object.keys(window.TOOL_SKELETONS).sort()).toEqual(POOL);
        expect(POOL.length).toBe(78);
    });

    it('声明件与落位表键集一致（有落位必有件，防「有壳无肉」）', () => {
        expect(Object.keys(window.TOOL_DECL).sort()).toEqual(POOL);
    });

    it('每件声明非空对象，且字段类型合法', () => {
        for (const name of POOL) {
            const d = window.toolDeclOf(name);
            expect(typeof d, name + ' 声明').toBe('object');
            for (const key of ['inputLines', 'outputLines', 'badge', 'headline', 'inputImages', 'outputImages']) {
                if (d[key] !== undefined) {
                    expect(typeof d[key], name + ' 声明.' + key).toBe('function');
                }
            }
        }
    });
});

describe('骨架分派与回落', () => {
    it('登记工具按落位表走骨架（永不返回 null）', () => {
        for (const name of POOL) {
            const body = window.toolBody(card(name, {}, '原文'));
            expect(body, name + ' toolBody').toBeTruthy();
            expect(Array.isArray(body.segs), name + ' segs').toBe(true);
        }
    });

    it('未登记工具走形态探测——JSON 可解析 → json 骨架；否则 text 骨架', () => {
        const jsonBody = window.toolBody(card('unknown-tool', {}, '{"a":1}'));
        expect(jsonBody.segs.length).toBeGreaterThan(0);
        const textBody = window.toolBody(card('unknown-tool', {}, '纯文本结果'));
        expect(textBody.segs.length).toBeGreaterThan(0);
    });

    it('空工具名 → null（唯一不渲染的例外）', () => {
        expect(window.toolBody(card('', {}, 'x'))).toBeNull();
    });

    it('骨架图标 / 中文名——未登记回落空串（调用方用 ❓ 显式暴露）', () => {
        expect(window.iconOf('cs-check')).toBe('🩺');
        expect(window.iconOf('unknown-tool')).toBe('');
        expect(window.SKEL_LABELS['info']).toBe('环境信息');
        expect(window.toolIconOf('text-replace')).toBe('🔄');
        expect(window.toolIconOf('unknown-tool')).toBe('❓');
    });
});

describe('专属骨架——info / catinfo（逐字段摊平）', () => {
    it('info——分类 JSON 块 → 键值行（猫 / 版本 / LLM / 可见根 …）', () => {
        const result = JSON.stringify({
            cat: '639258885922650133',
            version: { version: '1.7.55', build: '2026-10-06 17:53:55' },
            time: { now: '2026-10-06 18:27:22' },
            llm: { protocol: 'deepseek', host: 'api.deepseek.com', model: 'deepseek-flash', source: '猫绑定' },
            endpoint: { chat: 'http://127.0.0.1:8082', panel: 'http://127.0.0.1:8080' },
            roots: [{ id: 'mau', writable: true, note: '', path: 'C:/gitee/mau' }],
            tokens: { context: 41925 },
            packs: [{ key: 'overwork', desc: '收工加载包' }]
        });
        const node = window.buildToolBlock(card('info', {}, result), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('猫');
        expect(text).toContain('版本');
        expect(text).toContain('1.7.55');
        expect(text).toContain('LLM');
        expect(text).toContain('可见根');
        expect(text).toContain('mau(rw)');
        expect(text).toContain('加载包');
        // 缺类不显示（无 qqbot 键 → 不出现该行）
        expect(text).not.toContain('QQBot');
    });

    it('catinfo——整块 JSON → 每猫一行（运行态 / 相位 / 前文 / 活跃）', () => {
        const result = JSON.stringify({
            count: 2,
            cats: [
                { name: '大管家', id: 'abc', port: 8082, running: true, phase: 'Idle', runState: 'idle', context: 1200, contextCount: 30, lastActiveAt: Date.now() - 5000, round: 3, msgCount: 12, noteActive: true, special: true },
                { name: '猫二', running: false }
            ]
        });
        const node = window.buildToolBlock(card('majordomo-catinfo', {}, result), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('大管家');
        expect(text).toContain('★');
        expect(text).toContain('运行中 :8082');
        expect(text).toContain('前文 1200 tokens');
        expect(text).toContain('Note 激活');
        expect(text).toContain('静默');
    });
});

describe('折叠行', () => {
    it('前缀——单发图标 / 并发批次 [icon n/m] / 失败 ⚠️', () => {
        expect(window.toolPrefix({ name: 'cs-check', toolTotal: 1, toolIndex: 1 }, false)).toBe('🩺 ');
        expect(window.toolPrefix({ name: 'cs-check', toolTotal: 3, toolIndex: 2 }, false)).toBe('[🩺 2/3] ');
        expect(window.toolPrefix({ name: 'cs-check', toolTotal: 3, toolIndex: 1 }, true)).toBe('[⚠️ 1/3] ');
        expect(window.toolPrefix({ name: 'cs-check' }, true)).toBe('⚠️ ');
    });

    it('执行序徽标——载荷带 order 才渲染（缺字段零猜测）', () => {
        const withOrder = summaryOf(card('time', {}, '2026-10-06 18:00:00', { order: '-1' }));
        expect(withOrder.querySelector('.chat-order')).toBeTruthy();
        expect(withOrder.querySelector('.chat-order').textContent).toBe('⚙-1');
        const without = summaryOf(card('time', {}, '2026-10-06 18:00:00'));
        expect(without.querySelector('.chat-order')).toBeNull();
    });

    it('原始工具名——载荷 name 直显（紧跟执行序徽标）', () => {
        const s = summaryOf(card('text-read_lines', { path: 'x' }, 'ok'));
        const name = s.querySelector('.chat-toolname');
        expect(name).toBeTruthy();
        expect(name.textContent).toBe('text-read_lines');
    });

    it('工具用时——durMs > 0 显示（走 fmtMs）；缺字段 / -1 不写', () => {
        const withDur = summaryOf(card('cs-build', { path: 'x' }, 'ok', { durMs: 1200 }));
        expect(withDur.querySelector('.chat-dur')).toBeTruthy();
        expect(withDur.querySelector('.chat-dur').textContent).toBe(' · 1.2s');
        expect(summaryOf(card('cs-build', { path: 'x' }, 'ok', { durMs: -1 })).querySelector('.chat-dur')).toBeNull();
        expect(summaryOf(card('cs-build', { path: 'x' }, 'ok')).querySelector('.chat-dur')).toBeNull();
    });

    it('声明层 headline——读结构化头产出自然语言（cs-build）', () => {
        const head = '{"ok":true,"tool":"cs-build","target":"CatHome4.Core","exit":0,"errors":0,"warnings":0,"ms":2100,"scope":"single"}';
        const s = summaryOf(card('cs-build', { path: 'mau:CatHome4/CatHome4.Core/CatHome4.Core.csproj' }, head + '\nBuild succeeded.'));
        const text = s.textContent;
        expect(text).toContain('编译 CatHome4.Core.csproj · 成功 0 错 0 警 · 2.1s');
    });

    it('PS 双线——变体标签（PS 5.1 / PS 7）', () => {
        const s = summaryOf(card('powershell7', { command: 'git status' }, 'ok'));
        const tag = s.querySelector('.ps-tag');
        expect(tag).toBeTruthy();
        expect(tag.textContent).toBe('PS 7');
        expect(tag.classList.contains('ps7')).toBe(true);
    });

    it('骨架兜底——无 headline 声明的工具走「骨架中文名 · 工具名」', () => {
        expect(window.toolFallbackHeadline({ name: 'text-read' })).toBe('读取文件 · text-read');
        expect(window.toolFallbackHeadline({ name: 'unknown-tool' })).toBe('unknown-tool');
    });

    it('规模后缀——小结果不标；大结果 / 截断才标', () => {
        expect(window.toolResultSuffix({ chars: 120, truncated: false })).toBe('');
        expect(window.toolResultSuffix({ chars: 4096, truncated: false })).toBe(' · 4.10K 字符');
        expect(window.toolResultSuffix({ chars: 9000, truncated: true, timeout: true })).toContain('已达上限');
    });

    it('参数解析失败——不抛；未登记工具回落通用键值表并显式「（无参数）」', () => {
        const node = window.buildToolBlock({ name: 'unknown-tool', arguments: 'not-json', result: 'x' }, 'toolcard');
        expect(node.textContent).toContain('（无参数）');
    });

    it('参数解析失败但有声明——走声明层意图行，缺值显式「(未指定)」', () => {
        const node = window.buildToolBlock({ name: 'cs-check', arguments: 'not-json', result: 'x' }, 'toolcard');
        expect(node.textContent).toContain('(未指定)');
    });
});

describe('声明层覆盖——段内容生效', () => {
    it('输入段——自然语言意图行替代裸参数键值表', () => {
        const head = '{"ok":true,"tool":"text-read_lines","start":1,"end":20}';
        const node = window.buildToolBlock(card('text-read_lines', { path: 'ccbp:L1/Tree.md', start: 1, end: 20 }, head + '\n1: a'), 'toolcard');
        expect(node.textContent).toContain('按行读取 ccbp:L1/Tree.md · L1~20');
    });

    it('输出段——cs-member 批量落盘逐条列行号区间', () => {
        const head = '{"ok":true,"tool":"cs-member","op":"insert","target":"Foo","file":"Foo.cs","items":2,"entries":[{"start":10,"end":14,"kind":"Method"},{"start":16,"end":20,"kind":"Property"}]}';
        const node = window.buildToolBlock(card('cs-member', { op: 'insert', class: 'Foo', codes: ['a', 'b'] }, head), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('#1 L10-14 · Method');
        expect(text).toContain('#2 L16-20 · Property');
    });

    it('失败态——ERR 前缀 → 折叠行 ⚠️ + 块体 err 类', () => {
        const node = window.buildToolBlock(card('cs-build', { path: 'x.csproj' }, 'ERR|BUILD_TIMEOUT|dotnet build 超时'), 'toolcard');
        expect(node.querySelector('.chat-tool').classList.contains('err')).toBe(true);
        expect(node.textContent).toContain('⚠️');
    });

    it('输入图片段——声明 inputImages 的工具渲染缩略图组（路径交 images 单一出口）', () => {
        const node = window.buildToolBlock(card('image-analyze', { path: 'mau:shot.png', question: '看什么' }, '描述文本'), 'toolcard');
        expect(node.textContent).toContain('识别图片 mau:shot.png');
        expect(node.textContent).toContain('提示词 看什么');
    });

    it('输出图片段——声明 outputImages 的工具有缩略图组（如 browser-shot 截图）', () => {
        const res = 'file=C:/Users/x/Data/browser/c1/out/1-shot.png | full=false | px=800x600';
        const node = window.buildToolBlock(card('browser-shot', { full: false }, res), 'toolcard');
        const group = node.querySelector('.chat-imgs');
        expect(group).toBeTruthy();
        expect(group.querySelector('img')).toBeTruthy();
    });
});

describe('流式工具计时（A263）', () => {
    it('在途卡（结果未定稿）显示自增用时——锚随首现时刻记忆', () => {
        const p = { name: 'text-read', arguments: '{"path":"ccbp:L1/Tree.md"}', toolIndex: 1, toolTotal: 2 };
        const node = window.buildToolBlock(p, 'toolcard');
        const live = node.querySelector('.chat-dur-live');
        expect(live).not.toBeNull();
        expect(live.getAttribute('data-since')).toMatch(/^\d+$/);
        expect(live.textContent).toMatch(/^ · \d+\.\ds$/);
    });

    it('同一在途卡重复渲染——锚不变（live 帧每帧重建，读数不归零）', () => {
        const p = { name: 'cs-build', arguments: '{"path":"x.csproj"}', toolIndex: 2, toolTotal: 3 };
        const a = window.buildToolBlock(p, 'toolcard').querySelector('.chat-dur-live').getAttribute('data-since');
        const b = window.buildToolBlock(p, 'toolcard').querySelector('.chat-dur-live').getAttribute('data-since');
        expect(b).toBe(a);
    });

    it('定稿卡无 durMs 不显示用时；有 durMs 走既有口径', () => {
        const done = window.buildToolBlock(card('text-read', { path: 'x' }, 'OK|done'), 'toolcard');
        expect(done.querySelector('.chat-dur-live')).toBeNull();
        expect(done.querySelector('.chat-dur')).toBeNull();
        const timed = window.buildToolBlock(card('text-read', { path: 'x' }, 'OK|done', { durMs: 1200 }), 'toolcard');
        expect(timed.querySelector('.chat-dur')).not.toBeNull();
        expect(timed.querySelector('.chat-dur-live')).toBeNull();
    });
});

describe('DbCat——库名与条目摘要（A274）', () => {
    /** 成功回执三段：结构化头 + 摘要行 + JSON 载荷 */
    function dbResult(tool, lib, summary, payload) {
        return '{"ok":true,"tool":"' + tool + '","target":"' + lib + '"}\n' + summary + '\n' + payload;
    }

    it('db-query——三数进折叠行，条目逐条列出（ID / 标题 / 更新时间）', () => {
        const res = dbResult('db-query', 'smriti', '命中 2 / 活跃 5',
            '{"total":5,"matched":2,"shown":2,"items":[{"id":7,"title":"甲","updatedAt":"2026-10-20 22:10:00"},{"id":3,"title":"乙","updatedAt":"2026-10-19 09:00:00"}]}');
        const node = window.buildToolBlock(card('db-query', { lib: 'smriti', keyword: '甲' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('查询 smriti · 「甲」');
        expect(text).toContain('查询库 smriti · 关键词 甲');
        expect(text).toContain('命中 2 / 活跃 5');
        expect(text).toContain('#7 · 甲 · 2026-10-20 22:10:00');
        expect(text).toContain('#3 · 乙');
    });

    it('db-query——无命中显式占位，截断时标「仅列前 N 条」', () => {
        const empty = dbResult('db-query', 'smriti', '命中 0 / 活跃 5', '{"total":5,"matched":0,"shown":0,"items":[]}');
        expect(window.buildToolBlock(card('db-query', { lib: 'smriti' }, empty), 'toolcard').textContent).toContain('（无命中条目）');
        const cut = dbResult('db-query', 'smriti', '命中 9 / 活跃 20',
            '{"total":20,"matched":9,"shown":1,"items":[{"id":1,"title":"甲","updatedAt":"2026-10-20 22:10:00"}]}');
        expect(window.buildToolBlock(card('db-query', { lib: 'smriti' }, cut), 'toolcard').textContent).toContain('仅列前 1 条（命中 9）');
    });

    it('db-get——版本时间行 + 正文段', () => {
        const res = dbResult('db-get', 'smriti', '条目 #7',
            '{"id":7,"title":"甲","body":"单行正文","v":2,"createdAt":"2026-10-18 10:00:00","updatedAt":"2026-10-20 22:10:00"}');
        const node = window.buildToolBlock(card('db-get', { lib: 'smriti', key: '甲' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('读取 smriti · 标题 甲 · #7 v2');
        expect(text).toContain('#7 · v2');
        expect(text).toContain('更新 2026-10-20 22:10:00');
        expect(text).toContain('正文 · 1 行');
        expect(text).toContain('单行正文');
    });

    it('db-get——按 ID 定位时折叠行标 ID 前缀', () => {
        const res = dbResult('db-get', 'smriti', '条目 #7',
            '{"id":7,"title":"甲","body":"单行正文","v":1,"createdAt":"2026-10-18 10:00:00","updatedAt":"2026-10-20 22:10:00"}');
        const node = window.buildToolBlock(card('db-get', { lib: 'smriti', mode: 'id', key: '7' }, res), 'toolcard');
        expect(node.textContent).toContain('读取 smriti · ID 7');
    });

    it('db-put / db-update / db-delete——摘要带库名与条目 ID', () => {
        const put = window.buildToolBlock(card('db-put', { lib: 'smriti', title: '甲', body: 'x' },
            dbResult('db-put', 'smriti', '条目已写入: #7', '{"id":7,"v":1}')), 'toolcard');
        expect(put.textContent).toContain('库 smriti · 条目 #7（v1）已写入');
        const upd = window.buildToolBlock(card('db-update', { lib: 'smriti', title: '甲', body: 'y' },
            dbResult('db-update', 'smriti', '条目已更新: #7 v2', '{"id":7,"v":2}')), 'toolcard');
        expect(upd.textContent).toContain('条目 #7 已更新至 v2');
        const del = window.buildToolBlock(card('db-delete', { lib: 'smriti', title: '甲' },
            dbResult('db-delete', 'smriti', '条目已删除（墓碑保留）: #7', '{"id":7}')), 'toolcard');
        expect(del.textContent).toContain('条目 #7 已删除（墓碑保留，可审计）');
    });

    it('失败态——回落骨架原文，机器码可见', () => {
        const node = window.buildToolBlock(card('db-put', { lib: 'smriti', title: '甲', body: 'x' },
            'ERR|DB_TITLE_EXISTS|标题已存在: 甲'), 'toolcard');
        expect(node.textContent).toContain('DB_TITLE_EXISTS');
        expect(node.querySelector('.chat-tool').classList.contains('err')).toBe(true);
    });
});
