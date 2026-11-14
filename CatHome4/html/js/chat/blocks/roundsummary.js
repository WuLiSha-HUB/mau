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
var audioUnlockDone = false;   // 已取得用户激活（本地兜底标志——浏览器给不出事实时用）
var pendingAutoPlay = null;    // 最近一次因未解锁而失败的自动播（单条，不排队——历史块不批量补播）

/// 自动播许可判据——**单一出口**：先问浏览器（`navigator.userActivation.hasBeenActive` = sticky activation），
/// 取不到才回落本地交互标志。动机：本地标志只在解锁监听触发时置真，监听若晚于首次交互即永久失准——
/// 「页面从未交互」与「已交互但标志没置上」在前端表现一样（都不播），归因却完全不同。
function audioAllowedToAutoplay() {
    try {
        if (typeof navigator !== 'undefined' && navigator.userActivation) {
            return navigator.userActivation.hasBeenActive === true;
        }
    } catch (e) {
        // 探针不可用——回落本地标志（不抛）
    }
    return audioUnlockDone === true;
}

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

    // 产物等待窗口（2026-11-14 · 莎报障「推送即不可用、刷新可播」）——
    // 总结块**先落盘**、云端 TTS 后装配成单文件（ChatSession.Meta「块先落盘不回转写」是既定设计：
    // UI 不被合成阻塞）。于是前端拿到 audio 名的那一刻文件常常还没写完，首次请求必然 404。
    // 处置 = 指数退避重试：窗口内到货即自动播；窗口用尽才是「真不可用」（与策略拒绝再分开）。
    var loadRetry = 0;
    var maxLoadRetry = 6;
    var loadRetryDelays = [1000, 2000, 3000, 5000, 8000, 12000];

    // 🔴 单飞与代际（2026-11-14 三次修正 · 莎报障「重试成功后反复播放」）——
    // 一次 404 会**同时**触发两条失败路径（元素 `error` 事件 + `play()` 的 reject），
    // 各自排一个重试就变成多个 Audio 实例同时起播；旧元素未弃用时它的迟到 `play()` 还会兑现成再一次发声。
    // 两条铁律：① 同一时刻只允许一个待重试排程（`retryTimer` 单飞）；② 每次重建递增代际，
    // 旧代际的迟到回调一律丢弃（`attemptGen` / `handledGen`）。
    var retryTimer = null;
    var attemptGen = 0;
    var handledGen = 0;

    // 弃用当前元素——暂停 + 摘监听 + 清源：旧元素从此不可能再发声（迟到 resolve 亦然）
    function releasePlayer() {
        if (player === null) {
            return;
        }
        try {
            player.removeEventListener('error', onElementError);
            player.removeEventListener('playing', onPlaying);
            player.removeEventListener('ended', onEnded);
            player.pause();
            player.src = '';
        } catch (e) {
            // 弃用失败不阻断——元素即将被丢弃
        }
        player = null;
    }

    // 排一次重试——单飞：已有排程时直接返回（双路径触发也只排一次）
    function scheduleRetry() {
        if (retryTimer !== null) {
            return;
        }
        var wait = loadRetryDelays[loadRetry];
        loadRetry = loadRetry + 1;
        btn.textContent = '… 等待音频';
        btn.title = '音频生成中（第 ' + String(loadRetry) + ' 次等待）';
        retryTimer = setTimeout(function () {
            retryTimer = null;
            releasePlayer();
            startPlay(autoPlay === true);
        }, wait);
    }

    // 音频地址——重试带 cache-buster：绕开浏览器对 404 的负缓存（端点按路径匹配，查询串不参与路由）
    function audioUrl() {
        var base = '/api/v1/audio/' + encodeURIComponent(audioName);
        return loadRetry > 0 ? base + '?r=' + String(loadRetry) : base;
    }

    // 加载失败分流——三种结局必须可区分：还在生成（等它）/ 策略拒绝（排队待解锁）/ 源真不可用（告警并禁用）
    function onLoadError(gen) {
        if (gen !== attemptGen) {
            return;
        }
        if (handledGen === gen) {
            return;
        }
        handledGen = gen;
        if (loadRetry < maxLoadRetry) {
            scheduleRetry();
            return;
        }
        // 窗口用尽——源真不可读（端点 404 / 文件损坏 / 格式不支持）：可见 + 留痕，不静默
        btn.textContent = '⚠ 音频不可用';
        btn.title = '音频源不可用：' + audioName;
        btn.disabled = true;
        if (typeof console !== 'undefined' && console.warn) {
            console.warn('[roundsummary] 音频加载失败：' + audioName + '（重试 ' + String(maxLoadRetry) + ' 次仍未就绪）');
        }
    }

    /// 元素级 error 监听入口——代际在闭包里，摘监听用同一引用
    function onElementError() {
        onLoadError(playerGen);
    }

    /// 起播事实——按钮状态跟音频状态走（不再在 play() 调用处提前改文案）
    function onPlaying() {
        if (playerGen !== attemptGen) {
            return;
        }
        loadRetry = 0;
        btn.textContent = '⏸ 播放中';
        btn.title = '';
    }

    /// 播完归位
    function onEnded() {
        btn.textContent = '▶ 播放';
    }

    /// 当前元素所属代际——元素迟到回调按此比对（旧代际一律丢弃）
    var playerGen = 0;

    // 懒建 Audio 元素——首次播放才发请求
    function ensurePlayer() {
        if (player !== null) {
            return;
        }
        attemptGen = attemptGen + 1;
        playerGen = attemptGen;
        player = new Audio(audioUrl());
        // 预取——自动播在数据到位后立即起播；页签隐藏不影响 Audio 播放（音频不受后台节流），
        // 故「后台播」不需要额外 visibility 处理，只需许可面成立。
        player.preload = 'auto';
        player.addEventListener('ended', onEnded);
        player.addEventListener('playing', onPlaying);
        player.addEventListener('error', onElementError);
    }

    // silent = 自动播路：许可不足只回落按钮；真实故障（解码 / 网络 / 源不支持）一律可见 + 留痕
    function startPlay(silent) {
        ensurePlayer();
        // 🔴 先试后判（2026-11-14 二次修正）——`hasBeenActive` 只反映**本文档**是否激活；
        //   从面板 / 链接跳转进来的新 document 恒为 false，而浏览器可能因站点媒体参与度（MEI）放行。
        //   先判会把「其实能播」的情形静默吞掉（症状 = 播报永远不响、按钮无异常），改为直接尝试，
        //   仅在 `NotAllowedError` 时排队——策略拒绝是**运行时事实**，不是可以事先推断的状态。
        var gen = playerGen;
        var ret = player.play();
        if (ret && typeof ret.catch === 'function') {
            ret.catch(function (err) {
                var errName = (err && err.name) ? String(err.name) : '';
                if (silent === true && errName === 'NotAllowedError') {
                    // 未取得用户激活（策略拒绝）：保留按钮 + 记一条待补播（解锁时补一次，历史块不批量补）
                    btn.textContent = '▶ 播放';
                    btn.title = audioAllowedToAutoplay() === true
                        ? '播放被拒——浏览器策略（点一下页面即可恢复自动播报）'
                        : '浏览器策略：页面交互后可自动播报';
                    pendingAutoPlay = function () { startPlay(true); };
                    return;
                }
                // 其余失败（含「产物还没写完」→ NotSupportedError / 解码失败）——统一走加载失败分流：
                // 未过等待窗口先等，窗口用尽才转告警并禁用（失败必须可见，但不把「还在生成」当「坏了」）
                if (typeof console !== 'undefined' && console.warn) {
                    console.warn('[roundsummary] 音频播放失败：' + errName + ' '
                        + (err && err.message ? String(err.message) : ''));
                }
                onLoadError(gen);
            });
        }
    }

    btn.addEventListener('click', function () {
        // 手动点播 = 新一轮尝试——作废在途重试（否则点了会同旧排程叠成两路并发，播两遍）
        loadRetry = 0;
        if (retryTimer !== null) {
            clearTimeout(retryTimer);
            retryTimer = null;
        }
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
