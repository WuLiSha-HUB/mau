// ═══════════════════════════════════════════
// chat/tools/builtin/info.js —— info 工具（专属骨架 info）
//
// 定位：本会话环境自省的**专属骨架渲染**——返回体 = 分类 JSON 块（非「头 + 正文」两段结构，整块解析）。
// 口径（莎定 2026-09-18）：info 以 LLM 可读为第一目的（后端分类 JSON 块）；前端把各大类**摊平成键值行**，
//       值内多字段用「 · 」连接，长值由 CSS 折行（不截断）。
// 缺类不显示该行（后端「无则省略键」约定——前端不补空行、不写 NaN）。
// 来源：重构前 `chat-tools.js::chatSkelInfo / chatInfoPairs`（A200 恢复）。
// ═══════════════════════════════════════════

/// info 骨架——分类 JSON 块 → 键值行（解析不出 → 原文兜底，失败可见不静默）
function skelInfo(tool) {
    var segs = [segInput(tool, '')];
    var d = tryJson(tool.result);
    if (!d) {
        segs.push(segOutput(tool, '输出', function (body, text, isErr) {
            segBlock(body, isErr ? 'tr err' : 'tr', text);
        }, tool.result));
        return { tag: '', tagCls: '', segs: segs };
    }
    var pairs = ovInfoPairs(d);
    segs.push(segOutput(tool, '输出 · ' + pairs.length + ' 项', function (body, text, isErr) {
        segKv(body, pairs);
    }, ovInfoPeek(pairs)));
    return { tag: '', tagCls: '', segs: segs };
}

