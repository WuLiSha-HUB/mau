// ═══════════════════════════════════════════
// chat/tools/config/config-list.js —— config-list / 配置项清单（ConfigCat）
// 声明：折叠行读结构化头（items / sensitive）+ 输入意图行；段结构归骨架 `listing`。
// ═══════════════════════════════════════════

toolDecl('config-list', {
    inputLines: function () {
        return ['列出全部已落盘配置项（纯 KV）'];
    },
    headline: function (a, r) {
        var h = ovHead(r);
        if (!h) {
            return '配置列表' + ovStat(r);
        }
        var m = h.meta;
        return '配置列表 · ' + ovMetaNum(m, 'items', 0) + ' 项 · 敏感 ' + ovMetaNum(m, 'sensitive', 0);
    }
});
