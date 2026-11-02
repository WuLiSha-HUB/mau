// ═══════════════════════════════════════════
// chat/tools/helpers.js —— 逐工具声明层公共取用小件
//
// 定位：各工具件共用的「取参数 / 量结果 / 读结构化头」小件——把固定语句拼装从 62 个件里抽出来。
// 边界：不做渲染（不产 DOM）、不做分派（骨架分派在 lib/tool-format.js）、不持状态。
// 规模格式化（k/M）走 `lib/format.js::fmtCount`——两处实现即漂移源，本件不重写。
// 来源：重构前 `js/chat-tools.js` 的 `chatOv*` 系列（A200 恢复，改名去 `chat` 前缀统一为 `ov*`）。
// ═══════════════════════════════════════════

// ── 参数取值 ──────────────────────────────

/// 缺值显式化——未传参不静默拼空串
function ovText(v) {
    if (v === undefined || v === null || v === '') {
        return '(未指定)';
    }
    return String(v);
}

/// 单行预览——多行压平 + 60 字符截断（进折叠行 / 意图行用）
function ovPeek(v) {
    if (typeof v !== 'string') {
        return ovText(v);
    }
    var t = v.replace(/\r/g, '').replace(/\n/g, ' ');
    return (t.length > 60) ? (t.substring(0, 60) + '…') : t;
}

/// 规模——字符数；数组 = 条数 + 总字符数（多值参数一律原生数组）
function ovSize(v) {
    if (Array.isArray(v)) {
        var n = 0;
        for (var i = 0; i < v.length; i = i + 1) {
            if (typeof v[i] === 'string') {
                n = n + v[i].length;
            }
        }
        return v.length + ' 条 · ' + n + ' 字符';
    }
    return ((typeof v === 'string') ? v.length : 0) + ' 字符';
}

/// 行区间描述——「L1~20」/「L5 起」
function ovRange(a) {
    return 'L' + ovText(a.start) + (a.end === undefined ? ' 起' : '~' + a.end);
}

/// 区间锚点描述——「 · 锚点 A ~ B」（两端皆缺则省略）
function ovAnchor(a) {
    if (!a.str1 && !a.str2) {
        return '';
    }
    return ' · 锚点 ' + (a.str1 || '（文件头）') + ' ~ ' + (a.str2 || '（文件尾）');
}

/// 替换模式描述——「 · 模式 exact」（未传省略）
function ovMode(a) {
    return a.mode ? ' · 模式 ' + a.mode : '';
}

/// 时长对象 → 「1 时 30 分」（{hours, minutes, seconds}；全零 / 缺 → 空串）
function ovDurText(o) {
    if (!o || typeof o !== 'object') {
        return '';
    }
    var parts = [];
    var h = Number(o.hours) || 0;
    var mi = Number(o.minutes) || 0;
    var s = Number(o.seconds) || 0;
    if (h > 0) {
        parts.push(h + ' 时');
    }
    if (mi > 0) {
        parts.push(mi + ' 分');
    }
    if (s > 0) {
        parts.push(s + ' 秒');
    }
    return parts.join(' ');
}

/// 时刻文本——Unix 毫秒 → 本地「HH:mm:ss」（缺值 / 非法 → 空串）
function ovClock(ms) {
    if (typeof ms !== 'number' || ms <= 0) {
        return '';
    }
    var d = new Date(ms);
    var hh = ('0' + d.getHours()).slice(-2);
    var mm = ('0' + d.getMinutes()).slice(-2);
    var ss = ('0' + d.getSeconds()).slice(-2);
    return hh + ':' + mm + ':' + ss;
}

/// 路径短名——取末段（折叠行可读性；全路径仍在输入段）
function ovShort(p) {
    var t = ovText(p);
    var i = Math.max(t.lastIndexOf('/'), t.lastIndexOf('\\'));
    return (i >= 0 && i + 1 < t.length) ? t.substring(i + 1) : t;
}

// ── 结果度量 ──────────────────────────────

/// 结果行数——空结果 0
function ovLines(r) {
    if (typeof r !== 'string' || r.length === 0) {
        return 0;
    }
    return r.split('\n').length;
}

/// 结果规模后缀——「 · N 行 M 字符」（未完成不标；空结果显式「 · 无输出」）
function ovStat(r) {
    if (r === undefined) {
        return '';
    }
    if (typeof r !== 'string' || r.length === 0) {
        return ' · 无输出';
    }
    return ' · ' + ovLines(r) + ' 行 ' + fmtCount(r.length) + ' 字符';
}

