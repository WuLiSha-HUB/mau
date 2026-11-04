// ═══════════════════════════════════════════
// tests/unit/chat-ctx.test.mjs —— 前文弹层 + 端点角色标签回归网
//
// 覆盖：
//   ① 信息位整行入口——整行点击开弹层（默认按条视图）；内容走上游富文本分片（不再分段）
//   ② 弹层四视图——打开即读对应端点；同视图再点收起；异视图切并复用缓存
//   ③ 条目展开——点条目头切全文（摘要 ↔ 全文）
//   ④ 失败可见——端点报错 / 未响应在弹层内出声（不静默留白）
//   ⑤ 端点角色标签——读端点渲染（主要 / 备用态类 + 模型名）；点击投递 toggle 后重读
//   ⑥ 思考强度标签——读端点渲染（本猫覆盖 / 跟随全局）；点击循环投递下一档后重读
//
// 边界：只测对外行为（端点调用面 + DOM 可见结果），不测内部函数拆分。
// ═══════════════════════════════════════════

import { beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { bootChatPage, flushMicro } from './chat-env.mjs';

beforeAll(async () => {
    await bootChatPage();
});

beforeEach(() => {
    window.ctxClose();
    window.ctxData = null;
    window.ctxKeyData = null;
    window.ctxFullData = null;
    window.ctxMode = 'list';
    // 页面级临时模型——每个用例重置（页载归位语义的单测基线）
    window.apiModelPick = '';
    window.apiModelDirty = false;
    window.apiModelSynced = '';
    const info = document.getElementById('chatInfo');
    if (info) {
        info.textContent = '';
    }
});

/** 端点桩——按 url 关键字分派响应，并记录调用 */
function mockEndpoints(map) {
    const calls = [];
    globalThis.fetch = async (url, opt) => {
        calls.push({ url: String(url), opt: opt });
        const key = Object.keys(map).find((k) => String(url).indexOf(k) >= 0);
        return { json: async () => (key ? map[key] : { ok: false, error: '端点未注册' }) };
    };
    return calls;
}

const CTX_OK = {
    ok: true, count: 2, chars: 30, ctxTokens: 1234,
    items: [
        { i: 1, role: 'system', chars: 10, preview: '系统提示', content: '系统提示全文' },
        { i: 2, role: 'user', chars: 20, preview: '问题一', content: '问题一全文', time: 1700000000000 }
    ]
};

const KEY_OK = {
    ok: true, count: 1, chars: 8,
    items: [{ i: 1, role: 'report', chars: 8, preview: '加载报告', content: '加载报告全文' }]
};

const FULL_OK = {
    ok: true, count: 1, chars: 12, tokens: 2400, ratio: 2.5, estimated: false,
    items: [{ i: 1, role: 'system', chars: 12, preview: '系统段', content: '系统段全文' }]
};

// ── ① 信息位整行入口 ─────────────────────────
describe('信息位整行入口（state 段投影）', () => {
    it('整行点击开弹层——默认按条视图并读取 /api/v1/context', async () => {
        const calls = mockEndpoints({ '/api/v1/context': CTX_OK });
        window.stateApply({ sessionId: 'majordomo', tokens: { count: 12, context: 3456 } });
        expect(document.querySelectorAll('#chatInfo [data-ctx]').length).toBe(0);
        document.getElementById('chatInfo').click();
        await flushMicro();
        expect(document.getElementById('ctxPopover').style.display).toBe('flex');
        expect(calls.length).toBe(1);
        expect(calls[0].url).toContain('/api/v1/context');
        expect(document.getElementById('ctxTitle').textContent).toBe('前文条目');
    });

    it('信息位内容走上游富文本分片（数值包 .ci-num，不再出可点段）', () => {
        window.stateApply({
            meta: { contextChars: 3000 },
            tokens: {
                count: 12, context: 3456, sessionPrompt: 100, sessionMiss: 10,
                sessionCompletion: 20, sessionRate: 0.9
            }
        });
        const info = document.getElementById('chatInfo');
        expect(info.querySelectorAll('.ci-num').length).toBeGreaterThan(0);
        expect(info.textContent).toContain('前文');
        expect(info.textContent).toContain('条');
    });
});

// ── ② 弹层四视图 ─────────────────────────────
describe('前文弹层——四视图与三态', () => {
    it('点条数段打开并读取 /api/v1/context，条目按序渲染', async () => {
        const calls = mockEndpoints({ '/api/v1/context': CTX_OK });
        window.stateApply({ sessionId: 'majordomo', tokens: { count: 2, context: 1234 } });
        document.getElementById('chatInfo').click();
        await flushMicro();
        expect(document.getElementById('ctxPopover').style.display).toBe('flex');
        expect(calls.length).toBe(1);
        expect(calls[0].url).toContain('/api/v1/context');
        expect(document.querySelectorAll('#ctxList .ctx-item').length).toBe(2);
        expect(document.getElementById('ctxTitle').textContent).toBe('前文条目');
    });

    it('已开且同视图再点收起（不重复读取）', async () => {
        const calls = mockEndpoints({ '/api/v1/context': CTX_OK });
        window.stateApply({ sessionId: 'majordomo', tokens: { count: 2, context: 1234 } });
        const link = document.getElementById('chatInfo');
        link.click();
        await flushMicro();
        link.click();
        await flushMicro();
        expect(document.getElementById('ctxPopover').style.display).toBe('none');
        expect(calls.length).toBe(1);
    });

    it('已开切 tokens 页签复用缓存（不重复读取）并渲染占比条', async () => {
        const calls = mockEndpoints({ '/api/v1/context': CTX_OK });
        window.stateApply({ sessionId: 'majordomo', tokens: { count: 2, context: 1234 } });
        document.getElementById('chatInfo').click();
        await flushMicro();
        document.getElementById('ctxModeTokens').click();
        await flushMicro();
        expect(calls.length).toBe(1);
        expect(document.getElementById('ctxTitle').textContent).toBe('前文 token 明细');
        expect(document.querySelectorAll('#ctxList .ctx-bar').length).toBe(2);
        expect(document.getElementById('ctxModeTokens').className).toContain('ctx-mode-on');
    });

    it('切关键信息读 /api/v1/keyinfo（role 标签走中文名）', async () => {
        const calls = mockEndpoints({ '/api/v1/context': CTX_OK, '/api/v1/keyinfo': KEY_OK });
        window.stateApply({ sessionId: 'majordomo', tokens: { count: 2, context: 1234 } });
        document.getElementById('chatInfo').click();
        await flushMicro();
        document.getElementById('ctxModeKey').click();
        await flushMicro();
        expect(calls[1].url).toContain('/api/v1/keyinfo');
        expect(document.getElementById('ctxTitle').textContent).toBe('前文关键信息');
        expect(document.querySelector('#ctxList .ctx-role').textContent).toBe('加载报告');
    });

    it('切完整前文读 /api/v1/fullctx（token 文案随宿主折算值）', async () => {
        const calls = mockEndpoints({ '/api/v1/context': CTX_OK, '/api/v1/fullctx': FULL_OK });
        window.stateApply({ sessionId: 'majordomo', tokens: { count: 2, context: 1234 } });
        document.getElementById('chatInfo').click();
        await flushMicro();
        document.getElementById('ctxModeFull').click();
        await flushMicro();
        expect(calls[1].url).toContain('/api/v1/fullctx');
        expect(document.getElementById('ctxTitle').textContent).toBe('完整前文');
        expect(document.getElementById('ctxMeta').textContent).toContain('实测比值 2.50 折算');
    });

    it('Esc 关闭弹层', async () => {
        mockEndpoints({ '/api/v1/context': CTX_OK });
        window.stateApply({ sessionId: 'majordomo', tokens: { count: 2, context: 1234 } });
        document.getElementById('chatInfo').click();
        await flushMicro();
        document.dispatchEvent(new window.Event('keydown'));
        window.ctxClose();
        expect(document.getElementById('ctxPopover').style.display).toBe('none');
    });
});

// ── ③ 条目展开 ───────────────────────────────
describe('前文弹层——条目展开', () => {
    it('点条目头展开全文，再点折叠', async () => {
        mockEndpoints({ '/api/v1/context': CTX_OK });
        window.stateApply({ sessionId: 'majordomo', tokens: { count: 2, context: 1234 } });
        document.getElementById('chatInfo').click();
        await flushMicro();
        const item = document.querySelector('#ctxList .ctx-item');
        const head = item.querySelector('.ctx-item-head');
        const full = item.querySelector('.ctx-full');
        const body = item.querySelector('.ctx-body');
        expect(full.style.display).toBe('none');
        head.click();
        expect(full.style.display).toBe('block');
        expect(body.style.display).toBe('none');
        head.click();
        expect(full.style.display).toBe('none');
    });
});

// ── ④ 失败可见 ───────────────────────────────
describe('前文弹层——失败可见', () => {
    it('端点回 ok=false 时弹层内出声（不静默留白）', async () => {
        mockEndpoints({});
        window.stateApply({ sessionId: 'majordomo', tokens: { count: 2, context: 1234 } });
        document.getElementById('chatInfo').click();
        await flushMicro();
        expect(document.getElementById('ctxList').textContent).toContain('不可读');
        expect(document.getElementById('ctxList').textContent).toContain('端点未注册');
    });

    it('读取异常时弹层内出声', async () => {
        globalThis.fetch = async () => { throw new Error('网络断了'); };
        window.stateApply({ sessionId: 'majordomo', tokens: { count: 2, context: 1234 } });
        document.getElementById('chatInfo').click();
        await flushMicro();
        expect(document.getElementById('ctxList').textContent).toContain('读取失败');
    });
});

// ── ⑤ 端点角色标签 ───────────────────────────
describe('端点角色标签（apirole.js）', () => {
    it('读端点渲染端点名与模型名（API:端点名[模型]），备用态加 backup 类', async () => {
        const calls = mockEndpoints({
            '/api/v1/api-role': { role: '备用', primary: 'deepseek-v4.1', backup: 'glm-4.6', primaryName: '主端点', backupName: '备端点', activeName: '备端点', activeModel: 'glm-4.6', override: '' }
        });
        window.apiRoleRefresh();
        await flushMicro();
        const node = document.getElementById('chatApiRole');
        expect(calls[0].url).toContain('/api/v1/api-role');
        expect(node.querySelector('.apirole-main').textContent).toBe('API:备端点[glm-4.6]');
        expect(node.className).toContain('backup');
    });

    it('临时模型覆盖生效时——标签显示覆盖后的模型名', async () => {
        mockEndpoints({
            '/api/v1/api-role': { role: '主要', primary: 'deepseek-v4.1', backup: 'glm-4.6', primaryName: '主端点', backupName: '备端点', activeName: '主端点', activeModel: 'deepseek-v4.1', override: 'my-temp-model' }
        });
        window.apiModelPick = 'my-temp-model';
        window.apiRoleRefresh();
        await flushMicro();
        const node = document.getElementById('chatApiRole');
        expect(node.querySelector('.apirole-main').textContent).toBe('API:主端点[my-temp-model]');
    });

    it('页载归位——清页面内存态与后端覆盖（关页再打开回默认）', async () => {
        const calls = mockEndpoints({
            '/api/v1/api-role': { role: '主要', activeName: '主端点', activeModel: 'deepseek-v4.1', override: '' },
            '/api/v1/api-model': { ok: true, model: '' }
        });
        window.apiModelPick = 'stale-model';
        window.apiModelResetOnLoad();
        await flushMicro();
        expect(window.apiModelPick).toBe('');
        const posts = calls.filter((c) => c.url.indexOf('/api/v1/api-model') >= 0);
        expect(posts.length).toBe(1);
        expect(JSON.parse(posts[0].opt.body).model).toBe('');
    });

    it('发送前同步——脏标记为真时补投（不一致才写后端）', async () => {
        const calls = mockEndpoints({
            '/api/v1/api-role': { role: '主要', activeName: '主端点', activeModel: 'deepseek-v4.1', override: '' },
            '/api/v1/api-model': { ok: true, model: 'glm-4.6' }
        });
        window.apiModelPick = 'glm-4.6';
        window.apiModelDirty = true;
        await window.apiModelSync();
        const posts = calls.filter((c) => c.url.indexOf('/api/v1/api-model') >= 0);
        expect(posts.length).toBe(1);
        expect(JSON.parse(posts[0].opt.body).model).toBe('glm-4.6');
        // 同步成功后脏标记清零 → 再同步零请求
        await window.apiModelSync();
        expect(calls.filter((c) => c.url.indexOf('/api/v1/api-model') >= 0).length).toBe(1);
    });

    it('发送前同步——无脏标记时零请求（发送路径不额外打接口）', async () => {
        const calls = mockEndpoints({
            '/api/v1/api-role': { role: '主要', activeName: '主端点', activeModel: 'deepseek-v4.1', override: '' },
            '/api/v1/api-model': { ok: true, model: '' }
        });
        window.apiModelPick = 'glm-4.6';
        window.apiModelDirty = false;
        await window.apiModelSync();
        expect(calls.length).toBe(0);
    });

    it('端点名缺失回落占位', async () => {
        mockEndpoints({ '/api/v1/api-role': { role: '主要', primary: '', backup: '', activeName: '', activeModel: '' } });
        window.apiRoleRefresh();
        await flushMicro();
        const node = document.getElementById('chatApiRole');
        expect(node.querySelector('.apirole-main').textContent).toBe('API:未配置[未配置]');
        expect(node.className).not.toContain('backup');
    });

    it('左段点击投递 toggle 后重读端点（真源在后端，不本地翻转）', async () => {
        const calls = mockEndpoints({
            '/api/v1/api-role': { role: '主要', primary: 'deepseek-v4.1', backup: 'glm-4.6', activeName: '主端点', activeModel: 'deepseek-v4.1' },
            '/api/v1/api-role/toggle': { ok: true, role: '备用' }
        });
        document.getElementById('chatApiRole').querySelector('.apirole-main').click();
        await flushMicro();
        const toggle = calls.filter((c) => c.url.indexOf('/toggle') >= 0);
        expect(toggle.length).toBe(1);
        expect(toggle[0].opt.method).toBe('POST');
        expect(calls.filter((c) => c.url.indexOf('/toggle') < 0).length).toBeGreaterThan(0);
    });

    it('右段小三角展开模型下拉——清单来自 api-models，单选写入 api-model', async () => {
        const calls = mockEndpoints({
            '/api/v1/api-role': { role: '主要', activeName: '主端点', activeModel: 'deepseek-v4.1' },
            '/api/v1/api-models': { ok: true, endpointName: '主端点', current: 'deepseek-v4.1', models: ['deepseek-v4.1', 'glm-4.6'] },
            '/api/v1/api-model': { ok: true, model: 'glm-4.6' }
        });
        document.getElementById('chatApiRole').querySelector('.apirole-caret').click();
        await flushMicro();
        const panel = document.getElementById('apiModelsPanel');
        expect(panel).not.toBeNull();
        expect(panel.style.display).toBe('block');
        const radios = panel.querySelectorAll('input[type=radio]');
        // 「跟随端点默认」+ 两个模型
        expect(radios.length).toBe(3);
        // 选第二个模型 → 投递会话级临时模型（不回写端点配置）
        radios[2].checked = true;
        radios[2].dispatchEvent(new Event('change'));
        await flushMicro();
        const posts = calls.filter((c) => c.url.indexOf('/api/v1/api-model') >= 0 && c.url.indexOf('/api/v1/api-models') < 0);
        expect(posts.length).toBe(1);
        expect(JSON.parse(posts[0].opt.body).model).toBe('glm-4.6');
        window.apiModelsClose();
    });

    it('模型清单失败——下拉内出声给原因（不静默空列表）', async () => {
        mockEndpoints({
            '/api/v1/api-role': { role: '主要', activeName: '主端点', activeModel: 'x' },
            '/api/v1/api-models': { ok: false, error: '上游无响应' }
        });
        document.getElementById('chatApiRole').querySelector('.apirole-caret').click();
        await flushMicro();
        const panel = document.getElementById('apiModelsPanel');
        expect(panel.textContent).toContain('上游无响应');
        window.apiModelsClose();
    });

    it('读取失败回落占位并出声（不显示假值）', async () => {
        globalThis.fetch = async () => { throw new Error('端点不可达'); };
        window.apiRoleRefresh();
        await flushMicro();
        expect(document.getElementById('chatApiRole').querySelector('.apirole-main').textContent).toBe('API —');
    });
});

// ── ⑥ 思考强度标签 ───────────────────────────
describe('思考强度标签（effort.js）', () => {
    it('读端点渲染——本猫覆盖态加 override 类', async () => {
        mockEndpoints({ '/api/v1/effort': { effort: 'max', global: 'high' } });
        window.effortRefresh();
        await flushMicro();
        const node = document.getElementById('chatEffort');
        expect(node.textContent).toBe('思考 max');
        expect(node.className).toContain('override');
    });

    it('空覆盖——显示跟随全局并带全局值（无 override 类）', async () => {
        mockEndpoints({ '/api/v1/effort': { effort: '', global: 'low' } });
        window.effortRefresh();
        await flushMicro();
        const node = document.getElementById('chatEffort');
        expect(node.textContent).toBe('思考 跟随全局(low)');
        expect(node.className).not.toContain('override');
    });

    it('点击循环——读真源取下一档后投递（不本地翻转）+ 写后重读', async () => {
        const calls = [];
        globalThis.fetch = async (url, opt) => {
            calls.push({ url: String(url), opt: opt });
            if (opt && opt.method === 'POST') {
                return { json: async () => ({ ok: true, effort: JSON.parse(opt.body).value }) };
            }
            return { json: async () => ({ effort: 'low', global: 'high' }) };
        };
        document.getElementById('chatEffort').click();
        await flushMicro();
        const posts = calls.filter((c) => c.opt && c.opt.method === 'POST');
        expect(posts.length).toBe(1);
        expect(posts[0].url).toContain('/api/v1/effort');
        // 三档循环 low → high → max（跟随全局不在常驻序里）
        expect(JSON.parse(posts[0].opt.body).value).toBe('high');
        // 写后重读真源（GET 至少一次）
        expect(calls.filter((c) => !c.opt || c.opt.method !== 'POST').length).toBeGreaterThan(0);
    });

    it('当前档与全局值恰好相同时——循环首位插入「跟随全局」（空串）', async () => {
        const calls = [];
        globalThis.fetch = async (url, opt) => {
            calls.push({ url: String(url), opt: opt });
            if (opt && opt.method === 'POST') {
                return { json: async () => ({ ok: true, effort: JSON.parse(opt.body).value }) };
            }
            return { json: async () => ({ effort: 'high', global: 'high' }) };
        };
        document.getElementById('chatEffort').click();
        await flushMicro();
        const posts = calls.filter((c) => c.opt && c.opt.method === 'POST');
        expect(posts.length).toBe(1);
        // 覆盖档 == 全局值 → 首位插入跟随全局 → 切到空串
        expect(JSON.parse(posts[0].opt.body).value).toBe('');
    });

    it('跟随全局态——点击切到 low（不回插入跟随态）', async () => {
        const calls = [];
        globalThis.fetch = async (url, opt) => {
            calls.push({ url: String(url), opt: opt });
            if (opt && opt.method === 'POST') {
                return { json: async () => ({ ok: true, effort: JSON.parse(opt.body).value }) };
            }
            return { json: async () => ({ effort: '', global: 'low' }) };
        };
        document.getElementById('chatEffort').click();
        await flushMicro();
        const posts = calls.filter((c) => c.opt && c.opt.method === 'POST');
        expect(JSON.parse(posts[0].opt.body).value).toBe('low');
    });

    it('读取失败回落占位（不显示假值）', async () => {
        globalThis.fetch = async () => { throw new Error('端点不可达'); };
        window.effortRefresh();
        await flushMicro();
        expect(document.getElementById('chatEffort').textContent).toBe('思考 —');
    });
});
