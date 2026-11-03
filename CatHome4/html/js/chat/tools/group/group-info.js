// ═══════════════════════════════════════════
// chat/tools/group/group-info.js —— group-info / 组信息（GroupCat）
// 声明：专属图标 👥 + 输入意图行（组名）+ 折叠行读成员数 / 消息数 + 输出逐成员列。
// 回执形态（A214）：首行元数据头 {ok,tool,target,items,project,enabled,msgCount,lastSeq,lastAt}
//                   + 摘要行 + 逐成员行（· 显示名（key）— 角色）。
// ═══════════════════════════════════════════

toolDecl('group-info', {
    icon: '👥',
    inputLines: function (a) {
        return ['组 ' + ovText(a.group)];
    },
    headline: function (a, r) {
        var head = '组信息 ' + ovText(a.group);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var h = ovHead(r);
        if (!h) {
            return head;
        }
        var proj = ovMetaStr(h.meta, 'project');
        var tail = (proj.length > 0) ? (' · 项目 ' + proj) : '';
        return head + tail + ' · ' + ovMetaNum(h.meta, 'items', 0) + ' 名成员 · 消息 ' + ovMetaNum(h.meta, 'msgCount', 0) + ' 条';
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
            out.push('（无成员）');
        }
        return out;
    }
});
