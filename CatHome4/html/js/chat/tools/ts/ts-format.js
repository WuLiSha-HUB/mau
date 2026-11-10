// ═══════════════════════════════════════════
// chat/tools/ts/ts-format.js —— ts-format / TypeScript 文本层规整（TsCat · A332 工具 / A335 前端件）
// 声明：输入意图行（工程 + 干跑 / 写盘）+ 折叠行读差异规模 + 输出逐文件差异与缩进口径；
//       段结构归骨架 `diagnostics`（诊断列表——逐文件差异）。
// 回执形态（stdio_server ts.format）：单段 JSON {ok,mode,files,changedFiles,changedLines,indent,
//       items[{file,changedLines,written,error?}]}
// ═══════════════════════════════════════════

/// 差异条目 → 单行（file · N 行差异 · 已写盘 / 写盘失败原因）
function tsFormatItemLine(it) {
    if (!it || typeof it !== 'object') {
        return '';
    }
    var file = ovMetaStr(it, 'file');
    var lines = ovMetaNum(it, 'changedLines', 0);
    var out = (file.length > 0 ? file : '(未命名)') + ' · ' + lines + ' 行差异';
    var err = ovMetaStr(it, 'error');
    if (err.length > 0) {
        out = out + ' · 写盘失败：' + err;
    } else if (it.written === true) {
        out = out + ' · 已写盘';
    }
    return out;
}

toolDecl('ts-format', {
    inputLines: function (a) {
        var mode = (typeof a.mode === 'string' && a.mode.length > 0) ? a.mode : 'check';
        return ['TS 格式规整 ' + ovText(a.path) + ' · ' + mode];
    },
    headline: function (a, r) {
        var head = '格式 ' + ovShort(a.path);
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
        var mode = ovMetaStr(o, 'mode');
        return head + ' · ' + (mode.length > 0 ? mode : 'check') + ' · ' + ovMetaNum(o, 'changedFiles', 0) + ' 文件差异';
    },
    badge: function (r) {
        var o = ovJson(r);
        if (!o || o.ok === false) {
            return '';
        }
        return ' · ' + ovMetaNum(o, 'changedFiles', 0) + ' 文件差异';
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
        out.push('mode ' + ovText(ovMetaStr(o, 'mode')) + ' · ' + ovMetaNum(o, 'files', 0) + ' 文件 · 差异 ' + ovMetaNum(o, 'changedFiles', 0) + ' 文件 / ' + ovMetaNum(o, 'changedLines', 0) + ' 行');
        var indent = ovMetaStr(o, 'indent');
        if (indent.length > 0) {
            out.push('缩进口径 ' + indent);
        }
        var items = Array.isArray(o.items) ? o.items : [];
        for (var i = 0; i < items.length; i = i + 1) {
            out.push(tsFormatItemLine(items[i]));
        }
        if (items.length === 0) {
            out.push('（零差异——无需规整）');
        }
        return out;
    }
});
