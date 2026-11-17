// ═══════════════════════════════════════════
// chat/tools/qqbot/qqbot-info.js —— qqbot-info / 通道自述（QqBotCat）
// 声明：专属图标 📡 + 输入意图行 + 折叠行读绑定态
//       + 输出逐行（通道头 / 绑定与开关 / 每机器人一行：名称·环境·连接·群摘要）。
// 回执形态（MyChannelText，无结构化头）：
//   "QQBot 通道 · 猫: <catId>"
//   "· 绑定 <botKey> · 收信 开|关 · 发言 开|关 · 渠道 <…>" 或 "· 未绑定机器人——…"
//   "· <显示名>（默认）· <sandbox|production> · 已连接|未连接 · 群: <…>"
// ═══════════════════════════════════════════

/// 非空行过滤——保留原始缩进与内容
toolDecl('qqbot-info', {
    icon: '📡',
    inputLines: function () {
        return ['查看本猫绑定的机器人（连接状态 / 接入环境 / 可收信群）'];
    },
    headline: function (a, r) {
        if (r === undefined) {
            return 'QQBot 通道 · 处理中';
        }
        var bind = ovLineField(r, '· 绑定 ');
        if (bind.length > 0) {
            return 'QQBot 通道 · 绑定 ' + bind.split(' · ')[0];
        }
        if (typeof r === 'string' && r.indexOf('· 未绑定机器人') >= 0) {
            return 'QQBot 通道 · 未绑定机器人';
        }
        return 'QQBot 通道自述';
    },
    outputLines: function (r) {
        if (r === undefined) {
            return null;
        }
        var out = ovNonEmptyLines(ovBodyLines(r), 0);
        if (out.length === 0) {
            out.push('（无通道信息）');
        }
        return out;
    }
});
