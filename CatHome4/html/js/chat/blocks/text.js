// ═══════════════════════════════════════════
// blocks/text.js —— text 块（persist 类）
//
// 契约：
//   item = { type:'text', ts, msgIndex, round, payload:{ text } }
//   text = Markdown 原文（含图片包裹时先出缩略图组）
//
// 产出：.chat-row.assistant > .chat-bubble > (图片组?) + .md-block[.md-copy] + (.node-actions?)
//   操作条（2026-10-06 复归）——`msgIndex ≥ 0` 时底部两按钮（⟲ 回滚 / ⧉ 分支）；指令走指令总线
//   `postCommand`（出口面），投递回执走通知面 `warn`（信息位归 state 段独占——契约 §12.7）
//
// 来源：chat-view.js chatMdFill（第 714-726 行）+ chatAppendHistoryBlock「text」分支
//       + chatAppendNodeActions（第 389-426 行）——操作条复归（旧版直写 fetch / chatInfo，本版改走总线与通知面）
//
// 丢弃（属主干，不入素材）：
//   · DOM 挂载 / 滚动跟随
//   · 历史收尾（chatHistoryFinish / chatRenderHistory）—— 属主编排
//   · 顶部信息行（前文 N 条 / tokens）—— 属主干状态面
// ═══════════════════════════════════════════

function buildTextBlock(payload, item) {
    // payload.text = Markdown 原文；item = 整条条目（块级 msgIndex 决定是否挂节点操作条）
    var p = payload || {};
    var row = blockRow('text');
    var bubble = el('div', bodyClass('text'));
    row.appendChild(bubble);
    fillMdBlock(bubble, p.text || '');
    appendNodeActions(bubble, item);
    return row;
}

/// P6b 节点操作条——正式回复底部按钮（⟲ 回滚）
/// 入口判据 = 块级 `msgIndex`（前文来源；独立块 -1 不挂）；指令走指令总线（出口面——本件不拼网络请求）
/// 投递回执走通知面（桌宠气泡 `warn`）——信息位归 state 段独占（契约 §12.7）
function appendNodeActions(bubble, item) {
    var idx = item ? item.msgIndex : undefined;
    if (typeof idx !== 'number' || idx < 0) {
        return;
    }
    var bar = el('div', 'node-actions');
    bar.appendChild(nodeActionBtn('⟲ 回滚', 'node-btn-rollback',
        '从此处继续对话（回滚——该回复后的内容将截断，不可恢复）', function () {
            if (!window.confirm('从此处继续对话？该回复之后的所有消息将被截断（不可恢复）。')) {
                return;
            }
            postCommand('session.rollback ' + idx);
            warn('回滚指令已投递——建议刷新页面');
        }));
    bubble.appendChild(bar);
}

/// 操作条按钮——文案（图标 + 标签）+ 悬浮提示 + 点击回调
function nodeActionBtn(label, variant, title, onClick) {
    var b = el('button', 'node-btn ' + variant);
    b.type = 'button';
    b.title = title;
    b.textContent = label;
    b.addEventListener('click', onClick);
    return b;
}

function fillMdBlock(bubble, content) {
    // assistant 泡内容填充——无图片包裹走原路径（md-block 单块）；有包裹 = 前段 + 缩略图组 + 后段
    // （2026-10-06 放宽：包裹可位于任意位置——前 / 后段各自成 MD 块，顺序即原文顺序）
    var r = imgSplit(content);
    if (r.items.length === 0) {
        fillMdPart(bubble, content);
        return;
    }
    if (r.before.length > 0) {
        fillMdPart(bubble, r.before);
    }
    bubble.appendChild(imageGroup(r.items));
    if (r.after.length > 0) {
        fillMdPart(bubble, r.after);
    }
}

function fillMdPart(bubble, text) {
    // MD 块填充——代码块 / 表格挂复制按钮（整块渲染后一次性挂载）
    var md = el('div', 'md-block');
    md.innerHTML = mdToHtml(text);
    mdBindCopy(md);
    bubble.appendChild(md);
    return md;
}
