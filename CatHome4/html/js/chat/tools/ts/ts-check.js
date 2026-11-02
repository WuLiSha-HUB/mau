// ═══════════════════════════════════════════
// chat/tools/ts/ts-check.js —— ts-check / TypeScript 工程口径诊断（TsCat · A294）
// 声明：输入意图行（工程 / 单文件 + full 开关）+ 折叠行读 counts（errors / warnings）+ 输出逐条诊断；
//       段结构归骨架 `diagnostics`（计数徽标 + 诊断逐行）。
// 回执形态（stdio_server ts.check）：单段 JSON {ok,mode,tsconfig,files,errors[],warnings[],counts{}}
//       ——无结构化头（首行即全量 JSON），故走 ovJson 直解。
// ═══════════════════════════════════════════

/// 诊断条目 → 单行（文件:行:列 码 · 消息；字段缺失不写占位符）
function tsDiagLine(d, mark) {
    if (!d || typeof d !== 'object') {
        return '';
    }
    var where = ovMetaStr(d, 'file');
    var line = ovMetaNum(d, 'line', 0);
    var col = ovMetaNum(d, 'col', 0);
    if (where.length > 0 && line > 0) {
        where = where + ':' + line + (col > 0 ? ':' + col : '');
    }
    var code = ovMetaStr(d, 'code');
    if (code.length === 0 && typeof d.code === 'number') {
        code = 'TS' + d.code;
    }
    var msg = ovMetaStr(d, 'message');
    var out = (mark.length > 0 ? mark + ' ' : '') + where;
    if (code.length > 0) {
        out = out + ' ' + (code.indexOf('TS') === 0 ? code : 'TS' + code);
    }
    if (msg.length > 0) {
        out = out + ' ' + msg;
    }
    return out;
}

/// 计数取用——counts 优先，缺失回落数组长度
function tsDiagCounts(o) {
    var e = 0;
    var w = 0;
    if (o && o.counts && typeof o.counts === 'object') {
        e = ovMetaNum(o.counts, 'errors', 0);
        w = ovMetaNum(o.counts, 'warnings', 0);
        return { errors: e, warnings: w };
    }
    if (!o) {
        return null;
    }
    e = Array.isArray(o.errors) ? o.errors.length : 0;
    w = Array.isArray(o.warnings) ? o.warnings.length : 0;
    return { errors: e, warnings: w };
}

/// 全量诊断行——错误在前、警告在后（空 → 显式占位）
function tsDiagLines(o) {
    var out = [];
    var errs = Array.isArray(o.errors) ? o.errors : [];
    var warns = Array.isArray(o.warnings) ? o.warnings : [];
    var i;
    for (i = 0; i < errs.length; i = i + 1) {
        out.push(tsDiagLine(errs[i], '✖'));
    }
    for (i = 0; i < warns.length; i = i + 1) {
        out.push(tsDiagLine(warns[i], '⚠'));
    }
    if (out.length === 0) {
        out.push('（无类型错误）');
    }
    return out;
}

toolDecl('ts-check', {
    inputLines: function (a) {
        return ['TS 类型检查 ' + ovText(a.path) + (a.full === true ? ' · 含警告' : '')];
    },
    headline: function (a, r) {
        var head = 'TS 检查 ' + ovShort(a.path);
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
        var c = tsDiagCounts(o);
        var mode = (ovMetaStr(o, 'mode') === 'single') ? ' · 单文件' : '';
        return head + ' · ' + c.errors + ' 错 ' + c.warnings + ' 警 · ' + ovMetaNum(o, 'files', 0) + ' 文件' + mode;
    },
    badge: function (r) {
        var o = ovJson(r);
        if (!o || o.ok === false) {
            return '';
        }
        var c = tsDiagCounts(o);
        return ' · ' + c.errors + ' 错 ' + c.warnings + ' 警';
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
        return tsDiagLines(o);
    }
});
