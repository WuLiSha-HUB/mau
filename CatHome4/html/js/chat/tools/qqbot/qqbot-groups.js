// ═══════════════════════════════════════════
// chat/tools/qqbot/qqbot-groups.js —— qqbot-groups / 群目录维护（QqBotCat）
// 声明：专属图标 🗂 + 输入意图行（动作 + 群 + 别名 + 机器人）+ 折叠行读动作与规模
//       + 输出逐行。动作面分两种回执形态：
//   list（默认）/ 省略 action → BotDetailText（同 qqbot-botinfo，首行进折叠行）
//   alias / add / remove → 单行结果文本
//   refresh → "刷新官方状态 · <名> · 群 N 个" + 逐群「· Label · Tail8 → 已刷新|未确认…」+ "成功 X / N"
// ═══════════════════════════════════════════

/// 动作名——缺省与显式 list 归一
function qqbotGroupsAction(a) {
    if (a.action === undefined || a.action === '') {
        return 'list';
    }
    return String(a.action);
}

toolDecl('qqbot-groups', {
    icon: '🗂',
    inputLines: function (a) {
        var lines = ['动作 ' + qqbotGroupsAction(a)];
        if (a.group !== undefined && a.group !== '') {
            lines.push('群 ' + ovText(a.group));
        }
        if (a.alias !== undefined && a.alias !== '') {
            lines.push('别名 ' + ovText(a.alias));
        }
        if (a.bot !== undefined && a.bot !== '') {
            lines.push('机器人 ' + ovText(a.bot));
        }
        return lines;
    },
    headline: function (a, r) {
        var act = qqbotGroupsAction(a);
        var head = '群目录 ' + act;
        if (r === undefined) {
            return head + ' · 处理中';
        }
        if (act === 'list') {
            var n = ovCount(r, '个');
            if (n.length > 0) {
                head = head + ' · 群 ' + n + ' 个';
            }
        }
        if (act === 'refresh') {
            var ok = ovLineField(r, '成功 ');
            if (ok.length > 0) {
                head = head + ' · 成功 ' + ok;
            }
        }
        return head;
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var act = qqbotGroupsAction(a);
        var start = (act === 'list') ? 1 : 0;
        var out = ovNonEmptyLines(ovBodyLines(r), start);
        if (out.length === 0) {
            out.push('（目录内无群）');
        }
        return out;
    }
});
