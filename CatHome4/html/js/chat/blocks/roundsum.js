// ═══════════════════════════════════════════
// blocks/roundsum.js —— roundsum 块（persist 类 · 轮末统计）
//
// 契约：
//   item = { type:'roundsum', ts, msgIndex, round,
//            payload:{ data:{ prompt, completion, cacheHit, miss?, toolCount, requests?, elapsedMs,
//                             phases:{ link, wait, think, tool, run, reply } } } }
//   口径三级（会话 / 轮 / 请求）——本块只承载「轮」级：本轮全部请求的累加
//   平均首 token 延迟 = phases.link 累计 ÷ requests（缺 link 不加后缀）
//
// 产出：.chat-row.assistant.roundsum > .chat-plain.roundsum > .rs-head + .rs-tok + .rs-tools + .rs-times（形态见 registry.js §形态声明）
//
// 来源：chat-view.js chatOnRoundSum（第 447-494 行）
//
// 取舍：三段结构与文案原样保留；丢弃气泡构造（chatBubble → el）与 DOM 挂载 / 滚动跟随
// ═══════════════════════════════════════════

// 六态用时标签（idle 不计时故不入载荷）——单点声明在 registry.js 的 RUN_PHASES（A179 收口）

function roundsumHtml(payload) {
    var d = (payload && payload.data) || {};
    var phases = d.phases || {};
    // miss 为后端派生值（§12.10 口径：miss = prompt − cacheHit）——前端零兜底，字段缺失即显 0
    var miss = d.miss || 0;

    var html = '<div class="rs-head">📊 Round</div>';

    // cache 命中率——cacheHit / prompt（prompt=0 时 0%；仍为前端自算——§12.5 已知待收口项）
    var promptTotal = d.prompt || 0;
    var hitRate = (promptTotal > 0) ? ((d.cacheHit || 0) / promptTotal * 100) : 0;
    // 轮级四项（2026-10-06 · 莎定）——与信息位同格式同机制：顺序按计费从高到低（命中率 → Hit → Miss → Down）
    // Hit = 净命中量（prompt − miss；与后端 cacheHit 等价——miss 即由其派生）；数值走 fmtNumHtml 分片（小数灰小）
    var hit = promptTotal - miss;
    if (hit < 0) {
        hit = 0;
    }
    // 弱化件（2026-10-06 · 莎定）——括号与百分号包 `.rs-dim`（第三行同款灰）；小数点由 `.num-frac` 承载同色
    // 2026-10-06 微调（莎定）——K/M 单位随整数原色（去 `.rs-dim` 包裹，不再弱化）；间隔统一：命中率 ↔ Hit 四空格 · Hit ↔ Miss ↔ Down 各三空格
    html += '<div class="rs-tok">'
        + '<span class="rs-dim">（🎯</span><span class="ci-rate">' + fmtNumHtml(hitRate.toFixed(2) + '%', 'rs-dim') + '</span><span class="rs-dim">）</span>'
        + '    <span class="ci-hit">Hit</span>' + fmtNumHtml(fmtCount(hit))
        + '   <span class="ci-miss">Miss</span>' + fmtNumHtml(fmtCount(miss))
        + '   <span class="ci-down">Down</span>' + fmtNumHtml(fmtCount(d.completion || 0))
        + '</div>';

    // 第二行——工具次数 · 请求次数 · All 总耗时（三段各自着色）
    var toolsLine = '';
    if (d.toolCount > 0) { toolsLine = '<span class="rs-tool">🔧 Tool ' + d.toolCount + '</span>'; }
    if (d.requests !== undefined) {
        var apiText = '🔄 Api ' + (d.requests || 0);
        var linkMs = phases.link || 0;
        if (d.requests > 0 && linkMs > 0) {
            apiText += '（' + (linkMs / 1000 / d.requests).toFixed(2) + ' s /use）';
        }
        toolsLine += (toolsLine ? ' · ' : '') + '<span class="rs-api">' + apiText + '</span>';
    }
    toolsLine += (toolsLine ? ' · ' : '') + '<span class="rs-all">⏱ All ' + fmtMs(d.elapsedMs) + '</span>';
    html += '<div class="rs-tools">' + toolsLine + '</div>';

    // 第三行——六态用时（非零态上尾巴；单色弱化）
    var tparts = [];
    for (var pi = 0; pi < RUN_PHASES.length; pi++) {
        var pv = phases[RUN_PHASES[pi].key] || 0;
        if (pv > 0) { tparts.push(RUN_PHASES[pi].label + ' ' + fmtMs(pv)); }
    }
    html += '<div class="rs-times">' + tparts.join(' · ') + '</div>';
    return html;
}

function buildRoundSumBlock(payload, item) {
    // 轮末统计气泡——弱化系统样式；历史重建与实时推送共用同一渲染面
    var row = blockRow('roundsum');
    var bubble = el('div', bodyClass('roundsum'));
    bubble.innerHTML = roundsumHtml(payload);
    appendRoundRollback(bubble, payload, item);
    row.appendChild(bubble);
    return row;
}

/// A387 轮末回滚——按钮挂在轮末统计块上：回滚 = 回到这一轮完全结束时的状态
/// 判据 = 块 ts（视图锚）+ 载荷 lastMsgTs（前文锚）双齐——旧块缺 lastMsgTs 即不挂（不做迁移）
/// 指令走指令总线（出口面），投递回执走通知面 `warn`（信息位归 state 段独占）
function appendRoundRollback(bubble, payload, item) {
    var ts = item ? item.ts : undefined;
    var data = (payload && payload.data) || {};
    if (typeof ts !== 'number' || ts <= 0) {
        return;
    }
    if (typeof data.lastMsgTs !== 'number' || data.lastMsgTs <= 0) {
        return;
    }
    var bar = el('div', 'node-actions');
    bar.appendChild(nodeActionBtn('⟲ 回滚到此处', 'node-btn-rollback',
        '回到这一轮结束时的状态（此后产生的全部内容将被截断，不可恢复）', function () {
            if (!window.confirm('回到这一轮结束时的状态？此后产生的内容将被截断（不可恢复）。')) {
                return;
            }
            postCommand('session.rollback ' + ts);
            warn('回滚指令已投递——建议刷新页面');
        }));
    bubble.appendChild(bar);
}
