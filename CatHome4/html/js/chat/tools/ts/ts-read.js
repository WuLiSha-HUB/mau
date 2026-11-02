// ═══════════════════════════════════════════
// chat/tools/ts/ts-read.js —— ts-read / TypeScript 符号源码读取（TsCat · A294）
// 声明：输入意图行（文件 + 符号 / 概览）+ 折叠行读区间与声明种类 + 输出带行号源码段；
//       段结构归骨架 `lines`（行号列 + 内容——正文行首自带 `N: ` 前缀，骨架直接切列）。
// 回执形态（stdio_server ts.read）：单段 JSON {ok,file,symbol,kind,text,range{start,end},overview?,candidates?}
// ═══════════════════════════════════════════

toolDecl('ts-read', {
    inputLines: function (a) {
        var sym = (typeof a.symbol === 'string' && a.symbol.length > 0) ? (' · 符号 ' + a.symbol) : ' · 文件概览';
        return ['读 ' + ovText(a.file) + sym];
    },
    headline: function (a, r) {
        var head = '读取 ' + ovShort(a.file);
        var sym = (typeof a.symbol === 'string' && a.symbol.length > 0) ? (a.symbol + ' · ') : '';
        if (r === undefined) {
            return head + (sym.length > 0 ? ' · ' + a.symbol : '') + ' · 处理中';
        }
        var o = ovJson(r);
        if (!o) {
            return head + ovStat(r);
        }
        if (o.ok === false) {
            return head + ' · 服务失败';
        }
        var range = (o.range && typeof o.range === 'object') ? o.range : null;
        var span = '';
        if (range && ovMetaNum(range, 'start', 0) > 0) {
            span = ' · L' + ovMetaNum(range, 'start', 0) + '-' + ovMetaNum(range, 'end', 0);
        }
        var kind = ovMetaStr(o, 'kind');
        var tag = o.overview === true ? '概览' : (kind.length > 0 ? kind : '');
        var mid = (tag.length > 0 ? ' · ' + tag : '');
        return head + (sym.length > 0 ? ' · ' + a.symbol : '') + mid + span;
    },
    badge: function (r) {
        var o = ovJson(r);
        if (!o || o.ok === false) {
            return '';
        }
        var range = (o.range && typeof o.range === 'object') ? o.range : null;
        if (!range || ovMetaNum(range, 'start', 0) <= 0) {
            return '';
        }
        return ' · L' + ovMetaNum(range, 'start', 0) + '-' + ovMetaNum(range, 'end', 0);
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
        var text = ovMetaStr(o, 'text');
        if (text.length === 0) {
            return ['（无源码内容）'];
        }
        return text.split('\n');
    }
});
