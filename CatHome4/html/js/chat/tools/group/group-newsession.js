// ═══════════════════════════════════════════
// chat/tools/group/group-newsession.js —— group-newsession / 重建会话（GroupCat）
// 声明：专属图标 ♻️ + 输入意图行（组名 / @ 目标 / 原因字数 / 初始指令字数）
//       + 输出两行（已入组 / 已挂起或已重建）。
// 回执形态（A214）：首行元数据头 {ok,tool,target,seq,to,mode}（mode ∈ queued / rebuilt）+ 摘要行。
// ═══════════════════════════════════════════

toolDecl('group-newsession', {
    icon: '♻️',
    inputLines: function (a) {
        var reason = (typeof a.reason === 'string') ? a.reason.length : 0;
        var line = '重建会话 → ' + ovText(a.to) + ' · 组 ' + ovText(a.group) + ' · 原因 ' + reason + ' 字符';
        if (typeof a.firstMessage === 'string' && a.firstMessage.length > 0) {
            line = line + ' · 初始指令 ' + a.firstMessage.length + ' 字符';
        }
        return [line];
    },
    headline: function (a, r) {
        var head = '重建会话 → ' + ovText(a.to);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var h = ovHead(r);
        if (!h) {
            return head;
        }
        if (ovMetaStr(h.meta, 'mode') === 'queued') {
            return head + ' · 已挂起（轮末生效）';
        }
        return head + ' · 已重建';
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var h = ovHead(r);
        if (!h) {
            return null;
        }
        var to = ovMetaStr(h.meta, 'to');
        if (to.length === 0) {
            to = ovText(a.to);
        }
        var lines = ['已入组 #' + ovMetaNum(h.meta, 'seq', 0) + '（组 ' + ovMetaStr(h.meta, 'target') + '）'];
        if (ovMetaStr(h.meta, 'mode') === 'queued') {
            lines.push(to + ' 的会话将在本轮收尾后重建');
        } else {
            lines.push(to + ' 的会话已重建（前文清空 + 重注入）');
        }
        return lines;
    }
});