/// 摊平键值行——固定顺序：猫 / 版本 / 当前时间 / LLM / 本地端点 / 可见根 / 根路径 / 前文 / 加载包
function ovInfoPairs(d) {
    var pairs = [];
    var cat = field(d, 'cat');
    if (cat.length > 0) {
        pairs.push({ k: '猫', v: cat });
    }
    var v = d.version;
    if (v && typeof v === 'object') {
        var ver = field(v, 'version');
        var build = field(v, 'build');
        pairs.push({ k: '版本', v: ver + (build.length > 0 ? ' · 编译 ' + build : '') });
    }
    var t = d.time;
    if (t && typeof t === 'object') {
        pairs.push({ k: '当前时间', v: field(t, 'now') });
    }
    var l = d.llm;
    if (l && typeof l === 'object') {
        var lp = [];
        var proto = field(l, 'protocol');
        var host = field(l, 'host');
        var model = field(l, 'model');
        var src = field(l, 'source');
        if (proto.length > 0) {
            lp.push(proto);
        }
        if (host.length > 0) {
            lp.push(host);
        }
        if (model.length > 0) {
            lp.push('model=' + model);
        }
        if (src.length > 0) {
            lp.push(src);
        }
        pairs.push({ k: 'LLM', v: lp.join(' · ') });
    }
    var ep = d.endpoint;
    if (ep && typeof ep === 'object') {
        var eps = [];
        var chat = field(ep, 'chat');
        var panel = field(ep, 'panel');
        if (chat.length > 0) {
            eps.push('对话 ' + chat);
        }
        if (panel.length > 0) {
            eps.push('管理面板 ' + panel);
        }
        pairs.push({ k: '本地端点', v: (eps.length > 0 ? eps.join(' · ') : '（未监听）') });
    }
    if (d.roots && d.roots.length > 0) {
        var roots = [];
        var rootPaths = [];
        for (var i = 0; i < d.roots.length; i = i + 1) {
            var r = d.roots[i];
            var one = field(r, 'id') + '(' + (r.writable === true ? 'rw' : 'ro') + ')';
            var note = field(r, 'note');
            if (note.length > 0) {
                one = one + '[' + note + ']';
            }
            roots.push(one);
            var rp = field(r, 'path');
            if (rp.length > 0) {
                rootPaths.push(field(r, 'id') + ' → ' + rp);
            }
        }
        pairs.push({ k: '可见根', v: roots.join(' · ') });
        if (rootPaths.length > 0) {
            pairs.push({ k: '根路径', v: rootPaths.join(' · ') });
        }
    }
    if (d.tokens && typeof d.tokens === 'object') {
        var ctx = fieldNum(d.tokens, 'context');
        var tv = [];
        if (ctx.length > 0) {
            tv.push(ctx + ' tokens');
        }
        // A391——最近前文变动时刻（后端给 Unix 毫秒，展示层格式化）
        var ctxClock = ovClock(d.tokens.lastContextChangeAt);
        if (ctxClock.length > 0) {
            tv.push('变动 ' + ctxClock);
        }
        if (tv.length > 0) {
            pairs.push({ k: '前文', v: tv.join(' · ') });
        }
    }
    if (d.packs && d.packs.length > 0) {
        var packs = [];
        for (var k = 0; k < d.packs.length; k = k + 1) {
            var pk = field(d.packs[k], 'key');
            var desc = field(d.packs[k], 'desc');
            packs.push(desc.length > 0 ? pk + '(' + desc + ')' : pk);
        }
        pairs.push({ k: '加载包', v: packs.join(' · ') });
    }
    // [A391] 段补全——此前前端只渲染 8 段，以下为后端已推送但漏渲染的段（os / db / tools_drift / tools_defect / timeback）
    var o = d.os;
    if (o && typeof o === 'object') {
        var osParts = [];
        var osDesc = field(o, 'description');
        var osArch = field(o, 'arch');
        var osNet = field(o, 'dotnet');
        if (osDesc.length > 0) {
            osParts.push(osDesc);
        }
        if (osArch.length > 0) {
            osParts.push(osArch);
        }
        if (osNet.length > 0) {
            osParts.push('.NET ' + osNet);
        }
        if (osParts.length > 0) {
            pairs.push({ k: '系统', v: osParts.join(' · ') });
        }
    }
    var db = d.db;
    if (db && typeof db === 'object') {
        var dbParts = [];
        if (db.ok === true) {
            var dbSum = field(db, 'summary');
            if (dbSum.length > 0) {
                dbParts.push(dbSum);
            }
            if (db.libs && db.libs.length > 0) {
                var dbLibText = ovLibNames(db.libs);
                if (dbLibText.length > 0) {
                    dbParts.push(dbLibText);
                }
            }
        } else {
            var dbReason = field(db, 'reason');
            dbParts.push('不可用：' + (dbReason.length > 0 ? dbReason : '未说明原因'));
        }
        if (db.rootVisible !== true) {
            dbParts.push('根未暴露');
        }
        pairs.push({ k: '数据库', v: dbParts.join(' · ') });
    }
    var drift = d.tools_drift;
    if (drift && typeof drift === 'object') {
        var driftParts = [];
        var addedList = (drift.added && drift.added.length > 0) ? drift.added : [];
        var removedList = (drift.removed && drift.removed.length > 0) ? drift.removed : [];
        if (addedList.length > 0) {
            var addedNames = [];
            for (var ai = 0; ai < addedList.length; ai = ai + 1) {
                addedNames.push(ovToolName(addedList[ai]));
            }
            driftParts.push('新增 ' + addedList.length + '（' + addedNames.join(' · ') + '）');
        }
        if (removedList.length > 0) {
            driftParts.push('移除 ' + removedList.length + '（' + removedList.join(' · ') + '）');
        }
        pairs.push({ k: '工具漂移', v: driftParts.join(' · ') });
    }
    if (d.tools_defect && d.tools_defect.length > 0) {
        var defectItems = [];
        for (var di = 0; di < d.tools_defect.length; di = di + 1) {
            var one = d.tools_defect[di];
            defectItems.push(field(one, 'group') + '·' + field(one, 'stage') + '：' + field(one, 'reason'));
        }
        pairs.push({ k: '工具缺陷', v: d.tools_defect.length + ' 处 · ' + defectItems.join(' · ') });
    }
    var tb = d.timeback;
    if (tb && typeof tb === 'object') {
        var tbParts = [];
        var tbActive = tb.active;
        if (tbActive && typeof tbActive === 'object') {
            var tbId = field(tbActive, 'id');
            var tbPurpose = field(tbActive, 'purpose');
            var tbSeconds = fieldNum(tbActive, 'seconds');
            var tbOne = '活跃' + (tbId.length > 0 ? ' #' + tbId : '');
            if (tbPurpose.length > 0) {
                tbOne = tbOne + ' ' + tbPurpose;
            }
            if (tbSeconds.length > 0) {
                tbOne = tbOne + '（' + tbSeconds + 's）';
            }
            tbParts.push(tbOne);
        }
        if (tb.recent && tb.recent.length > 0) {
            tbParts.push('最近 ' + tb.recent.length + ' 条');
        }
        if (tb.archive && typeof tb.archive === 'object') {
            tbParts.push('归档 ' + (tb.archive.ok === true ? '可用' : '写失败'));
        }
        if (tbParts.length > 0) {
            pairs.push({ k: 'timeback', v: tbParts.join(' · ') });
        }
    }
    // [A391] 未注册段兜底——后端新增段不得在前端静默消失：JSON 原文直显（判据 = 后端加段，前端自动可见）
    var knownKeys = {
        ok: 1, tool: 1, cat: 1, version: 1, time: 1, os: 1, llm: 1, endpoint: 1,
        roots: 1, tokens: 1, packs: 1, db: 1, tools_drift: 1, tools_defect: 1, timeback: 1
    };
    var allKeys = Object.keys(d);
    for (var ki = 0; ki < allKeys.length; ki = ki + 1) {
        var rawKey = allKeys[ki];
        if (knownKeys[rawKey] === 1) {
            continue;
        }
        var rawText;
        try {
            rawText = JSON.stringify(d[rawKey]);
        } catch (e) {
            rawText = String(d[rawKey]);
        }
        pairs.push({ k: '未注册 · ' + rawKey, v: rawText });
    }
    return pairs;
}

