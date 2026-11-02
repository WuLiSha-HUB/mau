// ═══════════════════════════════════════════
// chat/tools/db/db-query.js —— db-query / 条目库查询（DbCat）
// 声明：专属图标 🔍 + 输入意图行（库名 / 关键词 / 上限）+ 折叠行读三数（total / matched / shown）
//       + 输出逐条列（ID / 标题 / 更新时间）；段结构归骨架 `listing`。
// 回执形态（DbService.ToToolText）：结构化头 {ok,tool,target[,badLines]} + 摘要行 + 载荷 {total,matched,shown,items[]}
// ═══════════════════════════════════════════

toolDecl('db-query', {
    icon: '🔍',
    inputLines: function (a) {
        var kw = (typeof a.keyword === 'string' && a.keyword.length > 0) ? ('关键词 ' + ovPeek(a.keyword)) : '全库列表';
        var lim = (a.limit === undefined) ? '默认上限' : ('上限 ' + ovText(a.limit));
        return ['查询库 ' + ovText(a.lib) + ' · ' + kw + ' · ' + lim];
    },
    headline: function (a, r) {
        var kw = (typeof a.keyword === 'string' && a.keyword.length > 0) ? ('「' + ovPeek(a.keyword) + '」') : '全库';
        var head = '查询 ' + ovText(a.lib) + ' · ' + kw;
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var o = ovPayload(r);
        if (!o) {
            return head;
        }
        return head + ' · 命中 ' + ovMetaNum(o, 'matched', 0) + ' / 活跃 ' + ovMetaNum(o, 'total', 0);
    },
    badge: function (r) {
        var o = ovPayload(r);
        if (!o) {
            return '';
        }
        return ' · 命中 ' + ovMetaNum(o, 'matched', 0) + ' / 活跃 ' + ovMetaNum(o, 'total', 0);
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var o = ovPayload(r);
        if (!o) {
            return null;
        }
        var items = Array.isArray(o.items) ? o.items : [];
        var out = [];
        for (var i = 0; i < items.length; i = i + 1) {
            var it = items[i];
            var id = ovMetaNum(it, 'id', 0);
            var line = '#' + id + ' · ' + ovMetaStr(it, 'title');
            var when = ovMetaStr(it, 'updatedAt');
            if (when.length > 0) {
                line = line + ' · ' + when;
            }
            out.push(line);
        }
        if (out.length === 0) {
            out.push('（无命中条目）');
        }
        var shown = ovMetaNum(o, 'shown', out.length);
        var matched = ovMetaNum(o, 'matched', out.length);
        if (shown < matched) {
            out.push('… 仅列前 ' + shown + ' 条（命中 ' + matched + '）');
        }
        return out;
    }
});
