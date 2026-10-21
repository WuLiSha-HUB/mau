// ═══════════════════════════════════════════
// chat/tools/dbadmin/dbAdmin-drop.js —— 删库（DbAdmin · 破坏性但可恢复）
// 声明：专属图标 🗑️ + 输入意图行（库名 / 确认串）+ 折叠行与输出自然语言化；段结构归骨架 `text`。
// 回执形态（DbService.ToToolText）：结构化头 {ok,tool,target} + 摘要行「库已移入回收区: X」+ 载荷 {id,trash}
// ═══════════════════════════════════════════

toolDecl('dbAdmin-drop', {
    icon: '🗑️',
    inputLines: function (a) {
        return ['删除库 ' + ovText(a.lib), '确认串 ' + ovPeek(a.confirm)];
    },
    headline: function (a, r) {
        var head = '删除库 ' + ovText(a.lib);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var o = ovPayload(r);
        var trash = o ? ovMetaStr(o, 'trash') : '';
        return (trash.length > 0) ? (head + ' · 已移入回收区') : head;
    },
    badge: function (r) {
        var o = ovPayload(r);
        var trash = o ? ovMetaStr(o, 'trash') : '';
        return (trash.length > 0) ? ' · 已删除' : '';
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
        var trash = o ? ovMetaStr(o, 'trash') : '';
        if (trash.length === 0) {
            return [ovBodyFirst(r)];
        }
        return ['库 ' + ovMetaStr(o, 'id') + ' 已移入回收区（可恢复）', '回收路径 · ' + trash];
    }
});
