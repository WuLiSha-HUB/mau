// ═══════════════════════════════════════════
// blocks/text.js —— text 块（persist 类）
//
// 契约：
//   item = { type:'text', ts, msgIndex, round, payload:{ text } }
//   text = Markdown 原文（含图片包裹时先出缩略图组）
//
// 产出：.chat-row.assistant > .chat-bubble > (图片组?) + .md-block[.md-copy]
//   操作条已迁至轮末统计块（A387——回滚锚 = 轮末，不再挂在任意回复节点）
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
    return row;
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
