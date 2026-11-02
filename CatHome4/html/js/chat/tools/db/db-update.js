// ═══════════════════════════════════════════
// chat/tools/db/db-update.js —— db-update / 条目库按标题覆盖正文（DbCat）
// 声明：专属图标 ✏️ + 输入意图行（库名 / 标题 / 新正文规模）+ 折叠行与输出自然语言化；段结构归骨架 `text`。
// 回执形态（DbService.ToToolText）：结构化头 {ok,tool,target[,badLines]} + 摘要行 + 载荷 {id,v}
// ═══════════════════════════════════════════

toolDecl('db-update', {
    icon: '✏️',
    inputLines: function (a) {
        return ['更新库 ' + ovText(a.lib), '标题 ' + ovPeek(a.title), '新正文 ' + ovSize(a.body)];
    },
    headline: function (a, r) {
        var head = '更新 ' + ovText(a.lib) + ' · ' + ovPeek(a.title);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var o = ovPayload(r);
        if (!o) {
            return head;
        }
        return head + ' · #' + ovMetaNum(o, 'id', 0) + ' v' + ovMetaNum(o, 'v', 0);
    },
    badge: function (r) {
        var o = ovPayload(r);
        if (!o) {
            return '';
        }
        return ' · v' + ovMetaNum(o, 'v', 0);
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
            + ' 已更新至 v' + ovMetaNum(o, 'v', 0)];
        var bad = ovMetaNum(h.meta, 'badLines', 0);
        if (bad > 0) {
            out.push('⚠️ 库文件有 ' + bad + ' 行坏行已跳过');
        }
        return out;
    }
});
