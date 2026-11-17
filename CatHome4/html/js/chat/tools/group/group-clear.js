// ═══════════════════════════════════════════
// chat/tools/group/group-clear.js —— group-clear / 清空组消息流（GroupCat）
// 声明：专属图标 🧹 + 输入意图行（组名）+ 折叠行读清空条数 + 输出摘要行与正文（归档文件名）。
// 回执形态（A214）：首行元数据头 {ok,tool,target,items,archived,clearedCount,clearedSeq}
//                   + 摘要行 + 正文两行（归档说明 / 游标归零说明）。
// 语义：旧流归档为 messages.<时间戳>.jsonl.bak（可恢复），新流从 #1 起；仅组管理员可调。
// ═══════════════════════════════════════════

toolDecl('group-clear', {
    icon: '🧹',
    inputLines: function (a) {
        return ['组 ' + ovText(a.group) + ' · 清空消息流（旧流归档保留）'];
    },
    headline: function (a, r) {
        var head = '清空 ' + ovText(a.group) + ' 消息流';
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var h = ovHead(r);
        if (!h) {
            return head;
        }
        return head + ' · ' + ovMetaNum(h.meta, 'clearedCount', 0) + ' 条';
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var h = ovHead(r);
        if (!h) {
            return null;
        }
        var out = [];
        var first = ovBodyFirst(r);
        if (first.length > 0) {
            out.push(first);
        }
        var rest = ovNonEmptyLines(ovBodyLines(r), 1);
        for (var i = 0; i < rest.length; i = i + 1) {
            out.push(rest[i]);
        }
        return out;
    }
});
