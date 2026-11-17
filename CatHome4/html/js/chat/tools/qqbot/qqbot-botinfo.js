// ═══════════════════════════════════════════
// chat/tools/qqbot/qqbot-botinfo.js —— qqbot-botinfo / 机器人详情（QqBotCat）
// 声明：专属图标 🤖 + 输入意图行（机器人 + 是否刷新）+ 折叠行读群数
//       + 输出逐群一行（别名/群名 · OpenID 末 8 位 · 角色 · 收信 · 主动发言 · 最近）；
//       段结构归骨架 `listing`。首行汇总进折叠行，末行「发言白名单」保留在正文。
// 回执形态（BotDetailText）：
//   "QQBot | <名> · AppID <id> · 群 N 个[ · 已刷新 X 群][ · 官方接口不可用…]"
//   "· <Label> · <Tail8> · 角色: … · 收信: … · 主动发言: … · 最近: …"
//   ["发言白名单: <…>"]
// ═══════════════════════════════════════════

toolDecl('qqbot-botinfo', {
    icon: '🤖',
    inputLines: function (a) {
        var which = (a.bot === undefined || a.bot === '') ? '（默认机器人）' : ovText(a.bot);
        var lines = ['机器人 ' + which];
        if (a.refresh === true) {
            lines.push('顺带刷新官方权限与群名');
        }
        return lines;
    },
    headline: function (a, r) {
        var which = (a.bot === undefined || a.bot === '') ? '默认机器人' : ovText(a.bot);
        var head = '机器人详情 ' + which;
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var n = ovCount(r, '个');
        if (n.length > 0) {
            head = head + ' · 群 ' + n + ' 个';
        }
        if (typeof r === 'string' && r.indexOf('官方接口不可用') >= 0) {
            head = head + ' · 官方接口降级';
        }
        return head;
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var out = ovNonEmptyLines(ovBodyLines(r), 1);
        if (out.length === 0) {
            out.push('（目录内无群）');
        }
        return out;
    }
});
