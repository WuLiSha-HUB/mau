// ═══════════════════════════════════════════
// chat/tools/group/group-delegate.js —— group-delegate / 派活并唤醒（GroupCat）
// 声明：专属图标 📤 + 输入意图行（组名 / @ 目标 / 正文字数）+ 折叠行读落位序号
//       + 输出两行（已入组 / 已唤醒目标）。
// 命名（2026-11-05 莎裁 β）：旧名 group-send——"send" 读不出「叫醒某只猫干活」这层语义。
// 回执形态（A214）：首行元数据头 {ok,tool,target,seq,to} + 摘要行（无载荷正文）。
// ═══════════════════════════════════════════

toolDecl('group-delegate', {
    icon: '📤',
    inputLines: function (a) {
        var len = (typeof a.text === 'string') ? a.text.length : 0;
        var line = '派活 → ' + ovText(a.to) + ' · 组 ' + ovText(a.group) + ' · 正文 ' + len + ' 字符';
        if (typeof a.refs === 'string' && a.refs.length > 0) {
            line = line + ' · 关联 ' + a.refs;
        }
        return [line];
    },
    headline: function (a, r) {
        var head = '派活 → ' + ovText(a.to);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var h = ovHead(r);
        if (!h) {
            return head;
        }
        return head + ' · #' + ovMetaNum(h.meta, 'seq', 0) + ' 已唤醒';
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
        return ['已入组 #' + ovMetaNum(h.meta, 'seq', 0) + '（组 ' + ovMetaStr(h.meta, 'target') + '）', '已唤醒 ' + to + ' —— 目标猫轮末自动回执'];
    }
});
