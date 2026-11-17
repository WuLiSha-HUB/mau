// ═══════════════════════════════════════════
// chat/fx/controls.js —— 动作按钮态（独立功能件 A176）
//
// 定位：按 state 段的 runState **派生动作按钮可用性**（停止 / 继续 / 开始 Note / 发送）。
// 输入：state 段的 runState（只读原始信息）
// 输出：#chatPause · #chatContinue · #noteStartBtn · #chatSendBtn · #chatSendInput 的 disabled 态
// 判据：外观层允许推算（设计 §1.1）——按钮显隐 / 禁用属合法推算；不产生业务效果、不含指令输入
// 说明：契约 §12.2 ① 曾把「按钮可用性」列为 state 段字段——A184 改写为外观层派生（本件即该派生处）
// 登记：fx/registry.js 的 FX_FEATURES（契约 §12「独立功能面」节）
// ═══════════════════════════════════════════

/// 通用禁用设置——元素缺失即跳过（防御式）
function setDisabled(id, disabled) {
    var node = document.getElementById(id);
    if (node) {
        node.disabled = (disabled === true);
    }
}

/// 轮进行中判据——runState 非空且非 idle（后端权威态，前端不猜）
function fxControlsRunning(state) {
    var s = (state && state.runState) ? state.runState : '';
    if (s === '' || s === 'idle') {
        return false;
    }
    return true;
}

/// 停止已投递标志——点了停止且指令已送达：按钮保持禁用直到本轮结束（A393）
/// 语义：runState 仍是进行态（本轮要自然跑完），但用户已没有可停的东西——按钮不该回弹成可用
var stopPending = false;

/// 标记停止已投递——input.js 投递成功后调用（点击即回执：按钮当场禁用）
function fxControlsMarkStopPending() {
    stopPending = true;
    setDisabled('chatPause', true);
}

/// 应用——按轮进行态派生按钮可用性（发送恒可用；忙时插话走插话队列语义）
function fxControlsApply(state) {
    var running = fxControlsRunning(state);
    if (!running) {
        // 轮已结束——本地「已点停止」态复位（下一轮按钮重新可用）
        stopPending = false;
    }
    setDisabled('chatPause', !running || stopPending);
    setDisabled('chatContinue', running);
    setDisabled('noteStartBtn', running);
    setDisabled('chatSendBtn', false);
    setDisabled('chatSendInput', false);
}
