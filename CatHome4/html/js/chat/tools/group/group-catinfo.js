// ═══════════════════════════════════════════
// chat/tools/group/group-catinfo.js —— group-catinfo / 单猫运行态（GroupCat · A322）
// 声明：专属图标 🐾 + 输入意图行（猫 @ 组）+ 折叠行读结构化头的运行态 + 输出摘要行与条目键值。
// 回执形态（A214）：首行元数据头 {ok,tool,cat,running,port} + 摘要行 + 单猫条目 JSON 行
//                   （条目与 cat.info 的 cats[] 元素同构——字段名不写死，按实际键摊平）。
// ═══════════════════════════════════════════

toolDecl('group-catinfo', {
    icon: '🐾',
    inputLines: function (a) {
        return ['查看运行态 ' + ovText(a.cat) + ' @ ' + ovText(a.group)];
    },
    headline: function (a, r) {
        var head = '运行态 ' + ovText(a.cat);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var h = ovHead(r);
        if (!h) {
            return head + ovStat(r);
        }
        var state = '静默';
        if (h.meta && h.meta.running === true) {
            var port = ovMetaNum(h.meta, 'port', 0);
            state = (port > 0) ? ('运行中 :' + port) : '运行中';
        }
        return head + ' · ' + state;
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var lines = ovBodyLines(r);
        var out = [];
        if (lines.length > 0 && lines[0].replace(/\s/g, '').length > 0) {
            out.push(lines[0]);
        }
        var entry = ovPayload(r);
        if (entry) {
            var keys = Object.keys(entry);
            for (var i = 0; i < keys.length; i++) {
                var v = entry[keys[i]];
                if (v === null || typeof v === 'object') {
                    continue;
                }
                out.push('  ' + keys[i] + ' = ' + String(v));
            }
        }
        if (out.length === 0) {
            out.push('（无数据）');
        }
        return out;
    }
});
