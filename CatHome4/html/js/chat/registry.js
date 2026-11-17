// ═══════════════════════════════════════════
// chat/registry.js —— 块声明表 + 渲染函数注册表（契约 design-ch4-protocol §12.5）
//
// 定位：块 type 的**唯一声明处**（形态 / 行语义 / 刻度角色）+ 渲染函数**唯一映射处**。
//      前端唯一扩展点——新增块型 = 后端加一个 type + 本表加一行 + 一个渲染函数，其余不管。
//
// 契约：
//   persist item = { type, ts, msgIndex, round, payload }
//   live    seg  = { type, context }（A196 状态投影：type ∈ toolrun / thinksse / replysse / empty；
//                   context = 该 type 当前整段内容——思考全文 / 回复全文 / 未完成工具卡数组 JSON / 空串）
//
// 两族渲染函数形态（见各素材头注）：
//   persist 族：function(payload, item) → 行元素（返回即完，挂载由 persist.js 承担；
//              第二参 = 整条条目——块级字段取用口，如 text 块操作条读 msgIndex）
//   live    族：function(payload) → 行元素（一件式入口自各 live 件提供——A178 归位：
//               `build*()` → 句柄的中间形态留在件内，本表只登记入口名，不兼实现）
// ═══════════════════════════════════════════

// ── 块声明表（单一真相源 · A179 收口）────────────────────────────
// 每 type 一行四列：
//   form 形态——`bubble`（对话内容体——persist 八类全量，2026-10-05 莎定改判）| `plain`（临时区三件——
//              面板自身即泡，内部件不再套壳；cursor / 流式光标等仍在 plain 面）
//   row  行语义类——`.chat-row` 上的变体类（左右分侧 / 布局）；渲染件不拼类名字符串
//   tick 刻度角色——滚动带刻度色（`user` / `reply` / `tool` / `think` / `sum`；CSS `.sb-tick.*`）
//   body 块体语义类（可选）——块体元素上的语义类（CSS 点名用，如 `.chat-plain.error`）；渲染件不拼类名字符串
// 🔴 本表是形态 / 行语义 / 刻度角色 / 块体语义的唯一声明处——渲染函数一律经 `formClass()`（形态）/
//    `bodyClass()`（块体 = 形态 + 语义）/ `blockRow()`（行容器）取用，**不自持类名字符串**
//    （改形态或语义 = 改本表一行，不动渲染函数）；滚动带经行元素的 `data-type` 反查本表
//    （不再探测类名——新增块型自动跟上）。
//    （A219：来源变体已退役——系统注入改独立块 systemautomessage，与 user 平级。）
var BLOCK_DECL = {
    'user': { form: 'bubble', row: 'user', tick: 'user' },
    'systemautomessage': { form: 'bubble', row: 'sysmsg', tick: 'user-sys' },
    'text': { form: 'bubble', row: 'assistant', tick: 'reply' },
    'reason': { form: 'bubble', row: 'assistant reason', tick: 'think' },
    'toolcard': { form: 'bubble', row: 'assistant tool', tick: 'tool' },
    'retry': { form: 'bubble', row: 'assistant', tick: 'reply', body: 'retry' },
    'error': { form: 'bubble', row: 'assistant', tick: 'reply', body: 'error' },
    'notice': { form: 'bubble', row: 'assistant', tick: 'reply', body: 'notice' },
    'inject_report': { form: 'bubble', row: 'assistant inject', tick: 'reply', body: 'inject' },
    'roundsum': { form: 'bubble', row: 'assistant roundsum', tick: 'sum', body: 'roundsum' },
    'roundsummary': { form: 'bubble', row: 'assistant summary', tick: 'sum', body: 'roundsummary' },
    'replysse': { form: 'plain', row: 'assistant', tick: 'reply', body: 'streaming' },
    'thinksse': { form: 'plain', row: 'assistant reason', tick: 'think' },
    'toolrun': { form: 'plain', row: 'assistant tool', tick: 'tool' }
};

/// 形态类名——表驱动唯一取用口；未声明的 type 回落朴素件（形态缺失不出气泡）
function formClass(type) {
    var d = BLOCK_DECL[type];
    return (d && d.form === 'bubble') ? 'chat-bubble' : 'chat-plain';
}

/// 块体类名——形态基类 + 语义类（`body` 缺省时等同形态基类）；块体构造的唯一取用口
function bodyClass(type) {
    var d = BLOCK_DECL[type];
    var base = formClass(type);
    return (d && d.body) ? (base + ' ' + d.body) : base;
}

/// 行容器——`.chat-row` + 行语义变体 + `data-type`（滚动带据此查刻度角色）
/// 🔴 行构造唯一入口：渲染件不拼类名字符串（未声明的 type 回落 assistant 行）
function blockRow(type) {
    var d = BLOCK_DECL[type];
    var row = el('div', 'chat-row ' + ((d && d.row) ? d.row : 'assistant'));
    row.setAttribute('data-type', (type || ''));
    return row;
}

/// 刻度角色——表驱动唯一取用口；未声明 / 无 type 的行回落 `reply`
function tickRole(type) {
    var d = BLOCK_DECL[type];
    return (d && d.tick) ? d.tick : 'reply';
}

// ── 来源变体已退役（A219）——系统注入改独立块 systemautomessage（行变体 / src 判据随之下线）

// ── 六态元信息（A179 收口——状态条与轮末统计共用一份）──────────────
// 契约：state 段 `runMs` 与 roundsum `payload.data.phases` 同键（link/wait/think/tool/run/reply）
// 消费：state.js 状态条（key + label + icon）· blocks/roundsum.js 六态用时行（key + label）
var RUN_PHASES = [
    { key: 'link', label: 'Link', icon: '🔗' },
    { key: 'wait', label: 'Wait', icon: '⏳' },
    { key: 'think', label: 'Think', icon: '🧠' },
    { key: 'tool', label: 'Tool', icon: '🔧' },
    { key: 'run', label: 'Run', icon: '⚙️' },
    { key: 'reply', label: 'Reply', icon: '💬' }
];

/// 持久区渲染表——type → function(payload) → 行元素
var PERSIST_RENDERERS = {
    'user': buildUserBlock,
    'systemautomessage': buildSystemAutoMessageBlock,
    'text': buildTextBlock,
    'reason': buildReasonBlock,
    'toolcard': buildToolBlock,
    'retry': buildRetryBlock,
    'error': buildErrorBlock,
    'notice': buildNoticeBlock,
    'inject_report': buildInjectReportBlock,
    'roundsum': buildRoundSumBlock,
    'roundsummary': buildRoundSummaryNoteBlock
};

/// 临时区渲染表——type → function(payload) → 行元素（A196：live 段为 {type, context} 两字符串）
/// `empty` 不产元素（live.js 显式处理）；`toolrun` 的 context 是**数组**，由 live.js 逐卡调
/// `buildToolBlock(card, null, 'toolrun')`（第三参 = 行身份；工具卡两区同源，不另设 live 件）——故不入本表
var LIVE_RENDERERS = {
    'replysse': liveReplySse,
    'thinksse': liveThinkSse
};
