// ═══════════════════════════════════════════
// chat/tools/ts/ts-test.js —— ts-test / TypeScript 跑测窗口（TsCat · A332 工具 / A335 前端件）
// 声明：输入意图行（工程 + 用例过滤）+ 折叠行读通过率与用时 + 输出计数三元组与失败明细；
//       段结构归骨架 `diagnostics`（诊断列表——失败用例逐条）。
// 回执形态（stdio_server ts.test）：单段 JSON {ok,runner,root,files,cases,passed,failed,skipped,
//       failures[],elapsedMs} | 失败回执 {ok:false,error,message}
// ═══════════════════════════════════════════

/// 毫秒 → 「3.5s」/「420ms」
function tsTestDur(ms) {
    var n = ovMetaNum({ d: ms }, 'd', 0);
    if (n <= 0) {
        return '';
    }
    if (n < 1000) {
        return n + 'ms';
    }
    return (n / 1000).toFixed(1) + 's';
}

/// 通过率文案——「245/246 通过 · 1 失败」；全绿省略失败段
function tsTestRatio(o) {
    var cases = ovMetaNum(o, 'cases', 0);
    var passed = ovMetaNum(o, 'passed', 0);
    var failed = ovMetaNum(o, 'failed', 0);
    var skipped = ovMetaNum(o, 'skipped', 0);
    var text = passed + '/' + cases + ' 通过';
    if (failed > 0) {
        text = text + ' · ' + failed + ' 失败';
    }
    if (skipped > 0) {
        text = text + ' · ' + skipped + ' 跳过';
    }
    return text;
}

toolDecl('ts-test', {
    inputLines: function (a) {
        var filter = (typeof a.filter === 'string' && a.filter.length > 0) ? (' · 过滤 ' + a.filter) : '';
        return ['TS 跑测 ' + ovText(a.path) + filter];
    },
    headline: function (a, r) {
        var head = '跑测 ' + ovShort(a.path);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var o = ovJson(r);
        if (!o) {
            return head + ovStat(r);
        }
        if (o.ok === false) {
            if (ovMetaStr(o, 'error').length > 0) {
                return head + ' · ' + ovMetaStr(o, 'error');
            }
            return head + ' · ' + tsTestRatio(o);
        }
        var dur = tsTestDur(ovMetaNum(o, 'elapsedMs', 0));
        return head + ' · ' + tsTestRatio(o) + (dur.length > 0 ? ' · ' + dur : '');
    },
    badge: function (r) {
        var o = ovJson(r);
        if (!o) {
            return '';
        }
        if (o.ok === false && ovMetaStr(o, 'error').length > 0) {
            return ' · ' + ovMetaStr(o, 'error');
        }
        var failed = ovMetaNum(o, 'failed', 0);
        if (failed > 0) {
            return ' · ' + failed + ' 失败';
        }
        return ' · ' + ovMetaNum(o, 'passed', 0) + ' 通过';
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var o = ovJson(r);
        if (!o) {
            return null;
        }
        if (o.ok === false && ovMetaStr(o, 'error').length > 0) {
            return ['ERR|' + ovMetaStr(o, 'error') + '|' + ovMetaStr(o, 'message')];
        }
        var out = [];
        var dur = tsTestDur(ovMetaNum(o, 'elapsedMs', 0));
        out.push(ovText(ovMetaStr(o, 'runner')) + ' · ' + ovMetaNum(o, 'files', 0) + ' 文件 · ' + ovMetaNum(o, 'cases', 0) + ' 用例（通过 ' + ovMetaNum(o, 'passed', 0) + ' / 失败 ' + ovMetaNum(o, 'failed', 0) + ' / 跳过 ' + ovMetaNum(o, 'skipped', 0) + '）' + (dur.length > 0 ? ' · ' + dur : ''));
        var failures = Array.isArray(o.failures) ? o.failures : [];
        for (var i = 0; i < failures.length; i = i + 1) {
            if (typeof failures[i] === 'string' && failures[i].length > 0) {
                out.push(failures[i]);
            }
        }
        if (failures.length === 0) {
            out.push('（无失败用例）');
        }
        return out;
    }
});
