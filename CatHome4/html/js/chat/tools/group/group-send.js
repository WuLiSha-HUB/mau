// ═══════════════════════════════════════════
// chat/tools/group/group-send.js —— group-send / 派单（GroupCat）
// 声明：专属图标 📤 + 输入意图行（组名 / @ 目标 / 正文字数）+ 折叠行读落位序号
//       + 输出两行（已入组 / 已投递）。
// 回执形态：整行 JSON {ok,tool,group,seq,to}；失败为 ERR| 前缀文本（走失败态）。
// ═══════════════════════════════════════════

toolDecl('group-send', {
    icon: '📤',
    inputLines: function (a) {
        var len = (typeof a.text === 'string') ? a.text.length : 0;
        var line = '派单 → ' + ovText(a.to) + ' · 组 ' + ovText(a.group) + ' · 正文 ' + len + ' 字符';
        if (typeof a.refs === 'string' && a.refs.length > 0) {
            line = line + ' · 关联 ' + a.refs;
        }
        return [line];
    },
    headline: function (a, r) {
        var head = '派单 → ' + ovText(a.to);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var o = ovWholeJson(r);
        if (!o) {
            return head;
        }
        return head + ' · #' + ovMetaNum(o, 'seq', 0) + ' 已投递';
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var o = ovWholeJson(r);
        if (!o) {
            return null;
        }
        var seq = ovMetaNum(o, 'seq', 0);
        var to = ovMetaStr(o, 'to');
        if (to.length === 0) {
            to = ovText(a.to);
        }
        return ['已入组 #' + seq + '（组 ' + ovMetaStr(o, 'group') + '）', '已投递给 ' + to + ' —— 目标猫轮末自动回执'];
    }
});
