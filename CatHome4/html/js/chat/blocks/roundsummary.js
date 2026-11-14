// ═══════════════════════════════════════════
// blocks/roundsummary.js —— roundsummary 块（persist 类 · 轮末总结）
//
// 契约：
//   item = { type:'roundsummary', ts, msgIndex, round, payload:{ text, audio } }
//   来源：LLMSimpleRequest 服务产出一句话总结（20-50 字）；失败侧 text 为 ERR| 文本（失败可见）
//   audio（A265）：三段（提示音 / 猫名 / 正文）装配成的单文件名（无目录）——按轮次唯一，
//     播放走 `/api/v1/audio/<名>`；失败侧 / 播报关闭 / 目录未配置时缺省不写该键（不渲染播放器）
//   播放（平台分离 P1）：服务端不再播——统一前端播，**追加路自动播**（新块到达即播；
//     被浏览器自动播放策略拒绝时静默回落按钮），历史重放（full）不自动播
//   位置：固定插在轮末统计（roundsum）之后
//
// 产出：.chat-row.assistant.summary > .chat-bubble.roundsummary > .rsm-head + .rsm-text [+ .rsm-audio]
//   （形态声明见 registry.js BLOCK_DECL）
//
// 安全：用户/模型文本一律 textContent 写入（不自拼 HTML——防注入与子元素销毁）
// ═══════════════════════════════════════════

/// roundsummary 块构建——auto 为真（仅追加路）时尝试自动播放
function buildRoundSummaryNoteBlock(payload, item, auto) {
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
        bubble.appendChild(buildRoundSummaryPlayer(audioName, auto === true));
    }
    row.appendChild(bubble);
    return row;
}

// ── 自动播放解锁（浏览器策略 · 2026-11-14）────────────────────────
// 事实：页面的「用户激活」是发声的前置——页面从未被点击时 `play()` 抛 NotAllowedError，
//   用户点过任意处之后才允许播放；**与页签是否可见无关**（后台标签页同样可播）。
// 处置：首次 pointerdown / keydown 做一次静音播放完成解锁，并补播最近一条被拒的自动播。
var audioUnlockDone = false;   // 已取得用户激活
var pendingAutoPlay = null;    // 最近一次因未解锁而失败的自动播（单条，不排队——历史块不批量补播）

/// 静音短播——仅用于取得播放许可，不产生声音
function probeAudioUnlock() {
    try {
        var probe = new Audio('data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEAgD4AAAB9AAACABAAZGF0YQAAAAA=');
        probe.muted = true;
        var ret = probe.play();
        if (ret && typeof ret.catch === 'function') {
            ret.catch(function () { });
        }
    } catch (e) {
        // 解锁失败不阻断——自动播继续回落按钮（出口 = 按钮可点）
    }
}

/// 解锁挂装——首次调用两点装监听（监听器自身只触发一次）
function installAudioUnlock() {
    if (installAudioUnlock.installed === true || audioUnlockDone === true) {
        return;
    }
    installAudioUnlock.installed = true;
    if (typeof document === 'undefined' || typeof document.addEventListener !== 'function') {
        return;
    }
    function onUnlock() {
        audioUnlockDone = true;
        probeAudioUnlock();
        var retry = pendingAutoPlay;
        pendingAutoPlay = null;
        if (typeof retry === 'function') {
            retry();
        }
        document.removeEventListener('pointerdown', onUnlock, true);
        document.removeEventListener('keydown', onUnlock, true);
    }
    document.addEventListener('pointerdown', onUnlock, true);
    document.addEventListener('keydown', onUnlock, true);
}

installAudioUnlock();

// 总结音频播放器（A265 / 平台分离 P1）——源 = 音频资产端点 `/api/v1/audio/<文件名>`。
// 懒创建（首次播放才请求）：历史块按需点播，不产生成批网络请求；命名按轮次唯一，可安全缓存。
// 🔴 自动播与手动播的失败语义不同——自动播被浏览器「用户手势」策略拒绝属正常回落
//    （不动按钮、不禁用），手动播失败才是真不可用（按钮转告警并禁用）。
function buildRoundSummaryPlayer(audioName, autoPlay) {
    var wrap = el('div', 'rsm-audio');
    var btn = el('button', 'rsm-audio-btn');
    btn.type = 'button';
    btn.textContent = '▶ 播放';
    var player = null;

    // 懒建 Audio 元素——首次播放才发请求
    function ensurePlayer() {
        if (player !== null) {
            return;
        }
        player = new Audio('/api/v1/audio/' + encodeURIComponent(audioName));
        player.addEventListener('ended', function () { btn.textContent = '▶ 播放'; });
        player.addEventListener('error', function () {
            btn.textContent = '⚠ 音频不可用';
            btn.disabled = true;
        });
    }

    // silent = 自动播路：策略拒绝只回落按钮
    function startPlay(silent) {
        ensurePlayer();
        var ret = player.play();
        btn.textContent = '⏸ 播放中';
        if (ret && typeof ret.catch === 'function') {
            ret.catch(function () {
                if (silent) {
                    // 未取得用户激活：保留按钮 + 记一条待补播（解锁时补一次，历史块不批量补）
                    btn.textContent = '▶ 播放';
                    btn.title = '浏览器策略：页面交互后可自动播报';
                    pendingAutoPlay = function () { startPlay(true); };
                    return;
                }
                btn.textContent = '⚠ 音频不可用';
                btn.disabled = true;
            });
        }
    }

    btn.addEventListener('click', function () {
        ensurePlayer();
        if (!player.paused) {
            player.pause();
            player.currentTime = 0;
            btn.textContent = '▶ 播放';
            return;
        }
        startPlay(false);
    });

    wrap.appendChild(btn);
    if (autoPlay) {
        startPlay(true);
    }
    return wrap;
}
