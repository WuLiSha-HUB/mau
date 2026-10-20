// ═══════════════════════════════════════════
// chat/tools/decl.js —— 逐工具声明层（注册口 + 落位表 + 图标 + 中文名）
//
// 定位：工具卡的「肉」——每个工具一件（`js/chat/tools/<组>/<工具名>.js`），件尾一行
//       `toolDecl('<工具名>', { … })` 自注册；本件持注册口（toolDecl）与取值口（toolDeclOf）。
// 三张表：
//   TOOL_DECL      工具名 → 声明对象（各件自注册——运行时填充）
//   TOOL_SKELETONS 工具名 → 骨架 id（**数据表**：增减工具只动此表 + 对应件，渲染面不动）
//   SKEL_ICONS / SKEL_LABELS  骨架图标 / 中文名（折叠行与图标兜底）
// 边界：骨架渲染（壳）在 `lib/tool-format.js`；折叠行组装在 `blocks/toolcard.js`；公共取用小件在 `tools/helpers.js`。
// 纪律：声明只写「偏离骨架的部分」——未声明项一律吃骨架默认；本层零 DOM、零状态、零网络。
// 来源：重构前 `js/chat-tools.js::CHAT_TOOL_OVERRIDES`（1812 行旧件，A167 随前端重构移除）
//       —— A200 按新框架恢复为拆件形态（2026-10-06）。
// ═══════════════════════════════════════════

// ── 声明契约（九项，全部可选）─────────────────────────────
//   icon        折叠行专属图标（默认：骨架图标 → ❓）
//   tag/tagCls  折叠行工具变体标签（如 PowerShell 的 PS 5.1 / PS 7——tagCls 走 ps5 / ps7 配色）
//   inputLines  function(args) → 行数组 | null（输入段自然语言意图；缺省 = 通用键值表）
//   outputLines function(result, args, isErr) → 行数组 | null（null = 回落骨架渲染）
//   badge       function(result, args) → 折叠头徽标后缀（缺省 = 骨架计数徽标）
//   headline    function(args, result) → 折叠行文案（缺省 = 骨架中文名 · 工具名）
//   inputImages function(args) → 路径数组（输入图片预览段；空数组 = 不显示）
//   outputImages function(result, args) → 路径数组（输出图片预览段——如 browser-shot 截图；空数组 = 不显示）
//   skeleton    （可选）件内声明骨架——仅在 TOOL_SKELETONS 未登记时作补充来源

var TOOL_DECL = {};

/// 注册口的唯一实现——各工具件在件尾调用（同名单次注册，重复注册以最后一次为准）
function toolDecl(name, decl) {
    if (typeof name !== 'string' || name.length === 0) {
        return;
    }
    if (!decl || typeof decl !== 'object') {
        return;
    }
    TOOL_DECL[name] = decl;
}

/// 取值口——工具名 → 声明对象（未注册 / 非法 → null）
function toolDeclOf(name) {
    var d = TOOL_DECL[name];
    return (d && typeof d === 'object') ? d : null;
}

// ── 骨架落位表（工具名 → 骨架 id）──────────────────────────
// 未登记的工具走形态探测回落（结果可解析 { → json 骨架，否则 text 骨架）——禁止空白、禁止静默。
// 口径：本表是数据不是逻辑；78 件与运行态工具池同步（基准 = 宿主 `ToolOrderTable` 全量登记表）。
var TOOL_SKELETONS = {
    // exec——进程 / 命令执行
    'powershell': 'exec',
    'powershell7': 'exec',
    'mau-setup': 'exec',
    'host-reload': 'exec',
    'restart-full': 'exec',
    'restart-incr': 'exec',
    'restart-host': 'exec',
    'majordomo-cmd': 'exec',
    'temp-exec': 'exec',
    // diagnostics——诊断列表
    'cs-check': 'diagnostics',
    'cs-build': 'diagnostics',
    'cs-comment_check': 'diagnostics',
    'cs-dead': 'diagnostics',
    'cs-format': 'diagnostics',
    'cs-test': 'diagnostics',
    'mau-verify': 'diagnostics',
    'mau-gen': 'diagnostics',
    'mau-proj': 'diagnostics',
    // listing——列举
    'file-tree': 'listing',
    'file-find': 'listing',
    'file-version': 'listing',
    'cs-list': 'listing',
    'config-list': 'listing',
    // matches——检索命中
    'text-grep': 'matches',
    'cs-find_ref': 'matches',
    'cs-find': 'matches',
    // lines——带行号文本
    'text-read_lines': 'lines',
    'cs-read': 'lines',
    // file——文件内容
    'text-read': 'file',
    'text-read_between': 'file',
    'browser-read': 'file',
    // json——通用结构
    'config-get': 'json',
    'host-flows': 'json',
    'temp-info': 'json',
    // 专属骨架——逐字段摊平（非通用形态）
    'info': 'info',
    'majordomo-catinfo': 'catinfo',
    // text——纯文本兜底
    'text-write': 'text',
    'text-append': 'text',
    'text-replace': 'text',
    'file-move': 'text',
    'file-delete': 'text',
    'file-copy': 'text',
    'cs-patch': 'text',
    'cs-member': 'text',
    'cs-comment': 'text',
    'config-set': 'text',
    'config-reset': 'text',
    'config-cat-get': 'text',
    'config-cat-set': 'text',
    'web-search': 'text',
    'web-ds-search': 'text',
    'image-analyze': 'text',
    'image-inject': 'text',
    'browser-open': 'text',
    'browser-eval': 'text',
    'browser-shot': 'text',
    'browser-tabs': 'text',
    'browser-headful': 'text',
    'Note': 'text',
    'time': 'text',
    'random': 'text',
    'sleep': 'text',
    'timer': 'text',
    'timeback-start': 'text',
    'timeback-back': 'text',
    'pack': 'text',
    // audio——音频（A239 / A246）
    'audio-play': 'text',
    'audio-speak': 'text',
    // py——执行器（PyCat）
    'py-run': 'json',
    'py-list': 'listing',
    'py-service': 'json',
    // web-fetch——资产下载（WebCat）
    'web-fetch': 'text',
    'web-fetch-jobs': 'json',
    // db——条目库（DbCat · A274）
    'db-put': 'text',
    'db-query': 'listing',
    'db-get': 'file',
    'db-update': 'text',
    'db-delete': 'text'
};

