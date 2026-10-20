// ═══════════════════════════════════════════
// blocks/toolcard.js —— toolcard 块（persist 类 · 完成态工具卡）
//
// 契约：
//   item = { type:'toolcard', ts, msgIndex, round,
//            payload:{ name, arguments, result, images? } }
//   · name = 工具名；arguments = 参数 JSON 原文；result = 结果原文（可带结构化头）
//   · 计数类字段（toolIndex / toolTotal / order / durMs）——字段面见契约 §12.5
//   · 在途卡（live 面 `toolrun`，`result` 未定稿）无 durMs——由前端按首次出现时刻自增显示已用时（A263）；
//     定稿后由后端 `durMs` 接管，同一位置、不同数据源，互不重叠
//
// 产出：.chat-row.assistant.tool > details.chat-tool[.err]
//         └ summary.tn（图标 + 变体标签 + 折叠行文案） + 段数组（lib/tool-format 骨架）
//
// 归属：
//   · 命令解码显示 → fx/cmd-intent（独立功能面——本件只调用，不自持解码与显示构造）
//   · 展开态点击收起 → lib/press.js（bindPressToggle）
//   · DOM 挂载 / 滚动跟随归主干
// ═══════════════════════════════════════════

function buildToolBlock(payload, item, type) {
    // 工具卡——行容器 + 折叠卡（默认折叠）
    // 签名 = persist 族通用形态（payload, item）；item 本件不用（只作签名一致——避免第二参语义重载）
    // type = 行身份（**第三参**，缺省 'toolcard'）——live 面以 'toolrun' 复用本渲染件（两区同源，行身份随来源）
    // 气泡外壳（2026-10-05 气泡化）——persist 面得 .chat-bubble；live 面（toolrun）仍 .chat-plain（面板自身即泡，内部件不再套壳）
    var t = type || 'toolcard';
    var row = blockRow(t);
    // 失败态标记——行级 `.err`：气泡描边 + 滚动带刻度同转红（刻度色取气泡描边色，一处标记两处生效）
    if (isErrResult(payload ? payload.result : undefined)) {
        row.classList.add('err');
    }
    // 工具名标记——`data-tool`：timeback 域区间（start..back）链着色用（其余仅作标识）
    if (payload && typeof payload.name === 'string' && payload.name.length > 0) {
        row.setAttribute('data-tool', payload.name);
    }
    var bubble = el('div', bodyClass(t));
    bubble.appendChild(buildToolCard(payload, false));
    row.appendChild(bubble);
    return row;
}

function resultInfo(resultText) {
    // 结果边界信息——仅截断 / 超时（规模计数归后端；此处只作警示判据）
    var info = { truncated: false, timeout: false };
    var text = resultText || '';
    if (text.length === 0) { return info; }
    var trimmed = text.replace(/^\s+|\s+$/g, '');
    if (trimmed.charAt(0) !== '{') { return info; }
    try {
        var o = JSON.parse(trimmed);
        if (o && typeof o.stdout === 'string') {
            info.truncated = (o.truncated === true);
            info.timeout = (o.timeout === true);
        }
    } catch (e) {
        // 非 JSON——无边界信息
    }
    return info;
}

// ── 流式工具计时（A263）─────────────────────────────────────────
// 运行中工具卡（live 面 `toolrun`，结果未定稿）显示**已用时**，0.1 秒分度自增——
//   与流式 think 的统计行同一观感口径（`fx/live-stats.js` 的 `time:` 行）。
// 计时锚按 `名字|toolIndex|toolTotal` 记忆首次出现时刻：live 帧每帧重建面板，锚必须跨帧稳定，
//   否则读数每帧归零。定稿后该工具从 live 面消失，锚随之失效（离开 toolrun 面整体清零）。

/// 心跳节拍（毫秒）——与 `fx/live-stats.js` 同为 10 次/秒
var TOOL_DUR_TICK_MS = 100;

/// 在途计时心跳句柄（无在途卡即自停——不常驻计时器）
var toolDurTimer = null;

/// 计时锚表——键 → 首次出现时刻（ms）
var toolRunAnchors = {};

/// 锚键——同一轮内同名工具的多次调用靠 toolIndex / toolTotal 区分
function toolRunAnchorKey(t) {
    var name = (t && typeof t.name === 'string') ? t.name : '?';
    var idx = (t && t.toolIndex !== undefined && t.toolIndex !== null) ? String(t.toolIndex) : '';
    var total = (t && t.toolTotal !== undefined && t.toolTotal !== null) ? String(t.toolTotal) : '';
    return name + '|' + idx + '|' + total;
}

/// 取锚（无则新建）——超限整体重置，防跨轮次残留累积
function toolRunSince(t) {
    var key = toolRunAnchorKey(t);
    var now = Date.now();
    var since = toolRunAnchors[key];
    if (typeof since !== 'number' || !isFinite(since)) {
        if (Object.keys(toolRunAnchors).length > 200) {
            toolRunAnchors = {};
        }
        toolRunAnchors[key] = now;
        return now;
    }
    return since;
}

/// 锚清零——live 面板离开 toolrun 面时调用：下一轮同名同序工具重新起算
function toolRunForgetAll() {
    toolRunAnchors = {};
}

/// 在途用时文案——0.1 秒分度（与统计行 `time:` 行同口径）
function toolDurLiveText(sinceMs) {
    return ' · ' + ((Date.now() - sinceMs) / 1000).toFixed(1) + 's';
}

