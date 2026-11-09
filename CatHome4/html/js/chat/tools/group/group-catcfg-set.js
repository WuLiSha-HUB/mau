// ═══════════════════════════════════════════
// chat/tools/group/group-catcfg-set.js —— group-catcfg-set / 写每猫配置（GroupCat · A322）
// 声明：专属图标 ⚙️ + 输入意图行（猫 · 字段 = 值预览）+ 折叠行读结构化头。
// 回执形态（A214）：首行元数据头 {ok,tool,target,cat,field} + 摘要行 + 落盘实况（写后读回单行）。
// 受限名单提示随正文带回（｜⚠️ 已剔除受限工具名 …）——原文展示，不由前端改写。
// ═══════════════════════════════════════════

toolDecl('group-catcfg-set', {
    icon: '⚙️',
    inputLines: function (a) {
        return ['写入配置 ' + ovText(a.cat) + ' · ' + ovText(a.field) + ' = ' + ovPeek(a.value)];
    },
    headline: function (a, r) {
        var head = '配置 ' + ovText(a.cat) + ' · ' + ovText(a.field);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var h = ovHead(r);
        if (!h) {
            return head + ovStat(r);
        }
        return head + ' 已写入';
    }
});