/// 库清单短文本——条目取 name / lib 字段；取不到字段名时回落该条 JSON 原文（不静默吞）
function ovLibNames(libs) {
    var names = [];
    for (var i = 0; i < libs.length; i = i + 1) {
        var one = libs[i];
        var nm = field(one, 'name');
        if (nm.length === 0) {
            nm = field(one, 'lib');
        }
        if (nm.length === 0) {
            nm = field(one, 'id');
        }
        names.push(nm.length > 0 ? nm : JSON.stringify(one));
    }
    return names.join(' · ');
}

/// 漂移条目短文本——取 name / tool 字段；取不到回落该条 JSON 原文
function ovToolName(one) {
    var nm = field(one, 'name');
    if (nm.length === 0) {
        nm = field(one, 'tool');
    }
    return nm.length > 0 ? nm : JSON.stringify(one);
}

/// 键值行 → 折叠摘要文本（段折叠摘要在行数 >5 时展示「前 2 + … + 后 2」）
function ovInfoPeek(pairs) {
    var lines = [];
    for (var i = 0; i < pairs.length; i = i + 1) {
        lines.push(pairs[i].k + ': ' + pairs[i].v);
    }
    return lines.join('\n');
}

/// 取数值字段——非数值返回空串（键值行不写 NaN / 空值行）
function fieldNum(obj, key) {
    if (!obj || typeof obj[key] !== 'number') {
        return '';
    }
    return String(obj[key]);
}

// ── 声明（覆盖层：折叠行文案；段结构归专属骨架）──────────────

toolDecl('info', {
    inputLines: function () {
        return ['查看本会话运行环境'];
    },
    headline: function (a, r) {
        // 返回体 = 分类 JSON 块（无「头 + 正文」两段）——整块解析，不用 metaHead
        var d = tryJson(r);
        if (!d) {
            return '环境信息' + ovStat(r);
        }
        var ver = '';
        if (d.version && typeof d.version === 'object') {
            ver = field(d.version, 'version');
        }
        var cat = field(d, 'cat');
        var t = '环境信息 · v' + ver;
        if (cat.length > 0) {
            t = t + ' · 猫 ' + cat;
        }
        return t;
    }
});
