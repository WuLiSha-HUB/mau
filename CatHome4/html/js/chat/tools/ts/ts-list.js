// ═══════════════════════════════════════════
// chat/tools/ts/ts-list.js —— ts-list / TypeScript 单文件结构清单（TsCat · A294）
// 声明：输入意图行 + 折叠行读声明总数与模块判定 + 输出逐条「种类 / 名称 / 签名 / 行号」；
//       段结构归骨架 `listing`（条目列表）。
// 回执形态（stdio_server ts.list）：单段 JSON {ok,file,isModule,kind,items[{kind,name,signature,line}],total}
// ═══════════════════════════════════════════

/// 声明条目 → 单行（种类 · 名称 — 签名 (Lnn)）
function tsListItemLine(it) {
    if (!it || typeof it !== 'object') {
        return '';
    }
    var kind = ovMetaStr(it, 'kind');
    var name = ovMetaStr(it, 'name');
    var sig = ovMetaStr(it, 'signature');
    var line = ovMetaNum(it, 'line', 0);
    var head = (kind.length > 0 ? '[' + kind + '] ' : '') + name;
    var out = head;
    if (sig.length > 0) {
        out = out + (name.length > 0 ? ' — ' : '') + sig;
    }
    if (line > 0) {
        out = out + ' (L' + line + ')';
    }
    return out;
}

toolDecl('ts-list', {
    inputLines: function (a) {
        var kind = (typeof a.kind === 'string' && a.kind.length > 0 && a.kind !== 'all') ? (' · 仅 ' + a.kind) : '';
        return ['列结构 ' + ovText(a.file) + kind];
    },
    headline: function (a, r) {
        var head = '结构 ' + ovShort(a.file);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var o = ovJson(r);
        if (!o) {
            return head + ovStat(r);
        }
        if (o.ok === false) {
            return head + ' · 服务失败';
        }
        var scope = (o.isModule === true) ? ' · 模块' : ' · 全局脚本';
        return head + ' · ' + ovMetaNum(o, 'total', 0) + ' 项' + scope;
    },
    badge: function (r) {
        var o = ovJson(r);
        if (!o || o.ok === false) {
            return '';
        }
        return ' · ' + ovMetaNum(o, 'total', 0) + ' 项';
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var o = ovJson(r);
        if (!o) {
            return null;
        }
        if (o.ok === false) {
            return ['ERR|' + ovMetaStr(o, 'error') + '|' + ovMetaStr(o, 'message')];
        }
        var items = Array.isArray(o.items) ? o.items : [];
        var out = [];
        for (var i = 0; i < items.length; i = i + 1) {
            out.push(tsListItemLine(items[i]));
        }
        if (out.length === 0) {
            out.push('（无声明）');
        }
        return out;
    }
});
