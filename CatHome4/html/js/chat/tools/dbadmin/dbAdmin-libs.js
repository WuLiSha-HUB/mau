// ═══════════════════════════════════════════
// chat/tools/dbadmin/dbAdmin-libs.js —— 库级明细（DbAdmin）
// 声明：专属图标 🗄️ + 输入意图行（授权过滤说明）+ 折叠行读库数
//       + 输出逐库一行（库名 / 条目 / 墓碑 / 坏行 / 字节 / 创建时刻）；段结构归骨架 `listing`。
// 回执形态（DbService.ToToolText）：结构化头 {ok,tool} + 摘要行「库 N（授权内 X / 共 Y……）」+ 载荷 {libs[]}
// ═══════════════════════════════════════════

/// 字节数 → 人读形态（B / KB / MB，一位小数）
function dbAdminBytes(n) {
    if (typeof n !== 'number' || n <= 0) {
        return '0 B';
    }
    if (n < 1024) {
        return n + ' B';
    }
    if (n < 1024 * 1024) {
        return (n / 1024).toFixed(1) + ' KB';
    }
    return (n / (1024 * 1024)).toFixed(1) + ' MB';
}

toolDecl('dbAdmin-libs', {
    icon: '🗄️',
    inputLines: function () {
        return ['列举库明细（按猫授权过滤）'];
    },
    headline: function (a, r) {
        var head = '库明细';
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var o = ovPayload(r);
        if (!o || !Array.isArray(o.libs)) {
            return head;
        }
        return head + ' · ' + o.libs.length + ' 个库';
    },
    badge: function (r) {
        var o = ovPayload(r);
        if (!o || !Array.isArray(o.libs)) {
            return '';
        }
        return ' · ' + o.libs.length + ' 库';
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
        if (!o || !Array.isArray(o.libs)) {
            return [ovBodyFirst(r)];
        }
        var out = [];
        for (var i = 0; i < o.libs.length; i = i + 1) {
            var lib = o.libs[i];
            var line = ovMetaStr(lib, 'id')
                + ' · 条目 ' + ovMetaNum(lib, 'entries', 0)
                + ' · 墓碑 ' + ovMetaNum(lib, 'tombstones', 0)
                + ' · 坏行 ' + ovMetaNum(lib, 'badLines', 0)
                + ' · ' + dbAdminBytes(lib.bytes);
            var created = ovMetaStr(lib, 'createdAt');
            if (created.length > 0) {
                line = line + ' · 创建 ' + created;
            }
            if (lib.broken === true) {
                line = line + ' · ⚠️ 损坏';
            }
            out.push(line);
        }
        if (out.length === 0) {
            out.push('（授权内无库）');
        }
        return out;
    }
});
