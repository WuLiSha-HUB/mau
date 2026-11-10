// ═══════════════════════════════════════════
// chat/tools/builtin/sleep.js —— sleep / 登记定时唤醒（内置）
// 声明：专属图标 ⏳ + 折叠行（等待时长）+ 输入意图行；同轮帧等待
//（到点即回本批；允许对话注入时，等待期间的人输入即打断等待——A327）。时长一律以秒示人。
// ═══════════════════════════════════════════

toolDecl('sleep', {
    icon: '⏳',
    inputLines: function (a) {
        var d = ovDurText(a);
        return ['登记定时唤醒' + ((d.length > 0) ? (' · ' + d) : '')];
    },
    headline: function (a, r) {
        var h = ovHead(r);
        if (!h) {
            var body = (typeof r === 'string') ? r : '';
            if (body.indexOf('打断') >= 0) {
                return '定时唤醒 · 已被对话注入打断';
            }
            return '定时唤醒' + ovStat(r);
        }
        var d = ovDurText(h.meta);
        return '定时唤醒 · ' + ((d.length > 0) ? d : '待唤醒');
    }
});
