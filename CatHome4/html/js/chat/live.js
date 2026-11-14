// ═══════════════════════════════════════════
// chat/live.js —— 临时区（契约 §12.5）
//
// 定位：临时区的唯一应用入口。**状态投影**——后端只给两个字符串（type + context），
//   输入面恒为「该 type 的当前整段」（全集）——协议面不做合并、不下发增量。
//   **呈现面两路**：
//     · 挂载件（`stream.js::STREAM_TYPES` 白名单——当前 `thinksse` / `replysse`）：交给
//       **流式呈现机制**（前缀增量 + 打字机 + 面板跟随 + 段期光标——整套效果的单点承载处）；
//     · 其余件（`toolrun` / `empty` / 非白名单流）：**整体替换**（状态投影原语义——清区重绘）。
//
// 物理位置：输入区内、与输入框**同区域互斥**（同一列同位——显隐由 input.js 切内联 display）；
//           不参与对话流、不进会话存档。
// 切换开关：本件每帧把当前 type 标到输入区容器上（`#chatinput` 的 `data-live`）——开关流式页签色
//           与临时区气泡描边同源（chat.css `--ch-live-color`），有内容时描边呼吸高亮。
//
// 语义（A196 · 契约 §12.5）：type ∈ {thinksse, replysse, toolrun, empty}
//   thinksse / replysse——context = 该流当前全文（文本，直接投给对应渲染件）
//   toolrun——context = 未完成工具卡数组 JSON（逐卡复用持久区工具卡渲染件）
//   empty——链路正常、内容为空：不产元素（面板无内容即零高，不占位）
// ═══════════════════════════════════════════

/// 临时区容器——输入区切换面板
function liveContainer() {
    return document.getElementById('chatLivePanel');
}

/// 流式态标记——把当前 live type 写到输入区容器上（`#chatinput` 的 `data-live`）：
/// 色映射归 chat.css 单一色源 `--ch-live-color`——切换开关流式页签 / 滑块与临时区气泡描边共用，本件不碰色值
function liveMarkType(type) {
    var box = document.getElementById('chatinput');
    if (box) {
        box.setAttribute('data-live', type);
    }
}

// ── 增量累积层（A362）───────────────────────
// 后端 live 段有两种下发：整段（无 mode 字段——首次 / type 切换 / 内容改写）与增量
// （mode=delta + base + 新增尾巴）。本层把两种拼回「该 type 的当前全文」，下游呈现链零改动。
// 基线不符（帧被丢弃 / 累积错位）不静默：出声并清累积，后续整段帧自动自愈。
var liveAccumType = '';
var liveAccumText = '';

/// 传输统计（A362）——前端侧分片与失配计数（观测面：异常不静默，验收可直读）
var liveTransferStats = { fullFrames: 0, deltaFrames: 0, baseMiss: 0, typeMiss: 0 };

/// 统计对象直读：window.liveTransferStats（不设转发函数——零引用声明即死代码，由前端符号自检拦下）

/// 临时区正文解析——整段即重置基线；增量按 base 校验后追加。
/// 🔴 失配不丢已收内容（丢历史 = 屏幕上只剩最后一片）：保留历史 + 可见缺口标记 + 统计出声。
function liveResolveContext(type, seg) {
    if (seg === null || seg === undefined) {
        liveAccumType = type;
        liveAccumText = '';
        return '';
    }
    var ctx = (seg.context === undefined || seg.context === null) ? '' : String(seg.context);
    if (seg.mode !== 'delta') {
        liveTransferStats.fullFrames = liveTransferStats.fullFrames + 1;
        liveAccumType = type;
        liveAccumText = ctx;
        return ctx;
    }
    liveTransferStats.deltaFrames = liveTransferStats.deltaFrames + 1;
    if (type !== liveAccumType) {
        liveTransferStats.typeMiss = liveTransferStats.typeMiss + 1;
        if (liveTransferStats.typeMiss === 1) {
            warn('临时区增量基线错位（type 变更未带整段）——按该尾巴重建');
        }
        liveAccumType = type;
        liveAccumText = ctx;
        return ctx;
    }
    var base = (typeof seg.base === 'number') ? seg.base : -1;
    if (base !== liveAccumText.length) {
        liveTransferStats.baseMiss = liveTransferStats.baseMiss + 1;
        if (liveTransferStats.baseMiss === 1) {
            warn('临时区增量基线不匹配（本地 ' + liveAccumText.length + ' / 期望 ' + base + '）——保留已收内容续接，缺口已标记');
        }
        liveAccumText = liveAccumText + '\u26a0[缺口]' + ctx;
        return liveAccumText;
    }
    liveAccumText = liveAccumText + ctx;
    return liveAccumText;
}

/// 临时段入口——挂载件走流式呈现机制（stream.js），其余走整体替换；**功能隔离**：单段异常不中断其他
function liveApply(seg) {
    var type = seg ? (seg.type || 'empty') : 'empty';
    // 🔴 累积层先于面板检查推进——面板缺失期间也连着基线：否则恢复后每帧都判失配，历史被丢弃只剩尾巴
    var ctx = liveResolveContext(type, seg);
    var box = liveContainer();
    if (!box) {
        return;
    }
    liveMarkType(type);
    // 流式工具计时锚清零（A263）——离开 toolrun 面（空 / 其它类型）即让下一轮同名同序工具重新起算
    if (type !== 'toolrun' && typeof toolRunForgetAll === 'function') {
        toolRunForgetAll();
    }
    if (streamWanted(type)) {
        try {
            streamApply(box, type, ctx);
        } catch (e) {
            streamReset();
            box.textContent = '';
            box.appendChild(buildRenderErrorBlock({ type: type, payload: { text: ctx } }, e));
        }
        return;
    }
    streamReset();
    box.textContent = '';
    if (type === 'empty') {
        return;
    }
    try {
        liveRender(type, ctx, box);
    } catch (e) {
        box.appendChild(buildRenderErrorBlock({ type: type, payload: { text: ctx } }, e));
    }
}

/// 渲染分发——按 type 把 context 投给对应结构（toolrun 为数组：逐卡复用持久区工具卡渲染件）
function liveRender(type, ctx, box) {
    if (type === 'toolrun') {
        // 后端保证合法 JSON 数组（空数组 = 无在途工具，阵列零卡）；解析异常由 liveApply 兜底
        var cards = JSON.parse(ctx.length > 0 ? ctx : '[]');
        for (var i = 0; i < cards.length; i++) {
            box.appendChild(buildToolBlock(cards[i], null, 'toolrun'));
        }
        return;
    }
    var fn = LIVE_RENDERERS[type];
    if (typeof fn !== 'function') {
        box.appendChild(buildUnknownBlock({ type: type, payload: { text: ctx } }));
        return;
    }
    box.appendChild(fn({ text: ctx }));
}

/// 断线收尾（A206）——SSE 断开时把临时区收干净：停打字机 + 清面板 + 摘流式态标记。
/// 动机：断线后帧送不达，live 区半句残影 + 进行中光标会一直卡到重连（判例 2026-10-06 重启现场）；
///   持久区不受影响（重连后全量首帧重建），故此处只动临时区。
function chatLiveAbort() {
    streamReset();
    var box = liveContainer();
    if (box) {
        box.textContent = '';
    }
    liveMarkType('empty');
}
