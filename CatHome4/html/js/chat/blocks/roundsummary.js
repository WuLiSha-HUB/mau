// ═══════════════════════════════════════════
// blocks/roundsummary.js —— roundsummary 块（persist 类 · 轮末总结）
//
// 契约：
//   item = { type:'roundsummary', ts, msgIndex, round, payload:{ text, audio } }
//   来源：LLMSimpleRequest 服务产出一句话总结（20-50 字）；失败侧 text 为 ERR| 文本（失败可见）
//   audio（A265）：拼接单文件名（无目录）——按轮次唯一，前端点播走 `/api/v1/audio/<名>`；
//     失败侧 / 播报关闭 / 目录未配置时缺省不写该键（前端不渲染播放按钮）
//   位置：固定插在轮末统计（roundsum）之后
//
// 产出：.chat-row.assistant.summary > .chat-bubble.roundsummary > .rsm-head + .rsm-text
//   （形态声明见 registry.js BLOCK_DECL）
//
// 安全：用户/模型文本一律 textContent 写入（不自拼 HTML——防注入与子元素销毁）
// ═══════════════════════════════════════════

function buildRoundSummaryNoteBlock(payload) {
    var row = blockRow('roundsummary');
    var bubble = el('div', bodyClass('roundsummary'));
    var head = el('div', 'rsm-head');
    head.textContent = '📝 总结';
    bubble.appendChild(head);
    var body = el('div', 'rsm-text');
    body.textContent = (payload && payload.text) ? payload.text : '';
    bubble.appendChild(body);
    var audioName = (payload && payload.audio) ? String(payload.audio).trim() : '';
    if (audioName.length > 0) {
        bubble.appendChild(buildRoundSummaryPlayer(audioName));
    }
    row.appendChild(bubble);
    return row;
}

// 总结音频播放器（A265）——点播走浏览器原生 Audio；源 = 音频资产端点 `/api/v1/audio/<文件名>`。
// 懒创建（首次点击才请求）：页面历史大量总结块时不产生成批网络请求；命名按轮次唯一，可安全缓存。
function buildRoundSummaryPlayer(audioName) {
    var wrap = el('div', 'rsm-audio');
    var btn = el('button', 'rsm-audio-btn');
    btn.type = 'button';
    btn.textContent = '▶ 播放';
    var player = null;
    btn.addEventListener('click', function () {
        if (player === null) {
            player = new Audio('/api/v1/audio/' + encodeURIComponent(audioName));
            player.addEventListener('ended', function () { btn.textContent = '▶ 播放'; });
            player.addEventListener('error', function () {
                btn.textContent = '⚠ 音频不可用';
                btn.disabled = true;
            });
        }
        if (!player.paused) {
            player.pause();
            player.currentTime = 0;
            btn.textContent = '▶ 播放';
            return;
        }
        var ret = player.play();
        btn.textContent = '⏸ 播放中';
        if (ret && typeof ret.catch === 'function') {
            ret.catch(function () {
                btn.textContent = '⚠ 音频不可用';
                btn.disabled = true;
            });
        }
    });
    wrap.appendChild(btn);
    return wrap;
}
