// ═══════════════════════════════════════════
// chat/tools/ts/ts-lint.js —— ts-lint / TypeScript 松散写法扫描（TsCat · A332 工具 / A335 前端件）
// 声明：输入意图行（工程 / 单文件 + 规则过滤）+ 折叠行读命中合计 + 输出逐条命中与分规则计数；
//       段结构归骨架 `diagnostics`（诊断列表）。
// 回执形态（stdio_server ts.lint）：单段 JSON {ok,mode,tsconfig,files,rules[],counts{},total,
//       items[{file,line,rule,note,excerpt}]}
// ═══════════════════════════════════════════

/// 分规则计数行——「合计 N 条 · any 2 / console-log 3」
function tsLintCountsLine(o) {
    var counts = (o && o.counts && typeof o.counts === 'object') ? o.counts : {};
    var keys = Object.keys(counts);
    var parts = [];
    for (var i = 0; i < keys.length; i = i + 1) {
        parts.push(keys[i] + ' ' + ovMetaNum(counts, keys[i], 0));
    }
    var line = '合计 ' + ovMetaNum(o, 'total', 0) + ' 条';
    if (parts.length > 0) {
        line = line + ' · ' + parts.join(' / ');
    }
    return line;
}

/// 命中条目 → 单行（file:line [规则] 说明 ｜ 摘录）
function tsLintItemLine(it) {
    if (!it || typeof it !== 'object') {
        return '';
    }
    var where = ovMetaStr(it, 'file');
    var line = ovMetaNum(it, 'line', 0);
    if (where.length > 0 && line > 0) {
        where = where + ':' + line;
    }
    var rule = ovMetaStr(it, 'rule');
    var note = ovMetaStr(it, 'note');
    var excerpt = ovMetaStr(it, 'excerpt');
    var out = where;
    if (rule.length > 0) {
        out = out + ' [' + rule + ']';
    }
    if (note.length > 0) {
        out = out + ' ' + note;
    }
    if (excerpt.length > 0) {
        out = out + ' ｜ ' + excerpt;
    }
    return out;
}

toolDecl('ts-lint', {
    inputLines: function (a) {
        var rules = '';
        if (Array.isArray(a.rules) && a.rules.length > 0) {
            rules = ' · 仅 ' + a.rules.join(' / ');
        } else if (typeof a.rules === 'string' && a.rules.length > 0) {
            rules = ' · 仅 ' + a.rules;
        }
        return ['TS 松散写法扫描 ' + ovText(a.path) + rules];
    },
    headline: function (a, r) {
        var head = '松散扫描 ' + ovShort(a.path);
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
        return head + ' · ' + ovMetaNum(o, 'total', 0) + ' 条 · ' + ovMetaNum(o, 'files', 0) + ' 文件';
    },
    badge: function (r) {
        var o = ovJson(r);
        if (!o || o.ok === false) {
            return '';
        }
        return ' · ' + ovMetaNum(o, 'total', 0) + ' 条';
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
        var out = [];
        out.push(tsLintCountsLine(o));
        var items = Array.isArray(o.items) ? o.items : [];
        for (var i = 0; i < items.length; i = i + 1) {
            out.push(tsLintItemLine(items[i]));
        }
        if (items.length === 0) {
            out.push('（无命中）');
        }
        return out;
    }
});
