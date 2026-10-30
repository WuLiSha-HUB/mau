// ═══════════════════════════════════════════
// chat/tools/ts/ts-find-ref.js —— ts-find-ref / TypeScript 符号引用查找（TsCat · A294）
// 声明：输入意图行（符号 / tsconfig）+ 折叠行读引用总数 + 输出逐条命中；
//       段结构归骨架 `matches`（路径 / 行号 三列——行形态 `路径:行:列: 种类`）。
// 回执形态（stdio_server ts.findRef）：单段 JSON {ok,file,symbol,refs[{file,line,col,kind}],total}
// ═══════════════════════════════════════════

/// 引用条目 → `路径:行:列: 种类`（骨架 matches 按冒号切三列；列号缺失不写）
function tsRefLine(ref) {
    if (!ref || typeof ref !== 'object') {
        return '';
    }
    var file = ovMetaStr(ref, 'file');
    var line = ovMetaNum(ref, 'line', 0);
    var col = ovMetaNum(ref, 'col', 0);
    var kind = ovMetaStr(ref, 'kind');
    var out = file;
    if (line > 0) {
        out = out + ':' + line + (col > 0 ? ':' + col : '');
    }
    if (kind.length > 0) {
        out = out + ': ' + kind;
    }
    return out;
}

toolDecl('ts-find-ref', {
    inputLines: function (a) {
        var base = '查找引用 ' + ovText(a.symbol);
        if (typeof a.file === 'string' && a.file.length > 0) {
            base = '查找引用 ' + a.symbol + ' · 定义于 ' + ovShort(a.file);
        }
        var cfg = (typeof a.tsconfig === 'string' && a.tsconfig.length > 0) ? (' · 工程 ' + ovShort(a.tsconfig)) : '';
        return [base + cfg];
    },
    headline: function (a, r) {
        var head = '引用 ' + ovText(a.symbol);
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
        return head + ' · ' + ovMetaNum(o, 'total', 0) + ' 处';
    },
    badge: function (r) {
        var o = ovJson(r);
        if (!o || o.ok === false) {
            return '';
        }
        return ' · ' + ovMetaNum(o, 'total', 0) + ' 处';
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
        var refs = Array.isArray(o.refs) ? o.refs : [];
        var out = [];
        for (var i = 0; i < refs.length; i = i + 1) {
            out.push(tsRefLine(refs[i]));
        }
        if (out.length === 0) {
            out.push('（无引用）');
        }
        return out;
    }
});
