// ═══════════════════════════════════════════
// chat/persist.js —— 持久对话区（契约 §12.2 ② · §12.3）
//
// 定位：持久区的唯一应用入口。**无键、无配对、无排序**——按到达顺序挂载（推送序即渲染序）。
//
// 两种模式（帧内 persist.mode）：
//   full   —— 清区整体重绘（连接首帧 / 重连 / 新会话重置后重发）
//   append —— 逐条追加（新持久条目）
//
// 边界：块函数只产出元素（素材零全局状态）；挂载、滚动跟随归本层。
//       自动切换（user 块 → 流式区 / roundsum → 输入区）由本层在**追加路**触发——判据取持久区新块，不按指令。
//       本层不判流式结束、不判换手、不配对——契约 D「前端 = 纯渲染」。
//       兜底件（未识别 type / 渲染异常）→ `lib/fallback.js`（两区共用，A178 归位）
// ═══════════════════════════════════════════

// ── type 归一（前端视觉口径 · 2026-10-05）─────────────────────
// `gap_text`（工具轮间隙文本）与 `text`（正式回复）在前端**视觉同形**——同气泡形态 / 同行语义 /
// 同 MD 填充件；语义区分（QQ `/last` 只取正式回复 · 会话存档不进间隙文本）由**后端消费方**承担。
// 前端零业务效果 → 收包即归一，不设第二件、不设第二张分型表。
// 新增同形 type：本表登记一行即可。
var TYPE_ALIAS = {
    'gap_text': 'text'
};

/// 类型归一——收包后第一个动作（`persistAppend` 内逐条调用；full / append 两路同经此）
function normalizeType(t) {
    return TYPE_ALIAS[t] || t;
}

/// 持久区容器——对话区
function persistContainer() {
    return document.getElementById('chatMsgs');
}

/// 持久段入口——按帧内 mode 分派（full 先清后绘 / append 逐条追加）
function persistApply(seg) {
    if (!seg) {
        return;
    }
    var items = seg.items || [];
    if (seg.mode === 'full') {
        // 全量重绘 = 历史重放（连接首帧 / 重连 / 新会话）——不触发自动切换
        persistFull(items);
        return;
    }
    // 追加路 = 新块到达——按块型做自动切换（**人工** user 才切流式；系统注入零动作——见 persistAutoSwap）
    persistAppend(items, true);
}

/// 全量重绘——清空后按序重建，并强制滚底（内容整体换过，跟随复位）
function persistFull(items) {
    var box = persistContainer();
    if (!box) {
        return;
    }
    box.textContent = '';
    persistAppend(items);
    tbScopeMark();
    scrollBottomNow();
}

/// 追加——逐条渲染挂载；**功能隔离**：单块渲染异常不中断整批（异常块落可见错误气泡）
/// auto=true 时按新块类型做输入区 / 流式区自动切换（仅追加路传入；full 重放不触发）
function persistAppend(items, auto) {
    var box = persistContainer();
    if (!box || !items || items.length === 0) {
        return;
    }
    // 跟随判定前置——**追加前**采样视口位置（追加后采样会被新块自身高度顶出阈值；2026-10-06 判例）
    var follow = scrollWanted();
    for (var i = 0; i < items.length; i++) {
        // type 归一——收包后第一个动作：此后所有 type 消费（判据 / 分派 / 兜底）都在归一口径上
        if (items[i]) {
            items[i].type = normalizeType(items[i].type);
        }
        // 自动切换——两个节点各一次（判据取持久区新块，不按指令）：
        //   人工 user 块 = 我的输入已落前文 → 切流式看流；roundsum = 本轮结算 → 切回输入
        if (auto) {
            persistAutoSwap(items[i]);
        }
        var node = null;
        try {
            node = persistRender(items[i]);
        } catch (e) {
            node = buildRenderErrorBlock(items[i], e);
        }
        if (node) {
            box.appendChild(node);
        }
    }
    tbScopeMark();
    if (follow) {
        scrollFollow();
    }
}

/// 自动切换——按新块切输入区 / 流式区（追加路专用；同态零动作 = 幂等）
/// ① **人工** user 块 → 流式（我的输入已落前文，看流）② roundsum → 输入（本轮结算，放行输入）
/// 🔴 系统注入块（type=systemautomessage · A219）**零动作**——不抢视线、不打断输入：输入区与流式区同区互斥，
///    切走输入区等于把用户正在打的字挤下屏。系统注入照常落持久区与滚动带，只是不动临时区。
///    roundsum 仍切回输入：它是「轮已结算、放行输入」的信号，与谁触发本轮无关（且同态即幂等）。
function persistAutoSwap(item) {
    if (typeof chatLiveShow !== 'function') {
        return;
    }
    var type = item ? item.type : '';
    if (type === 'user') {
        chatLiveShow(true);
        return;
    }
    if (type === 'roundsum') {
        chatLiveShow(false);
    }
}

/// 单条渲染——type 直指渲染函数；未登记 type → 兜底报错气泡（lib/fallback.js，两区共用）
function persistRender(item) {
    if (!item) {
        return null;
    }
    var fn = PERSIST_RENDERERS[item.type];
    if (typeof fn !== 'function') {
        return buildUnknownBlock(item);
    }
    // 第二参 = 整条条目——块级字段取用口（如 text 块操作条读 msgIndex 定回滚 / 分支切点）
    return fn(item.payload || {}, item);
}

/// timeback 域区间标记——从 `timeback-start` 行到 `timeback-back` 行之间（**不含两端**）的块加 `.tb-scope`（气泡描边转粉）；
/// 两端工具卡保持原色；失败块（`.err`）由 CSS 事后覆盖为红（红优先，粉不盖红）。
/// 每次渲染后全量重标（重放 / 追加同路）——行数有限，O(n) 可接受。
function tbScopeMark() {
    var box = persistContainer();
    if (!box) {
        return;
    }
    var rows = box.children;
    if (!rows) {
        return;
    }
    var open = false;
    for (var i = 0; i < rows.length; i = i + 1) {
        var row = rows[i];
        if (!row || typeof row.getAttribute !== 'function') {
            continue;
        }
        var tool = row.getAttribute('data-tool') || '';
        if (tool === 'timeback-start') {
            // 仅成功算发起（A330）——失败回执（行级 `.err`）不开启区间，后续块不被染成域内
            open = row.classList.contains('err') === false;
            row.classList.remove('tb-scope');
            continue;
        }
        if (tool === 'timeback-back') {
            open = false;
            row.classList.remove('tb-scope');
            continue;
        }
        if (open) {
            row.classList.add('tb-scope');
        } else {
            row.classList.remove('tb-scope');
        }
    }
}
