// ═══════════════════════════════════════════
// chat/tools/config/config-reset.js —— config-reset / 还原默认值（ConfigCat）
// 声明：折叠行读结构化头（scope / target / items）+ 输入意图行（单键 / 全部两态）。
// ═══════════════════════════════════════════

toolDecl('config-reset', {
    inputLines: function (a) {
        if (a.key) {
            return ['清除配置 ' + a.key + ' 的落盘值（回到未设置）'];
        }
        return ['清除全部落盘配置值（回到未设置）'];
    },
    headline: function (a, r) {
        var h = ovHead(r);
        if (!h) {
            return '清除配置' + ovStat(r);
        }
        var m = h.meta;
        if (ovMetaStr(m, 'scope') === 'all') {
            return '清除全部落盘配置 · ' + ovMetaNum(m, 'items', 0) + ' 项';
        }
        return '清除配置 ' + ovMetaStr(m, 'target') + ' 的落盘值';
    }
});
