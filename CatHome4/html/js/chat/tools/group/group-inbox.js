// ═══════════════════════════════════════════
// chat/tools/group/group-inbox.js —— group-inbox / 组消息拉取（GroupCat）
// 声明：专属图标 📥 + 输入意图行（组名 / 起始序号 / 上限）+ 折叠行读条数与最新序号
//       + 输出逐条列（#seq · 类型 · 发方 → 收方 · 摘要）。
// 回执形态：整行 JSON {ok,tool,name,lastSeq,messages[{seq,from,fromName,to,toName,type,text,ts,refs}]}
// ═══════════════════════════════════════════

toolDecl('group-inbox', {
    icon: '📥',
    inputLines: function (a) {
        var after = (a.after === undefined) ? '从头' : ('# ' + ovText(a.after) + ' 之后');
        var lim = (a.limit === undefined) ? '默认上限' : ('上限 ' + ovText(a.limit));
        return ['组 ' + ovText(a.group) + ' · ' + after + ' · ' + lim];
    },
    headline: function (a, r) {
        var head = '拉取 ' + ovText(a.group);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var o = ovWholeJson(r);
        if (!o) {
            return head;
        }
        var msgs = Array.isArray(o.messages) ? o.messages : [];
        return head + ' · ' + msgs.length + ' 条 · 最新 #' + ovMetaNum(o, 'lastSeq', 0);
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var o = ovWholeJson(r);
        if (!o) {
            return null;
        }
        var msgs = Array.isArray(o.messages) ? o.messages : [];
        var out = [];
        for (var i = 0; i < msgs.length; i = i + 1) {
            var m = msgs[i];
            var type = ovMetaStr(m, 'type');
            var label = '发言';
            if (type === 'delegation') {
                label = '派单';
            } else if (type === 'receipt') {
                label = '回执';
            }
            var from = ovMetaStr(m, 'fromName');
            if (from.length === 0) {
                from = ovMetaStr(m, 'from');
            }
            var to = ovMetaStr(m, 'toName');
            var route = (to.length > 0) ? (from + ' → ' + to) : from;
            var body = ovMetaStr(m, 'text').replace(/\s+/g, ' ');
            if (body.length > 60) {
                body = body.slice(0, 60) + '…';
            }
            out.push('#' + ovMetaNum(m, 'seq', 0) + ' [' + label + '] ' + route + ' · ' + body);
        }
        if (out.length === 0) {
            out.push('（无消息）');
        }
        return out;
    }
});
