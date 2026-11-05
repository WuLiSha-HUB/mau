// ═══════════════════════════════════════════
// chat/tools/group/group-post.js —— group-post / 组内发言（GroupCat）
// 声明：专属图标 💬 + 输入意图行（组名 / 字数）+ 折叠行读序号 + 输出一行落位回执。
// 命名（2026-11-05 莎裁 β）：旧名 group-say；to 参数已删——发言只写组流、不唤醒任何人。
// 回执形态（A214）：首行元数据头 {ok,tool,target,seq} + 摘要行（无载荷正文）。
// ═══════════════════════════════════════════

toolDecl('group-post', {
    icon: '💬',
    inputLines: function (a) {
        var len = (typeof a.text === 'string') ? a.text.length : 0;
        return ['组 ' + ovText(a.group) + ' · 正文 ' + len + ' 字符'];
    },
    headline: function (a, r) {
        var head = '在 ' + ovText(a.group) + ' 发言';
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var h = ovHead(r);
        if (!h) {
            return head;
        }
        return head + ' · #' + ovMetaNum(h.meta, 'seq', 0);
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var h = ovHead(r);
        if (!h) {
            return null;
        }
        return ['已记入组流水 · #' + ovMetaNum(h.meta, 'seq', 0) + '（不唤醒任何人）'];
    }
});