// ── 骨架图标 / 中文名（与骨架一一对应）──────────────────────
var SKEL_ICONS = {
    'exec': '💻',
    'diagnostics': '🩺',
    'listing': '📂',
    'matches': '🔍',
    'lines': '🔢',
    'file': '📖',
    'json': '🧩',
    'info': '🧭',
    'catinfo': '🐾',
    'text': '📝'
};

var SKEL_LABELS = {
    'exec': '命令执行',
    'diagnostics': '诊断',
    'listing': '列举',
    'matches': '检索',
    'lines': '按行读取',
    'file': '读取文件',
    'json': '结构查看',
    'info': '环境信息',
    'catinfo': '全猫状态',
    'text': '工具调用'
};

/// 工具 → 骨架 id（未登记返回空串）
function skeletonOf(name) {
    var skel = TOOL_SKELETONS[name];
    if (typeof skel === 'string') {
        return skel;
    }
    var d = toolDeclOf(name);
    if (d && typeof d.skeleton === 'string') {
        return d.skeleton;
    }
    return '';
}

/// 骨架图标——未登记（含探测回落）返回空串，调用方回落默认
function iconOf(name) {
    var icon = SKEL_ICONS[skeletonOf(name)];
    return (typeof icon === 'string') ? icon : '';
}

// ── 折叠行取值口（声明层 → 折叠行：图标 / 文案 / 批次前缀 / 规模后缀）────

/// 工具图标——三级回落：声明层专属 icon → 骨架图标 → ❓（未登记工具显式暴露，不给通用图标掩盖）
function toolIconOf(name) {
    var d = toolDeclOf(name);
    if (d && typeof d.icon === 'string' && d.icon.length > 0) {
        return d.icon;
    }
    var icon = iconOf(name);
    if (icon.length > 0) {
        return icon;
    }
    return '❓';
}

/// 折叠行文案——声明层 headline 优先；无 → 空串（调用方回落骨架兜底）
function toolHeadline(tool) {
    if (!tool || typeof tool.name !== 'string') {
        return '';
    }
    var d = toolDeclOf(tool.name);
    if (!d || typeof d.headline !== 'function') {
        return '';
    }
    var t = d.headline(toolArgs(tool), tool.result);
    return (typeof t === 'string') ? t : '';
}

/// 参数对象——arguments JSON → 对象（解析失败 / 非对象 → 空对象，不抛）
function toolArgs(tool) {
    if (!tool || typeof tool.arguments !== 'string') {
        return {};
    }
    var s = tool.arguments.replace(/^\s+|\s+$/g, '');
    if (s.length === 0 || s.charAt(0) !== '{') {
        return {};
    }
    try {
        var o = JSON.parse(s);
        return (o && typeof o === 'object') ? o : {};
    } catch (e) {
        return {};
    }
}

/// 骨架兜底折叠行——声明层未配 headline 时接管：骨架中文名 · 工具名（未登记骨架回落工具名本身）
function toolFallbackHeadline(tool) {
    if (!tool || typeof tool.name !== 'string' || tool.name.length === 0) {
        return '?';
    }
    var label = SKEL_LABELS[skeletonOf(tool.name)];
    if (typeof label !== 'string') {
        return tool.name;
    }
    return label + ' · ' + tool.name;
}

/// 折叠行前缀——失败态警示 / 并发批次序（`[图标 n/m]`）/ 工具图标
function toolPrefix(tool, isErr) {
    var icon = (isErr ? '⚠️' : toolIconOf(tool.name));
    var total = Number(tool.toolTotal) || 0;
    if (total > 1) {
        return '[' + icon + ' ' + (tool.toolIndex || '?') + '/' + total + '] ';
    }
    return icon + ' ';
}

/// 结果规模后缀——大结果（≥1000 字符）或截断 / 超时才追加（小结果不标，避免噪音）
function toolResultSuffix(info) {
    if (!info) {
        return '';
    }
    if (info.truncated) {
        return ' · ⚠️ 已达上限 ' + fmtCount(info.chars) + ' 字符' + (info.timeout ? '（超时终止）' : '');
    }
    if (info.chars >= 1000) {
        return ' · ' + fmtCount(info.chars) + ' 字符';
    }
    return '';
}
