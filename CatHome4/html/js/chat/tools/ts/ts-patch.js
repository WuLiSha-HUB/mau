// ═══════════════════════════════════════════
// chat/tools/ts/ts-patch.js —— ts-patch / TypeScript 符号与文本改写（TsCat · A294）
// 声明：输入意图行（符号模式 / 替换模式）+ 折叠行读变更行数与诊断计数 + 输出 diff 逐行与诊断；
//       段结构归骨架 `text`（正文块——声明层 outputLines 接管为 diff 行）。
// 回执形态（stdio_server ts.patch）：单段 JSON {ok,file,mode,changedLines,diff[],diagnostics[],errorCount}
// ═══════════════════════════════════════════

/// 诊断条目 → 单行（复用 ts-check 的格式化口径；本件独立实现避免跨件依赖顺序）
function tsPatchDiagLine(d) {
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
    var out = '✖ ' + where;
    if (code.length > 0) {
        out = out + ' ' + (code.indexOf('TS') === 0 ? code : 'TS' + code);
    }
    if (msg.length > 0) {
        out = out + ' ' + msg;
    }
    return out;
}

toolDecl('ts-patch', {
    inputLines: function (a) {
        var head = '改写 ' + ovText(a.file);
        if (typeof a.symbol === 'string' && a.symbol.length > 0) {
            return [head + ' · 符号 ' + a.symbol];
        }
        return [head + ' · 文本替换'];
    },
    headline: function (a, r) {
        var head = '改写 ' + ovShort(a.file);
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
        var changed = ovMetaNum(o, 'changedLines', 0);
        var errs = ovMetaNum(o, 'errorCount', 0);
        var mode = (ovMetaStr(o, 'mode') === 'symbol') ? ' · 符号' : ' · 替换';
        return head + mode + ' · ' + changed + ' 行变更' + (errs > 0 ? (' · ' + errs + ' 诊断') : '');
    },
    badge: function (r) {
        var o = ovJson(r);
        if (!o || o.ok === false) {
            return '';
        }
        var errs = ovMetaNum(o, 'errorCount', 0);
        return ' · ' + ovMetaNum(o, 'changedLines', 0) + ' 行' + (errs > 0 ? (' · ' + errs + ' 诊断') : '');
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
        var diff = Array.isArray(o.diff) ? o.diff : [];
        for (var i = 0; i < diff.length; i = i + 1) {
            if (typeof diff[i] === 'string') {
                out.push(diff[i]);
            }
        }
        if (out.length === 0) {
            out.push('（无变更）');
        }
        var diags = Array.isArray(o.diagnostics) ? o.diagnostics : [];
        for (var j = 0; j < diags.length; j = j + 1) {
            out.push(tsPatchDiagLine(diags[j]));
        }
        return out;
    }
});
