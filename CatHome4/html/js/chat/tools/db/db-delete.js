// ═══════════════════════════════════════════
// chat/tools/db/db-delete.js —— db-delete / 条目库删除（墓碑保留）（DbCat）
// 声明：专属图标 🗑️ + 输入意图行（库名 / 标题）+ 折叠行与输出自然语言化；段结构归骨架 `text`。
// 回执形态（DbService.ToToolText）：结构化头 {ok,tool,target[,badLines]} + 摘要行 + 载荷 {id}
// ═══════════════════════════════════════════

toolDecl('db-delete', {
    icon: '🗑️',
    inputLines: function (a) {
        return ['删除库 ' + ovText(a.lib) + ' · 标题 ' + ovPeek(a.title)];
    },
    headline: function (a, r) {
        var head = '删除 ' + ovText(a.lib) + ' · ' + ovPeek(a.title);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var o = ovPayload(r);
        var id = o ? ovMetaNum(o, 'id', 0) : 0;
        return (id > 0) ? (head + ' · #' + id) : head;
    },
    badge: function (r) {
        var o = ovPayload(r);
        var id = o ? ovMetaNum(o, 'id', 0) : 0;
        return (id > 0) ? (' · #' + id) : '';
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var h = ovHead(r);
        if (!h) {
            return null;
        }
        var o = ovPayload(r);
        var id = o ? ovMetaNum(o, 'id', 0) : 0;
        if (id <= 0) {
            return [ovBodyFirst(r)];
        }
        var out = ['库 ' + ovMetaStr(h.meta, 'target') + ' · 条目 #' + id
            + ' 已删除（墓碑保留，可审计）'];
        var bad = ovMetaNum(h.meta, 'badLines', 0);
        if (bad > 0) {
            out.push('⚠️ 库文件有 ' + bad + ' 行坏行已跳过');
        }
        return out;
    }
});
