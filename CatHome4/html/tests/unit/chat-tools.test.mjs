// ═══════════════════════════════════════════
// tests/unit/chat-tools.test.mjs —— 工具卡声明层 + 骨架层回归网（A200）
//
// 覆盖：
//   ① 声明面 ↔ 落位面不漂移——`TOOL_DECL`（各件自注册）与 `TOOL_SKELETONS`（落位表）键集必须一致，
//      且同为运行态工具池 94 件（基准 = 宿主 `ToolOrderTable` 全量登记表）
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

// 工具池基准——90 件（按工具组；新增工具须同批登记落位表 + 落件 + 本表）
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
    // DbAdmin 3（库级管理——明细分列 / 建库 / 删库）
    'dbAdmin-libs', 'dbAdmin-create', 'dbAdmin-drop',
    // TsCat 9（A294 五件 + A332 工程质量面四件）
    'ts-check', 'ts-list', 'ts-read', 'ts-find-ref', 'ts-patch',
    'ts-info', 'ts-lint', 'ts-test', 'ts-format',
    // GroupCat 8（A306 / A309 / A322——群聊 / 派单回执 / 会话重建 / 组内配置面）
    'group-info', 'group-inbox', 'group-post', 'group-delegate', 'group-newsession',
    'group-catinfo', 'group-catcfg-get', 'group-catcfg-set',
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
    it('落位表 = 工具池 98 件', () => {
        expect(Object.keys(window.TOOL_SKELETONS).sort()).toEqual(POOL);
        expect(POOL.length).toBe(98);
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

describe('组内配置面（A322）', () => {
    it('group-catinfo——折叠行读运行态，正文摊平条目键值', () => {
        const head = '{"ok":true,"tool":"group-catinfo","cat":"cat2","running":true,"port":8083}';
        const entry = '{"id":"cat2","name":"猫二","running":true,"port":8083,"context":1234,"round":2}';
        const res = head + '\n运行态：猫二（cat2）| 运行中 | 端口 8083\n' + entry;
        const node = window.buildToolBlock(card('group-catinfo', { group: 'g1', cat: 'cat2' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('查看运行态 cat2 @ g1');
        expect(text).toContain('运行态 cat2 · 运行中 :8083');
        expect(text).toContain('运行态：猫二（cat2）');
        expect(text).toContain('context = 1234');
    });

    it('group-catcfg-get——折叠行数字段，正文逐字段行（跳过摘要行）', () => {
        const head = '{"ok":true,"tool":"group-catcfg-get","target":"g1"}';
        const body = 'cat.cfg | cat2\n  id=cat2\n  displayName=猫二\n  themeColor=3';
        const node = window.buildToolBlock(card('group-catcfg-get', { group: 'g1', cat: 'cat2' }, head + '\n' + body), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('读取配置 cat2 @ g1');
        expect(text).toContain('配置 cat2 · 3 字段');
        expect(text).toContain('displayName=猫二');
    });

    it('group-catcfg-set——输入意图行带值预览，折叠行标已写入', () => {
        const head = '{"ok":true,"tool":"group-catcfg-set","cat":"cat2","field":"themeColor"}';
        const res = head + '\n配置已写入：cat2 / themeColor\ncat.cfg.set | cat2 | themeColor=3 已更新';
        const node = window.buildToolBlock(card('group-catcfg-set', { group: 'g1', cat: 'cat2', field: 'themeColor', value: '3' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('写入配置 cat2 · themeColor = 3');
        expect(text).toContain('配置 cat2 · themeColor 已写入');
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

describe('DbAdmin——库级管理渲染', () => {
    /** 管理面回执三段（无 target 时省略该键） */
    function adminResult(tool, target, summary, payload) {
        var head = '{"ok":true,"tool":"' + tool + '"' + (target ? ',"target":"' + target + '"' : '') + '}';
        return head + '\n' + summary + (payload ? '\n' + payload : '');
    }

    it('dbAdmin-libs——逐库一行（条目 / 墓碑 / 坏行 / 字节 / 创建 + 损坏标记）', () => {
        const res = adminResult('dbAdmin-libs', '', '库 2（授权内 2 / 共 3——1 个未授权已隐藏）',
            '{"libs":[{"id":"smriti","createdAt":"2026-10-21 13:10:33","entries":105,"tombstones":20,"badLines":0,"bytes":179699,"broken":false},'
            + '{"id":"probe","createdAt":"2026-10-20 10:00:00","entries":0,"tombstones":0,"badLines":2,"bytes":900,"broken":true}]}');
        const node = window.buildToolBlock(card('dbAdmin-libs', {}, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('库明细 · 2 个库');
        expect(text).toContain('列举库明细（按猫授权过滤）');
        expect(text).toContain('smriti · 条目 105 · 墓碑 20 · 坏行 0 · 175.5 KB');
        expect(text).toContain('创建 2026-10-21 13:10:33');
        expect(text).toContain('probe · 条目 0');
        expect(text).toContain('900 B');
        expect(text).toContain('⚠️ 损坏');
    });

    it('dbAdmin-libs——授权内无库时显式占位', () => {
        const res = adminResult('dbAdmin-libs', '', '库 0（授权内 0 / 共 3——3 个未授权已隐藏）', '{"libs":[]}');
        expect(window.buildToolBlock(card('dbAdmin-libs', {}, res), 'toolcard').textContent).toContain('（授权内无库）');
    });

    it('dbAdmin-create——库名与创建时刻自然语言化', () => {
        const res = adminResult('dbAdmin-create', 'smriti2', '库已建立: smriti2',
            '{"id":"smriti2","createdAt":"2026-10-21 15:30:00"}');
        const node = window.buildToolBlock(card('dbAdmin-create', { lib: 'smriti2' }, res), 'toolcard');
        expect(node.textContent).toContain('新建库 smriti2 · 已建立');
        expect(node.textContent).toContain('库 smriti2 已建立');
        expect(node.textContent).toContain('创建时刻 · 2026-10-21 15:30:00');
    });

    it('dbAdmin-drop——确认串入输入段，回收路径可见（可恢复）', () => {
        const res = adminResult('dbAdmin-drop', 'tmpdb', '库已移入回收区: tmpdb',
            '{"id":"tmpdb","trash":"C:/db/_trash/tmpdb-20261021_153000"}');
        const node = window.buildToolBlock(card('dbAdmin-drop', { lib: 'tmpdb', confirm: 'tmpdb' }, res), 'toolcard');
        expect(node.textContent).toContain('删除库 tmpdb · 已移入回收区');
        expect(node.textContent).toContain('确认串 tmpdb');
        expect(node.textContent).toContain('已移入回收区（可恢复）');
        expect(node.textContent).toContain('回收路径 · C:/db/_trash/tmpdb-20261021_153000');
    });

    it('失败态——授权面拒绝落 err 块且机器码可见（不空白）', () => {
        const node = window.buildToolBlock(card('dbAdmin-create', { lib: 'x1' },
            'ERR|TOOL_FORBIDDEN|工具不在当前授权面: dbAdmin-create'), 'toolcard');
        expect(node.textContent).toContain('TOOL_FORBIDDEN');
        expect(node.querySelector('.chat-tool').classList.contains('err')).toBe(true);
    });
});

describe('TsCat——TS 编码工具面渲染（A294）', () => {
    /** TS 工具回执——单段 JSON（无结构化头） */
    function tsResult(o) {
        return JSON.stringify(o);
    }

    it('ts-check——折叠行读 counts，正例显式「无类型错误」', () => {
        const res = tsResult({
            ok: true, mode: 'project', tsconfig: 'tsconfig.json', files: 3,
            errors: [], warnings: [], counts: { errors: 0, warnings: 0 }
        });
        const node = window.buildToolBlock(card('ts-check', { path: 'mau:tools/ts/fixtures/tsconfig.json' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('TS 类型检查 mau:tools/ts/fixtures/tsconfig.json');
        expect(text).toContain('TS 检查 tsconfig.json · 0 错 0 警 · 3 文件');
        expect(text).toContain('（无类型错误）');
    });

    it('ts-check——负例逐条诊断（文件:行:列 TS码 消息），单文件降级标注', () => {
        const res = tsResult({
            ok: true, mode: 'single', files: 1,
            errors: [{ file: 'src/broken.ts', line: 15, col: 14, code: 2322, message: "Type 'string' is not assignable to type 'number'." }],
            warnings: [], counts: { errors: 1, warnings: 0 }
        });
        const node = window.buildToolBlock(card('ts-check', { path: 'mau:tools/ts/fixtures-err/src/broken.ts' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('1 错 0 警 · 1 文件 · 单文件');
        expect(text).toContain('src/broken.ts:15:14 TS2322');
        expect(text).toContain("Type 'string' is not assignable to type 'number'.");
    });

    it('ts-list——模块判定与逐条声明（种类 / 名称 / 签名 / 行号）', () => {
        const res = tsResult({
            ok: true, file: 'C:/x/model.ts', isModule: true, kind: 'all', total: 2,
            items: [
                { kind: 'class', name: 'EntityStore', signature: 'export class EntityStore', line: 25 },
                { kind: 'method', name: 'add', signature: 'public add(entity: Entity): void', line: 34 }
            ]
        });
        const node = window.buildToolBlock(card('ts-list', { file: 'mau:tools/ts/fixtures/src/model.ts' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('结构 model.ts · 2 项 · 模块');
        expect(text).toContain('[class] EntityStore — export class EntityStore (L25)');
        expect(text).toContain('[method] add — public add(entity: Entity): void (L34)');
    });

    it('ts-read——源码段带行号列，折叠行标区间', () => {
        const res = tsResult({
            ok: true, file: 'C:/x/model.ts', symbol: 'add', kind: 'method',
            text: '30:     /**\n31:      * 加入实体\n34:     public add(entity: Entity): void',
            range: { start: 30, end: 37 }
        });
        const node = window.buildToolBlock(card('ts-read', { file: 'mau:tools/ts/fixtures/src/model.ts', symbol: 'add' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('读取 model.ts · add · method · L30-37');
        expect(text).toContain('public add(entity: Entity): void');
    });

    it('ts-find-ref——三列命中（路径 / 行号 / 种类），无引用显式占位', () => {
        const res = tsResult({
            ok: true, file: 'C:/x/model.ts', symbol: 'makeEntity', total: 2,
            refs: [
                { file: 'src/model.ts', line: 61, col: 17, kind: 'decl' },
                { file: 'src/consumer.ts', line: 12, col: 27, kind: 'ref' }
            ]
        });
        const node = window.buildToolBlock(card('ts-find-ref', { file: 'mau:tools/ts/fixtures/src/model.ts', symbol: 'makeEntity' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('引用 makeEntity · 2 处');
        expect(text).toContain('src/model.ts:61:17: decl');
        expect(text).toContain('src/consumer.ts:12:27: ref');

        const empty = window.buildToolBlock(card('ts-find-ref', { symbol: 'nobody' }, tsResult({ ok: true, total: 0, refs: [] })), 'toolcard');
        expect(empty.textContent).toContain('（无引用）');
    });

    it('ts-patch——diff 逐行 + 诊断行；无变更显式占位', () => {
        const res = tsResult({
            ok: true, file: 'C:/x/model.ts', mode: 'replace', changedLines: 1,
            diff: ["-     return { id: id, name: 'entity-' + id };", "+     return { id: id, name: 'probe-' + id };"],
            diagnostics: [], errorCount: 0
        });
        const node = window.buildToolBlock(card('ts-patch', { file: 'mau:tools/ts/fixtures/src/model.ts', old: "x", new: "y" }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('改写 model.ts · 替换 · 1 行变更');
        expect(text).toContain("-     return { id: id, name: 'entity-' + id };");
        expect(text).toContain("+     return { id: id, name: 'probe-' + id };");

        const noChange = window.buildToolBlock(card('ts-patch', { file: 'x.ts', symbol: 's' },
            tsResult({ ok: true, mode: 'symbol', changedLines: 0, diff: [], diagnostics: [], errorCount: 0 })), 'toolcard');
        expect(noChange.textContent).toContain('（无变更）');
    });

    it('服务失败——ok:false 显式出声（不静默成空白）', () => {
        const node = window.buildToolBlock(card('ts-check', { path: 'x' },
            tsResult({ ok: false, error: 'BAD_OP', message: '未知操作：ts.check' })), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('服务失败');
        expect(text).toContain('BAD_OP');
    });

    it('ts-info——折叠行读 strict 与规模，输出展开严格族 / 范围 / 未设置项', () => {
        const res = tsResult({
            ok: true, tsconfig: 'C:/x/tsconfig.json', extendsChain: ['C:/x/tsconfig.json'],
            strict: true, strictExplicit: true,
            strictFamily: { noImplicitAny: true, strictNullChecks: true, noImplicitThis: false },
            unset: ['exclude'], target: 'ES2020', module: 'CommonJS', moduleResolution: 'Node10',
            lib: [], types: [], include: ['src/**/*.ts'], exclude: [], baseUrl: '', paths: {},
            files: 3, nodeModulesInScope: 0, skipLibCheck: false, esModuleInterop: false
        });
        const node = window.buildToolBlock(card('ts-info', { path: 'mau:tools/ts/fixtures/tsconfig.json' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('口径 tsconfig.json · strict · 3 文件');
        expect(text).toContain('true（显式）');
        expect(text).toContain('noImplicitThis');
        expect(text).toContain('exclude (未设置)');
    });

    it('ts-lint——合计行 + 逐条命中（文件:行 [规则] 说明 ｜ 摘录）', () => {
        const res = tsResult({
            ok: true, mode: 'project', tsconfig: 'C:/x/tsconfig.json', files: 10,
            rules: ['any', 'empty-catch'], counts: { any: 1, 'empty-catch': 1 }, total: 2,
            items: [{ file: 'src/a.ts', line: 12, rule: 'any', note: '显式 any', excerpt: 'const v: any = 1;' }]
        });
        const node = window.buildToolBlock(card('ts-lint', { path: 'mau:tools/ts/fixtures/tsconfig.json' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('松散扫描 tsconfig.json · 2 条 · 10 文件');
        expect(text).toContain('合计 2 条');
        expect(text).toContain('src/a.ts:12 [any]');
        expect(text).toContain('const v: any = 1;');
    });

    it('ts-test——通过率；失败态出用例明细（ok:false 无 error 仍渲正常段）', () => {
        const green = tsResult({ ok: true, runner: 'vitest', files: 14, cases: 245, passed: 245, failed: 0, skipped: 0, failures: [], elapsedMs: 3461 });
        const okNode = window.buildToolBlock(card('ts-test', { path: 'mau:CatHome4/html/tests' }, green), 'toolcard');
        expect(okNode.textContent).toContain('245/245 通过');
        expect(okNode.textContent).toContain('（无失败用例）');

        const red = tsResult({ ok: false, runner: 'vitest', files: 15, cases: 246, passed: 245, failed: 1, skipped: 0, failures: ['FAIL  unit/x.test.mjs > probe > 必然失败 :: AssertionError: expected 1 to be 2'], elapsedMs: 4244 });
        const redNode = window.buildToolBlock(card('ts-test', { path: 'mau:CatHome4/html/tests' }, red), 'toolcard');
        expect(redNode.textContent).toContain('1 失败');
        expect(redNode.textContent).toContain('必然失败');
        expect(redNode.textContent).toContain('AssertionError');
    });

    it('ts-format——差异规模 + 逐文件；零差异显式占位', () => {
        const res = tsResult({ ok: true, mode: 'check', files: 10, changedFiles: 1, changedLines: 2, indent: 'space 4', items: [{ file: 'src/a.ts', changedLines: 2, written: false }] });
        const node = window.buildToolBlock(card('ts-format', { path: 'mau:tools/ts/fixtures/tsconfig.json' }, res), 'toolcard');
        expect(node.textContent).toContain('1 文件差异');
        expect(node.textContent).toContain('src/a.ts · 2 行差异');
        expect(node.textContent).toContain('space 4');

        const zero = window.buildToolBlock(card('ts-format', { path: 'x/tsconfig.json' },
            tsResult({ ok: true, mode: 'check', files: 10, changedFiles: 0, changedLines: 0, indent: '（未发现 .editorconfig——缩进不动）', items: [] })), 'toolcard');
        expect(zero.textContent).toContain('（零差异——无需规整）');
    });
});

describe('GroupCat——群聊 / 派单工具（A306）', () => {
    it('group-info——成员逐行（显示名 / key / 角色）', () => {
        const res = '{"ok":true,"tool":"group-info","target":"猫窝施工大队","items":2,"project":"CatHome4","enabled":true,"msgCount":6,"lastSeq":6,"lastAt":0}'
            + '\n猫窝施工大队 | 2 名成员 · 消息 6 条 · 最新 #6'
            + '\n· 猫窝程序员（k1） — 管理员 / 订阅'
            + '\n· GroupTest（k2）';
        const node = window.buildToolBlock(card('group-info', { group: '猫窝施工大队' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('2 名成员');
        expect(text).toContain('猫窝程序员（k1） — 管理员 / 订阅');
        expect(text).toContain('GroupTest（k2）');
    });

    it('group-inbox——逐条列（#seq / 类型 / 发方 → 收方 / 摘要）', () => {
        const res = '{"ok":true,"tool":"group-inbox","target":"猫窝施工大队","items":2,"lastSeq":6}'
            + '\n猫窝施工大队 | 2 条 · 最新 #6'
            + '\n#5 [派单] 猫窝程序员 → GroupTest · 任务：只回一句话 第二行'
            + '\n#6 [回执] GroupTest · A306 回执 OK（关联 5）';
        const node = window.buildToolBlock(card('group-inbox', { group: '猫窝施工大队', after: 4 }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('2 条');
        expect(text).toContain('#5 [派单] 猫窝程序员 → GroupTest');
        expect(text).toContain('#6 [回执] GroupTest');
    });

    it('group-delegate——落位序号进折叠行，两行交付说明（派活并唤醒）', () => {
        const res = '{"ok":true,"tool":"group-delegate","target":"猫窝施工大队","seq":7,"to":"k2"}'
            + '\n猫窝施工大队 | 已派单 → GroupTest · #7';
        const node = window.buildToolBlock(card('group-delegate', { group: '猫窝施工大队', to: 'k2', text: '干活' }, res), 'toolcard');
        const text = node.textContent;
        expect(text).toContain('派活 → k2 · #7 已唤醒');
        expect(text).toContain('已入组 #7');
        expect(text).toContain('已唤醒 k2');
    });

    it('group-post——发言只记流水（不唤醒任何人）；失败态机器码可见', () => {
        const ok = window.buildToolBlock(card('group-post', { group: '猫窝施工大队', text: '讨论' },
            '{"ok":true,"tool":"group-post","target":"猫窝施工大队","seq":8}\n猫窝施工大队 | 已记 #8'), 'toolcard');
        expect(ok.textContent).toContain('#8');
        expect(ok.textContent).toContain('不唤醒任何人');

        const err = window.buildToolBlock(card('group-delegate', { group: '猫窝施工大队', to: 'x', text: 'y' },
            'ERR|GROUP_NOT_ADMIN|调用者不是本组管理员: k9'), 'toolcard');
        expect(err.textContent).toContain('GROUP_NOT_ADMIN');
    });
});
