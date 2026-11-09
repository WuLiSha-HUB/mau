// ═══════════════════════════════════════════
// chat/tools/group/group-catcfg-get.js —— group-catcfg-get / 读每猫配置（GroupCat · A322）
// 声明：专属图标 ⚙️ + 输入意图行（猫 @ 组）+ 折叠行数字段 + 输出逐字段行。
// 回执形态（A214）：首行元数据头 {ok,tool,target} + 摘要行（cat.cfg | id）+ 逐字段行（「  key=value」13 项）。
// ═══════════════════════════════════════════

toolDecl('group-catcfg-get', {
    icon: '⚙️',
    inputLines: function (a) {
        return ['读取配置 ' + ovText(a.cat) + ' @ ' + ovText(a.group)];
    },
    headline: function (a, r) {
        var head = '配置 ' + ovText(a.cat);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var lines = ovBodyLines(r);
        var n = 0;
        for (var i = 1; i < lines.length; i = i + 1) {
            if (lines[i].replace(/\s/g, '').length > 0) {
                n = n + 1;
            }
        }
        return head + ' · ' + n + ' 字段';
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var lines = ovBodyLines(r);
        var out = [];
        for (var i = 1; i < lines.length; i = i + 1) {
            if (lines[i].replace(/\s/g, '').length > 0) {
                out.push(lines[i]);
            }
        }
        if (out.length === 0) {
            out.push('（无字段）');
        }
        return out;
    }
});