/// 心跳——单一 100ms 驱动所有 `.chat-dur-live`；无在途卡即自停
function toolDurTick() {
    var nodes = document.querySelectorAll('.chat-dur-live');
    if (nodes.length === 0) {
        if (toolDurTimer !== null) {
            clearInterval(toolDurTimer);
            toolDurTimer = null;
        }
        return;
    }
    for (var i = 0; i < nodes.length; i++) {
        var since = parseInt(nodes[i].getAttribute('data-since'), 10);
        if (!isFinite(since)) {
            continue;
        }
        nodes[i].textContent = toolDurLiveText(since);
    }
}

/// 心跳保障——首个在途卡出现时启动
function toolDurEnsureTick() {
    if (toolDurTimer === null && typeof setInterval === 'function') {
        toolDurTimer = setInterval(toolDurTick, TOOL_DUR_TICK_MS);
    }
}

function buildToolCard(tool, open) {
    // 工具卡——details 结构（open=true 展开：进行中卡直接展示；缺省折叠——点击 summary 展开/收起）
    // 失败态——ERR 前缀 或 结构化返回头 ok:false（lib/tool-format isErrResult 单一出口）
    var t = tool || {};
    var isErr = isErrResult(t.result);
    var det = el('details', 'chat-tool' + (isErr ? ' err' : ''));
    det.open = (open === true);
    var sum = el('summary', 'tn');

    // [段1] 折叠行前缀——失败态警示 / 并发批次序（`[图标 n/m]`）/ 工具图标（专属 → 骨架 → ❓）
    sum.appendChild(document.createTextNode(toolPrefix(t, isErr)));

    // [段2] 执行序徽标——宿主 order 表裁决（载荷缺字段 = 旧块 → 不渲染，零猜测）
    if (t.order !== undefined && t.order !== null && String(t.order).length > 0) {
        sum.appendChild(elText('span', 'chat-order', '⚙' + t.order));
        sum.appendChild(document.createTextNode(' '));
    }

    // [段3] 原始工具名——载荷 name 直显（紧跟执行序徽标；一眼可辨工具身份）
    if (typeof t.name === 'string' && t.name.length > 0) {
        sum.appendChild(elText('span', 'chat-toolname', t.name));
        sum.appendChild(document.createTextNode(' '));
    }

    // [段4] 骨架分派（lib/tool-format）——命中 → 段结构 + 工具变体标签；未命中 → 探测骨架
    var body = toolBody(t);
    if (body && body.tag) {
        sum.appendChild(elText('span', 'ps-tag ' + (body.tagCls || ''), body.tag));
        sum.appendChild(document.createTextNode(' '));
    }

    // [段5] 折叠行文案——PS 命令意图（fx/cmd-intent 独立功能件）→ 声明层 headline → 骨架中文名兜底
    //        无 headline 时追加结果规模后缀（同一语义不两处实现）
    var cmdIntent = (typeof fxCmdIntentDecode === 'function') ? fxCmdIntentDecode(t.name, t.arguments) : null;
    var headline = '';
    var hasHeadline = false;
    if (cmdIntent) {
        headline = cmdIntent.brief;
    } else {
        headline = (typeof toolHeadline === 'function') ? toolHeadline(t) : '';
        if (headline.length > 0) {
            hasHeadline = true;
        } else {
            headline = (typeof toolFallbackHeadline === 'function') ? toolFallbackHeadline(t) : (t.name || '?');
        }
    }
    sum.appendChild(document.createTextNode(headline + (hasHeadline ? '' : toolResultSuffix(resultInfo(t.result)))));

    // [段6] 工具用时——后端 durMs 真值（>0 显示；未记录 / 不适用不写）
    var dur = (typeof t.durMs === 'number') ? t.durMs : -1;
    if (dur > 0 && typeof fmtMs === 'function') {
        sum.appendChild(elText('span', 'chat-dur', ' · ' + fmtMs(dur)));
    }
    // [段6b] 在途用时（A263）——结果未定稿即流式中：按首次出现时刻自增；定稿后归段6 的 durMs
    if (t.result === undefined || t.result === null) {
        var since = toolRunSince(t);
        var liveDur = elText('span', 'chat-dur chat-dur-live', toolDurLiveText(since));
        liveDur.setAttribute('data-since', String(since));
        sum.appendChild(liveDur);
        toolDurEnsureTick();
    }
    det.appendChild(sum);
    if (cmdIntent && typeof fxCmdIntentAttach === 'function') {
        // 展开区首块——逐段意图对照（命令原文仍在输入段；未识别段标 ❓）
        fxCmdIntentAttach(det, cmdIntent);
    }

    var info = resultInfo(t.result);
    if (body && body.segs) {
        // 骨架路径——输入 / 输出段（二级折叠；段内沿用 .ta / .tr / .ta.warn 锚点类）
        for (var si = 0; si < body.segs.length; si++) {
            det.appendChild(body.segs[si]);
        }
    } else {
        // 回落路径——参数与结果原文（零回归）
        if (t.arguments) {
            det.appendChild(elText('div', 'ta', t.arguments));
        }
        if (info.truncated) {
            det.appendChild(elText('div', 'ta warn',
                '⚠️ 输出已达上限被截断——后续内容未回传' + (info.timeout ? '；进程超时已终止' : '')));
        }
        if (t.result) {
            det.appendChild(elText('div', 'tr' + (isErr ? ' err' : ''), t.result));
        }
    }

    // 展开态整块点击收起（折叠头仍走原生 toggle）
    bindPressToggle(det, function (d) { d.open = false; }, { onlyWhenOpen: true });
    return det;
}
