// ═══════════════════════════════════════════
// chat/tools/group/group-info.js —— group-info / 组信息（GroupCat）
// 声明：专属图标 👥 + 输入意图行（组名）+ 折叠行读成员数 / 消息数 + 输出逐成员列（显示名 / key / 角色）。
// 回执形态：整行 JSON {ok,tool,name,project,enabled,msgCount,lastSeq,lastAt,members[{key,displayName,admin,subscriber}]}
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
        var o = ovWholeJson(r);
        if (!o) {
            return head;
        }
        var members = Array.isArray(o.members) ? o.members : [];
        return head + ' · ' + members.length + ' 名成员 · 消息 ' + ovMetaNum(o, 'msgCount', 0) + ' 条';
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var o = ovWholeJson(r);
        if (!o) {
            return null;
        }
        var members = Array.isArray(o.members) ? o.members : [];
        var out = [];
        for (var i = 0; i < members.length; i = i + 1) {
            var m = members[i];
            var roles = [];
            if (m.admin === true) {
                roles.push('管理员');
            }
            if (m.subscriber === true) {
                roles.push('订阅');
            }
            var line = '· ' + ovMetaStr(m, 'displayName') + '（' + ovMetaStr(m, 'key') + '）';
            if (roles.length > 0) {
                line = line + ' — ' + roles.join(' / ');
            }
            out.push(line);
        }
        if (out.length === 0) {
            out.push('（无成员）');
        }
        var proj = ovMetaStr(o, 'project');
        out.unshift('组 ' + ovMetaStr(o, 'name') + (proj.length > 0 ? (' · 项目 ' + proj) : '') + ' · 最新 #' + ovMetaNum(o, 'lastSeq', 0));
        return out;
    }
});
