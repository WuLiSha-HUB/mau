// ═══════════════════════════════════════════
// blocks/notice.js —— notice 块（persist 类 · 中性操作提示）
//
// 契约：
//   item = { type:'notice', ts, msgIndex:-1, round, payload:{ text } }
//   text = 提示正文（缺省「提示」）
//
// 产出：.chat-row.assistant > .chat-bubble.notice（形态见 registry.js §形态声明）
//
// 取舍（A393）：与 error / retry 分型——本件只承载中性操作反馈（如「已终止续推」），
//   不带失败语义、不触发任何状态迁移；写入即落盘，刷新可回看。
// ═══════════════════════════════════════════

function buildNoticeBlock(payload) {
    // 提示气泡——恒定新建独立块（不与错误 / 重试气泡合并）
    var p = payload || {};
    var row = blockRow('notice');
    var bubble = el('div', bodyClass('notice'));
    bubble.textContent = p.text || '提示';
    row.appendChild(bubble);
    return row;
}
