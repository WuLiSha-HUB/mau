// ═══════════════════════════════════════════
// chat/tools/group/group-say.js —— group-say / 组内发言（GroupCat）
// 声明：专属图标 💬 + 输入意图行（组名 / 字数 / @ 对象）+ 折叠行读序号 + 输出一行落位回执。
// 回执形态：整行 JSON {ok,tool,group,seq}；失败为 ERR| 前缀文本（走失败态）。
// ═══════════════════════════════════════════

toolDecl('group-say', {
    icon: '💬',
    inputLines: function (a) {
        var len = (typeof a.text === 'string') ? a.text.length : 0;
        var line = '组 ' + ovText(a.group) + ' · 正文 ' + len + ' 字符';
        if (typeof a.to === 'string' && a.to.length > 0) {
            line = line + ' · @ ' + a.to;
        }
        return [line];
    },
    headline: function (a, r) {
        var head = '在 ' + ovText(a.group) + ' 发言';
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var o = ovWholeJson(r);
        if (!o) {
            return head;
        }
        return head + ' · #' + ovMetaNum(o, 'seq', 0);
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var o = ovWholeJson(r);
        if (!o) {
            return null;
        }
        return ['已记入组流水 · #' + ovMetaNum(o, 'seq', 0) + '（不投递任何人）'];
    }
});
