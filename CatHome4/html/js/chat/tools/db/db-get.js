// ═══════════════════════════════════════════
// chat/tools/db/db-get.js —— db-get / 条目库读取单条全文（DbCat）
// 声明：专属图标 📖 + 输入意图行（库名 / 定位模式 + 键）+ 折叠行（标题 / 版本）
//       + 输出「标题 / 版本时间 / 正文规模」表头 + 正文；段结构归骨架 `file`。
// 回执形态（DbService.ToToolText）：结构化头 {ok,tool,target[,badLines]} + 摘要行 + 载荷 {id,title,body,v,createdAt,updatedAt}
// ═══════════════════════════════════════════

toolDecl('db-get', {
    icon: '📖',
    inputLines: function (a) {
        var mode = (a.mode === 'id') ? 'ID' : '标题';
        return ['读取库 ' + ovText(a.lib) + ' · ' + mode + ' ' + ovPeek(a.key)];
    },
    headline: function (a, r) {
        var keyText = ((a.mode === 'id') ? 'ID ' : '标题 ') + ovPeek(a.key);
        var head = '读取 ' + ovText(a.lib) + ' · ' + keyText;
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
        var o = ovPayload(r);
        if (!o) {
            return null;
        }
        var body = ovMetaStr(o, 'body');
        var bodyLines = (body.length === 0) ? [] : body.split('\n');
        var chars = 0;
        for (var i = 0; i < bodyLines.length; i = i + 1) {
            chars = chars + bodyLines[i].length;
        }
        var out = [];
        out.push('标题 ' + ovMetaStr(o, 'title'));
        out.push('#' + ovMetaNum(o, 'id', 0) + ' · v' + ovMetaNum(o, 'v', 0)
            + ' · 更新 ' + ovMetaStr(o, 'updatedAt') + ' · 创建 ' + ovMetaStr(o, 'createdAt'));
        out.push('');
        out.push('正文 · ' + bodyLines.length + ' 行 ' + chars + ' 字符');
        for (var j = 0; j < bodyLines.length; j = j + 1) {
            out.push(bodyLines[j]);
        }
        return out;
    }
});
