// ═══════════════════════════════════════════
// chat/fx/cmd-intent.js —— 命令解码显示（独立功能件 A176）
//
// 定位：把 powershell / powershell7 工具卡的 command 原文**推算为中文意图显示**——折叠行简报 + 展开区首块。
//       解码器在 `lib/cmd.js`（纯函数 · 表驱动确定性解析）；本件只做「取用 + 落 DOM」的显示面。
// 输入：toolcard payload 的 name / arguments（**只读原始信息**）
// 输出：工具卡折叠行文案 · `.cmd-intent` 展开块
// 判据（设计 §1.2 三准入）：① 不承载业务效果（解码错只错画面）② 不含指令输入 ③ 输入只读原始信息
// 登记：`fx/registry.js` 的 `FX_FEATURES`（契约 §12「独立功能面」节）
// ═══════════════════════════════════════════

/// 适用判据——命令类工具（PS 双线 + Linux 单线 sh-exec）
function fxCmdIntentApplies(name) {
    if (name === 'powershell') {
        return true;
    }
    if (name === 'powershell7') {
        return true;
    }
    if (name === 'sh-exec') {
        return true;
    }
    return false;
}

/// 解码——适用且可解析时返回 `{brief, detail, truncated}`；否则 null（调用方回落骨架 / 工具名）
function fxCmdIntentDecode(name, argsText) {
    if (fxCmdIntentApplies(name) !== true) {
        return null;
    }
    if (typeof cmdDecodeTool !== 'function') {
        return null;
    }
    return cmdDecodeTool(argsText);
}

/// 展开块挂载——解码结果非空时追加 `.cmd-intent` 首块（逐段意图对照；命令原文仍在输入段）
function fxCmdIntentAttach(det, decoded) {
    if (!det || !decoded) {
        return;
    }
    det.appendChild(elText('div', 'cmd-intent', decoded.detail));
}
