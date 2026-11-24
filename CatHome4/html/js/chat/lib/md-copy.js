// ═══════════════════════════════════════════
// lib/md-copy.js —— MD 块复制按钮（代码块 / 表格右上角）
// 来源：chat-view.js 第 646-712 行（A81 MD 块复制原文）
// 分层：md.js 出结构（.md-copy 包裹 + data-md 源文本）→ 本层插按钮与绑定事件
// 取舍：保留整节；失败告警由 uiWarn（全局弹层）改为 console.warn（素材不引全局 UI 面）
// ═══════════════════════════════════════════

function mdCopySource(wrap) {
    // 源文本单一出口——data-md 优先（表格：含分隔行原文）；否则取代码块正文（pre code 的 textContent 天然等于原文）
    var src = wrap.getAttribute('data-md');
    if (src !== null && src !== undefined) { return src; }
    var code = wrap.querySelector('pre code');
    if (code) { return code.textContent; }
    return '';
}

function mdLegacyWriteClipboard(text) {
    // 非安全上下文兜底（http:// 且非 localhost 时 navigator.clipboard 为 undefined）：
    // 临时 textarea + execCommand('copy')——该命令无安全上下文限制，点击本身是用户手势
    if (typeof document === 'undefined' || typeof document.execCommand !== 'function') {
        return Promise.reject(new Error('clipboard 不可用'));
    }
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.top = '-1000px';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    if (ta.setSelectionRange) { ta.setSelectionRange(0, text.length); }
    var ok = false;
    try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
    document.body.removeChild(ta);
    return ok ? Promise.resolve() : Promise.reject(new Error('clipboard 不可用'));
}

function mdWriteClipboard(text) {
    // 剪贴板写入——navigator.clipboard 优先（安全上下文）；缺失或失败由调用方退到 mdLegacyWriteClipboard
    if (typeof navigator !== 'undefined' && navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
        return navigator.clipboard.writeText(text);
    }
    return mdLegacyWriteClipboard(text);
}

function mdCopyIcon() {
    // 线条复制图标——双框（Feather 风格内联 SVG：零依赖、零字体差异、随 currentColor 取灰）
    var ns = 'http://www.w3.org/2000/svg';
    var svg = document.createElementNS(ns, 'svg');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('width', '12');
    svg.setAttribute('height', '12');
    svg.setAttribute('fill', 'none');
    svg.setAttribute('stroke', 'currentColor');
    svg.setAttribute('stroke-width', '2');
    svg.setAttribute('stroke-linecap', 'round');
    svg.setAttribute('stroke-linejoin', 'round');
    var back = document.createElementNS(ns, 'rect');
    back.setAttribute('x', '9');
    back.setAttribute('y', '9');
    back.setAttribute('width', '13');
    back.setAttribute('height', '13');
    back.setAttribute('rx', '2');
    var front = document.createElementNS(ns, 'path');
    front.setAttribute('d', 'M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1');
    svg.appendChild(back);
    svg.appendChild(front);
    return svg;
}

function mdAppendCopyBtn(wrap) {
    // 复制按钮——线条图标（内联 SVG；零文本节点：不污染 textContent 断言与朗读）；成功切 done 类 1.2s 后复原
    var btn = el('button', 'md-copy-btn');
    btn.type = 'button';
    btn.title = '复制原文';
    btn.setAttribute('aria-label', '复制原文');
    btn.appendChild(mdCopyIcon());
    btn.addEventListener('click', function () {
        mdWriteClipboard(mdCopySource(wrap)).then(function () {
            btn.classList.add('done');
            btn.title = '已复制';
            window.setTimeout(function () {
                btn.classList.remove('done');
                btn.title = '复制原文';
            }, 1200);
        }).catch(function (e) {
            btn.title = '复制失败——请手动选择文本后复制';
            console.warn('复制原文失败', e);
        });
    });
    wrap.insertBefore(btn, wrap.firstChild);
}

function mdBindCopy(root) {
    // 整块渲染后一次性挂载——遍历 .md-copy 包裹
    var wraps = root.querySelectorAll('.md-copy');
    for (var i = 0; i < wraps.length; i = i + 1) {
        mdAppendCopyBtn(wraps[i]);
    }
}
