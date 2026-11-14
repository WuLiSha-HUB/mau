// ═══════════════════════════════════════════
// chat/tools/config/config-set.js —— config-set / 写配置项（ConfigCat）
// 声明：折叠行读结构化头（target）+ 输入意图行（key = value 预览）；value 空 = 清除该项落盘值（回到默认）。
// ═══════════════════════════════════════════

toolDecl('config-set', {
    inputLines: function (a) {
        if (!a.value) {
            return ['清除配置 ' + ovText(a.key) + '（回到默认）'];
        }
        return ['设置配置 ' + ovText(a.key) + ' = ' + ovPeek(a.value)];
    },
    headline: function (a, r) {
        var h = ovHead(r);
        if (!h) {
            return '设置配置 ' + ovText(a.key) + ovStat(r);
        }
        if (String(h.meta.cleared) === 'true') {
            return '设置配置 ' + ovMetaStr(h.meta, 'target') + ' · 已清除（回到默认）';
        }
        return '设置配置 ' + ovMetaStr(h.meta, 'target') + ' · 已更新';
    }
});