/// 结果条目数——空行与提示行（[git] / [skip] / [截断]）不计
function ovItems(r) {
    if (typeof r !== 'string' || r.length === 0) {
        return 0;
    }
    var lines = r.split('\n');
    var n = 0;
    for (var i = 0; i < lines.length; i = i + 1) {
        var t = lines[i];
        if (t.replace(/\s/g, '').length === 0) {
            continue;
        }
        if (t.indexOf('[git]') === 0 || t.indexOf('[skip]') === 0 || t.indexOf('[截断]') === 0) {
            continue;
        }
        n = n + 1;
    }
    return n;
}

/// 截断总量——结果含「[截断] 共 N 条」时返回 N，否则 0
function ovTotal(r) {
    if (typeof r !== 'string' || r.length === 0) {
        return 0;
    }
    var m = /\[截断\] 共 (\d+) 条/.exec(r);
    return m ? parseInt(m[1], 10) : 0;
}

/// 截断总量后缀——「（共 N 条）」；无截断返回空串
function ovTotalTail(r) {
    var t = ovTotal(r);
    return t > 0 ? '（共 ' + t + ' 条）' : '';
}

/// 结果中提取首个「N <单位>」计数（提取不到返回空串）
function ovCount(r, unit) {
    if (typeof r !== 'string' || r.length === 0) {
        return '';
    }
    var m = new RegExp('(\\d+)\\s*' + unit).exec(r);
    return m ? m[1] : '';
}

/// 替换结果尾巴——「 · N 处」/「 · 失败」（无可标则不标）
function ovReplaceTail(r) {
    var cnt = ovCount(r, '处');
    if (cnt.length > 0) {
        return ' · ' + cnt + ' 处';
    }
    if (typeof r === 'string' && r.indexOf('ERR') === 0) {
        return ' · 失败';
    }
    return '';
}

/// 成功回执尾段——`OK|<尾段>` → 尾段；非 OK / 无尾段 → 空串
function ovOkTail(r) {
    if (typeof r !== 'string' || r.indexOf('OK') !== 0) {
        return '';
    }
    var i = r.indexOf('|');
    return (i >= 0) ? r.substring(i + 1) : '';
}

/// 多行文本字段值——取以 key 开头的行，返回其后值（无匹配返回空串）
function ovLineField(r, key) {
    if (typeof r !== 'string' || r.length === 0) {
        return '';
    }
    var lines = r.split('\n');
    for (var i = 0; i < lines.length; i = i + 1) {
        var t = lines[i];
        if (t.indexOf(key) === 0) {
            return t.substring(key.length).replace(/^\s+/, '').replace(/\s+$/, '');
        }
    }
    return '';
}

/// 纯 JSON 结果解析——首字符非 `{` / 解析失败 → null（无结构化头的 JSON 回执走这里）
function ovJson(text) {
    if (typeof text !== 'string' || text.length === 0 || text.charAt(0) !== '{') {
        return null;
    }
    try {
        return JSON.parse(text);
    } catch (e) {
        return null;
    }
}

// ── 结构化头读取（防御式——字段缺失不写 NaN / undefined）──────

/// 头内数值——字段缺失 / 类型不符 → 兜底值
function ovMetaNum(meta, key, dflt) {
    if (!meta || typeof meta[key] !== 'number') {
        return dflt;
    }
    return meta[key];
}

/// 头内字符串——字段缺失 / 类型不符 → 空串
function ovMetaStr(meta, key) {
    if (!meta || typeof meta[key] !== 'string') {
        return '';
    }
    return meta[key];
}

/// 结构化头首次取值——无头返回 null（各件据此决定回落路径）
function ovHead(resultText) {
    if (typeof metaHead !== 'function') {
        return null;
    }
    return metaHead(resultText);
}

/// 结构化回执正文行——剥头后正文按行拆（无头 → 原文行；空 → 空数组）
function ovBodyLines(r) {
    if (typeof r !== 'string' || r.length === 0) {
        return [];
    }
    var h = ovHead(r);
    return (h ? h.body : r).split('\n');
}

/// 结构化回执摘要行——正文首行（空结果 → 空串；摘要与载荷分段回执用）
function ovBodyFirst(r) {
    var lines = ovBodyLines(r);
    return (lines.length > 0) ? lines[0] : '';
}

/// 结构化回执载荷——正文中首个可解析 JSON 对象（无 → null；非 JSON 行跳过不抛）
function ovPayload(r) {
    var lines = ovBodyLines(r);
    for (var i = 0; i < lines.length; i++) {
        var t = lines[i].replace(/^\s+|\s+$/g, '');
        if (t.charAt(0) !== '{') {
            continue;
        }
        try {
            var o = JSON.parse(t);
            return (o && typeof o === 'object') ? o : null;
        } catch (e) {
            return null;
        }
    }
    return null;
}
