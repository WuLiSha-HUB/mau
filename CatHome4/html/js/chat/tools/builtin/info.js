// ═══════════════════════════════════════════
// chat/tools/builtin/info.js —— info 工具（专属骨架 info）
//
// 定位：本会话环境自省的**专属骨架渲染**——返回体 = 分类 JSON 块（非「头 + 正文」两段结构，整块解析）。
// 口径（莎定 2026-09-18）：info 以 LLM 可读为第一目的（后端分类 JSON 块）；前端把各大类**摊平成键值行**，
//       值内多字段用「 · 」连接，长值由 CSS 折行（不截断）。
// 缺类不显示该行（后端「无则省略键」约定——前端不补空行、不写 NaN）。
// 来源：重构前 `chat-tools.js::chatSkelInfo / chatInfoPairs`（A200 恢复）。
// ═══════════════════════════════════════════

/// info 骨架——分类 JSON 块 → 键值行（解析不出 → 原文兜底，失败可见不静默）
function skelInfo(tool) {
    var segs = [segInput(tool, '')];
    var d = tryJson(tool.result);
    if (!d) {
        segs.push(segOutput(tool, '输出', function (body, text, isErr) {
            segBlock(body, isErr ? 'tr err' : 'tr', text);
        }, tool.result));
        return { tag: '', tagCls: '', segs: segs };
    }
    var pairs = ovInfoPairs(d);
    segs.push(segOutput(tool, '输出 · ' + pairs.length + ' 项', function (body, text, isErr) {
        segKv(body, pairs);
    }, ovInfoPeek(pairs)));
    return { tag: '', tagCls: '', segs: segs };
}

/// 摊平键值行——固定顺序：猫 / 版本 / 当前时间 / LLM / 本地端点 / 可见根 / 根路径 / 前文 / 加载包
function ovInfoPairs(d) {
    var pairs = [];
    var cat = field(d, 'cat');
    if (cat.length > 0) {
        pairs.push({ k: '猫', v: cat });
    }
    var v = d.version;
    if (v && typeof v === 'object') {
        var ver = field(v, 'version');
        var build = field(v, 'build');
        pairs.push({ k: '版本', v: ver + (build.length > 0 ? ' · 编译 ' + build : '') });
    }
    var t = d.time;
    if (t && typeof t === 'object') {
        pairs.push({ k: '当前时间', v: field(t, 'now') });
    }
    var l = d.llm;
    if (l && typeof l === 'object') {
        var lp = [];
        var proto = field(l, 'protocol');
        var host = field(l, 'host');
        var model = field(l, 'model');
        var src = field(l, 'source');
        if (proto.length > 0) {
            lp.push(proto);
        }
        if (host.length > 0) {
            lp.push(host);
        }
        if (model.length > 0) {
            lp.push('model=' + model);
        }
        if (src.length > 0) {
            lp.push(src);
        }
        pairs.push({ k: 'LLM', v: lp.join(' · ') });
    }
    var ep = d.endpoint;
    if (ep && typeof ep === 'object') {
        var eps = [];
        var chat = field(ep, 'chat');
        var panel = field(ep, 'panel');
        if (chat.length > 0) {
            eps.push('对话 ' + chat);
        }
        if (panel.length > 0) {
            eps.push('管理面板 ' + panel);
        }
        pairs.push({ k: '本地端点', v: (eps.length > 0 ? eps.join(' · ') : '（未监听）') });
    }
    if (d.roots && d.roots.length > 0) {
        var roots = [];
        var rootPaths = [];
        for (var i = 0; i < d.roots.length; i = i + 1) {
            var r = d.roots[i];
            var one = field(r, 'id') + '(' + (r.writable === true ? 'rw' : 'ro') + ')';
            var note = field(r, 'note');
            if (note.length > 0) {
                one = one + '[' + note + ']';
            }
            roots.push(one);
            var rp = field(r, 'path');
            if (rp.length > 0) {
                rootPaths.push(field(r, 'id') + ' → ' + rp);
            }
        }
        pairs.push({ k: '可见根', v: roots.join(' · ') });
        if (rootPaths.length > 0) {
            pairs.push({ k: '根路径', v: rootPaths.join(' · ') });
        }
    }
    if (d.tokens && typeof d.tokens === 'object') {
        var ctx = fieldNum(d.tokens, 'context');
        if (ctx.length > 0) {
            pairs.push({ k: '前文', v: ctx + ' tokens' });
        }
    }
    if (d.packs && d.packs.length > 0) {
        var packs = [];
        for (var k = 0; k < d.packs.length; k = k + 1) {
            var pk = field(d.packs[k], 'key');
            var desc = field(d.packs[k], 'desc');
            packs.push(desc.length > 0 ? pk + '(' + desc + ')' : pk);
        }
        pairs.push({ k: '加载包', v: packs.join(' · ') });
    }
    return pairs;
}

/// 键值行 → 折叠摘要文本（段折叠摘要在行数 >5 时展示「前 2 + … + 后 2」）
function ovInfoPeek(pairs) {
    var lines = [];
    for (var i = 0; i < pairs.length; i = i + 1) {
        lines.push(pairs[i].k + ': ' + pairs[i].v);
    }
    return lines.join('\n');
}

/// 取数值字段——非数值返回空串（键值行不写 NaN / 空值行）
function fieldNum(obj, key) {
    if (!obj || typeof obj[key] !== 'number') {
        return '';
    }
    return String(obj[key]);
}

// ── 声明（覆盖层：折叠行文案；段结构归专属骨架）──────────────

toolDecl('info', {
    inputLines: function () {
        return ['查看本会话运行环境'];
    },
    headline: function (a, r) {
        // 返回体 = 分类 JSON 块（无「头 + 正文」两段）——整块解析，不用 metaHead
        var d = tryJson(r);
        if (!d) {
            return '环境信息' + ovStat(r);
        }
        var ver = '';
        if (d.version && typeof d.version === 'object') {
            ver = field(d.version, 'version');
        }
        var cat = field(d, 'cat');
        var t = '环境信息 · v' + ver;
        if (cat.length > 0) {
            t = t + ' · 猫 ' + cat;
        }
        return t;
    }
});
