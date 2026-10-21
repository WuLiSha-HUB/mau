// ═══════════════════════════════════════════
// chat/tools/dbadmin/dbAdmin-create.js —— 新建库（DbAdmin · 库级管理）
// 声明：专属图标 🧱 + 输入意图行（库名）+ 折叠行与输出自然语言化；段结构归骨架 `text`。
// 回执形态（DbService.ToToolText）：结构化头 {ok,tool,target} + 摘要行「库已建立: X」+ 载荷 {id,createdAt}
// ═══════════════════════════════════════════

toolDecl('dbAdmin-create', {
    icon: '🧱',
    inputLines: function (a) {
        return ['新建库 ' + ovText(a.lib)];
    },
    headline: function (a, r) {
        var head = '新建库 ' + ovText(a.lib);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var o = ovPayload(r);
        var id = o ? ovMetaStr(o, 'id') : '';
        return (id.length > 0) ? (head + ' · 已建立') : head;
    },
    badge: function (r) {
        var o = ovPayload(r);
        var id = o ? ovMetaStr(o, 'id') : '';
        return (id.length > 0) ? ' · 已建立' : '';
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
        var id = o ? ovMetaStr(o, 'id') : '';
        if (id.length === 0) {
            return [ovBodyFirst(r)];
        }
        var out = ['库 ' + id + ' 已建立'];
        var created = ovMetaStr(o, 'createdAt');
        if (created.length > 0) {
            out.push('创建时刻 · ' + created);
        }
        return out;
    }
});
