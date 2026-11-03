// ═══════════════════════════════════════════
// chat/tools/group/group-inbox.js —— group-inbox / 组消息拉取（GroupCat）
// 声明：专属图标 📥 + 输入意图行（组名 / 起始序号 / 上限）+ 折叠行读条数与最新序号
//       + 输出逐条列（服务端已格式化为「#seq [类型] 发方 → 收方 · 正文」）。
// 回执形态（A214）：首行元数据头 {ok,tool,target,items,lastSeq} + 摘要行 + 逐消息行。
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
        var h = ovHead(r);
        if (!h) {
            return head;
        }
        return head + ' · ' + ovMetaNum(h.meta, 'items', 0) + ' 条 · 最新 #' + ovMetaNum(h.meta, 'lastSeq', 0);
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var lines = ovBodyLines(r);
        var out = [];
        for (var i = 1; i < lines.length; i = i + 1) {
            if (lines[i].replace(/\s/g, '').length > 0) {
                out.push(lines[i]);
            }
        }
        if (out.length === 0) {
            out.push('（无消息）');
        }
        return out;
    }
});
