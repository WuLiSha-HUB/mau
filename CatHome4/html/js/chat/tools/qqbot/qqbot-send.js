// ═══════════════════════════════════════════
// chat/tools/qqbot/qqbot-send.js —— qqbot-send / 主动发言（QqBotCat）
// 声明：专属图标 📤 + 输入意图行（目标群 + 正文预览）+ 折叠行读投递结果
//       + 输出逐行（投递目标 / 机器人 / 群内主动发言权限）；失败态由折叠行 ⚠️ 承担。
// 回执形态（QqBotService.Ops.cs）：
//   成功 = "<目标> | 主动消息已投递" + "\n机器人: <名> · 群内主动发言权限: <允许|未知（官方接口未确认）>"
//   失败 = "ERR|<码>|<说明>" + ["\n目标: <目标>"]
// ═══════════════════════════════════════════

toolDecl('qqbot-send', {
    icon: '📤',
    inputLines: function (a) {
        var group = (a.group === undefined || a.group === '') ? '（目录内唯一群）' : ovText(a.group);
        return ['群 ' + group, '正文 ' + ovPeek(a.text)];
    },
    headline: function (a, r) {
        var target = (a.group === undefined || a.group === '') ? '默认群' : ovText(a.group);
        if (r === undefined) {
            return 'QQ 发言 ' + target + ' · 处理中';
        }
        if (typeof r === 'string' && r.indexOf('ERR') === 0) {
            return 'QQ 发言 ' + target + ' · 失败';
        }
        return 'QQ 发言 ' + target + ' · 已投递';
    },
    outputLines: function (r) {
        if (r === undefined) {
            return null;
        }
        var lines = ovBodyLines(r);
        var out = [];
        for (var i = 0; i < lines.length; i = i + 1) {
            if (lines[i].replace(/\s/g, '').length > 0) {
                out.push(lines[i]);
            }
        }
        if (out.length === 0) {
            out.push('（无回执正文）');
        }
        return out;
    }
});
