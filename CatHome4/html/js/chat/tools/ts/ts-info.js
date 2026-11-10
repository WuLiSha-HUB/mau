// ═══════════════════════════════════════════
// chat/tools/ts/ts-info.js —— ts-info / TypeScript 工程口径回执（TsCat · A332 工具 / A335 前端件）
// 声明：输入意图行（工程路径）+ 折叠行读口径（strict / 编译面规模）+ 输出逐字段摊平；
//       段结构归骨架 `json`（结构查看——本件把口径摊成人类可读行）。
// 回执形态（stdio_server ts.info）：单段 JSON {ok,tsconfig,extendsChain[],strict,strictExplicit,
//       strictFamily{},unset[],target,module,moduleResolution,lib[],types[],skipLibCheck,
//       noImplicitAny,strictNullChecks,esModuleInterop,baseUrl,paths{},include[],exclude[],
//       files,nodeModulesInScope}
// ═══════════════════════════════════════════

/// 数组 → 「a / b / c」；空 / 非数组 → 空态文本
function tsInfoList(v, emptyText) {
    if (!Array.isArray(v) || v.length === 0) {
        return emptyText;
    }
    return v.join(' / ');
}

/// 布尔 → 是 / 否
function tsInfoBool(v) {
    return (v === true) ? '是' : '否';
}

/// 严格族一行——开启项列出，关闭项另标
function tsInfoStrictFamily(fam) {
    if (!fam || typeof fam !== 'object') {
        return '严格族  （服务未回执）';
    }
    var keys = Object.keys(fam);
    if (keys.length === 0) {
        return '严格族  （无）';
    }
    var on = [];
    var off = [];
    var i;
    for (i = 0; i < keys.length; i = i + 1) {
        if (fam[keys[i]] === true) {
            on.push(keys[i]);
        } else {
            off.push(keys[i]);
        }
    }
    var line = '严格族  ' + ((on.length > 0) ? on.join(' / ') : '（全关）');
    if (off.length > 0) {
        line = line + ' ｜ 关：' + off.join(' / ');
    }
    return line;
}

toolDecl('ts-info', {
    inputLines: function (a) {
        return ['TS 工程口径 ' + ovText(a.path)];
    },
    headline: function (a, r) {
        var head = '口径 ' + ovShort(a.path);
        if (r === undefined) {
            return head + ' · 处理中';
        }
        var o = ovJson(r);
        if (!o) {
            return head + ovStat(r);
        }
        if (o.ok === false) {
            return head + ' · 服务失败';
        }
        var strictText = (o.strict === true) ? 'strict' : '非 strict';
        return head + ' · ' + strictText + ' · ' + ovMetaNum(o, 'files', 0) + ' 文件';
    },
    badge: function (r) {
        var o = ovJson(r);
        if (!o || o.ok === false) {
            return '';
        }
        return ' · ' + ((o.strict === true) ? 'strict' : '非 strict');
    },
    outputLines: function (r, a, isErr) {
        if (isErr || r === undefined) {
            return null;
        }
        var o = ovJson(r);
        if (!o) {
            return null;
        }
        if (o.ok === false) {
            return ['ERR|' + ovMetaStr(o, 'error') + '|' + ovMetaStr(o, 'message')];
        }
        var out = [];
        out.push('tsconfig  ' + ovMetaStr(o, 'tsconfig'));
        var chain = Array.isArray(o.extendsChain) ? o.extendsChain : [];
        if (chain.length > 1) {
            out.push('extends   ' + chain.join(' → '));
        }
        var strictText = (o.strict === true) ? 'true' : 'false';
        strictText = strictText + ((o.strictExplicit === true) ? '（显式）' : '（继承）');
        out.push('strict    ' + strictText);
        out.push(tsInfoStrictFamily(o.strictFamily));
        out.push('编译面    target ' + ovText(ovMetaStr(o, 'target')) + ' · module ' + ovText(ovMetaStr(o, 'module')) + ' · resolution ' + ovText(ovMetaStr(o, 'moduleResolution')));
        out.push('库与类型  lib ' + tsInfoList(o.lib, '(未设置)') + ' ｜ types ' + tsInfoList(o.types, '(未设置)'));
        out.push('范围      include ' + tsInfoList(o.include, '(未设置)') + ' ｜ exclude ' + tsInfoList(o.exclude, '(未设置)'));
        var base = ovMetaStr(o, 'baseUrl');
        var paths = (o.paths && typeof o.paths === 'object') ? o.paths : {};
        var pathKeys = Object.keys(paths);
        out.push('映射      baseUrl ' + ((base.length > 0) ? base : '(未设置)') + ' · paths ' + ((pathKeys.length > 0) ? pathKeys.join(' / ') : '(未设置)'));
        out.push('规模      ' + ovMetaNum(o, 'files', 0) + ' 文件 · node_modules 入面 ' + ovMetaNum(o, 'nodeModulesInScope', 0) + ' · skipLibCheck ' + tsInfoBool(o.skipLibCheck) + ' · esModuleInterop ' + tsInfoBool(o.esModuleInterop));
        return out;
    }
});
