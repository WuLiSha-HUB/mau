// ═══════════════════════════════════════════
// chat/input.js —— 输入区与动作按钮
//
// 定位：用户侧唯一出口。发送 / 停止 / 继续 / 新会话 / 刷新 / 临时区切换。
// 指令一律走指令总线（POST /api/v1/command）——前端不拼业务指令串以外的逻辑。
//
// 输入区与临时区**同区域互斥**：同一列同位，输入框与 live 面板二选一（代码切内联 display）；
//   切换入口 = 输入框左侧竖向滑块开关（**单按钮**——整体点击即在输入 / 流式之间翻转，标签随态改字）。
//   回车发送时自动切回输入态，想看临时内容自己点回来（纯前端可视化，判据取全局态）。
//   本件只切本区两块——不碰对话区（#chatMsgs）与滚动带（#chatScrollBand，显隐归 fx/scroll）。
// ═══════════════════════════════════════════

/// 临时区显示态——false = 输入态（输入框可见），true = 临时内容（面板可见）
var liveShown = false;

/// 核心对话显示态——false = 全显示（默认），true = 只留核心对话（隐藏非 user / text 行）
var coreOnly = false;

/// 指令投递——统一出口（失败可见；投递即回执，结果异步经 SSE 回来）
/// images 可选——图片绝对路径列表（待发区）；编号与包裹由后端组装，前端只投路径
function postCommand(text, images) {
    var payload = { text: text };
    if (images && images.length > 0) {
        payload.images = images;
    }
    return fetch('/api/v1/command', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    }).then(function (r) {
        return r.json();
    }).then(function (j) {
        if (!j || j.ok !== true) {
            warn('指令未受理：' + text + (j && j.error ? '（' + j.error + '）' : ''));
        }
        return j;
    }).catch(function (e) {
        warn('指令投递失败', e);
        return null;
    });
}

/// 发送——空文本且无待发图零动作；发送时只滚底，不本地切态（切态由持久区新块驱动：
///   user 块到达 → 切流式；roundsum 到达 → 切回输入）
function chatSend() {
    var box = document.getElementById('chatSendInput');
    if (!box) {
        return;
    }
    var text = (box.value || '').replace(/^\s+|\s+$/g, '');
    if (typeof pendBusy === 'function' && pendBusy()) {
        // 上传未完成不发——防漏图（提示走待发区尾部，不抢顶部状态位）
        pendNotice('图片上传中——稍候再发');
        return;
    }
    var paths = (typeof pendPaths === 'function') ? pendPaths() : [];
    if (text.length === 0 && paths.length === 0) {
        return;
    }
    // 🔴 用户消息必须带 `Chat ` 前缀——宿主 `DispatchCommandForCat` 只认前缀行，裸文本 `return false` 不投递
    // 临时模型同步（页面级）：新会话清过后端覆盖 → 发送前补投，保证「页不关即沿用」。
    // 同步先于投递完成（串行 .then）——否则后端覆盖可能后到，本轮请求仍用旧模型。
    var sendText = 'Chat ' + text;
    var dispatch = function () { postCommand(sendText, paths); };
    if (typeof apiModelSync === 'function') {
        apiModelSync().then(dispatch);
    } else {
        dispatch();
    }
    box.value = '';
    if (typeof pendClear === 'function') {
        pendClear();
    }
    // 切态归持久区新块（user 块 → 流式 / roundsum → 输入；persist.js persistAutoSwap）——本处只滚底
    scrollBottomNow();
}

/// 停止本轮——已生成内容保留（后端负责前文格式修复）
function chatPause() {
    postCommand('cat.pause');
}

/// 继续——用当前前文直接再发一次请求（不追加消息）
function chatContinue() {
    postCommand('cat.continue');
}

/// 新会话——清前文并重新注入；**二次确认**（前文不可恢复：文档要求与节点操作条同款确认框）；服务端随后在流内重发全量帧
function chatNewSession() {
    if (!window.confirm('新建会话？前文将被清空并重新注入知识文件（不可恢复）。')) {
        return;
    }
    postCommand('session.new');
}

/// 刷新——纯前端重建：断开重连即由服务端重推全量帧（零本地状态修补）
function chatRefresh() {
    if (chatSse) {
        chatSse.close();
        chatSse = null;
    }
    chatConnect();
}

/// 临时区 / 输入区切换——同区域互斥（开关整体点击入口：翻转当前态）
function chatLiveToggle() {
    liveShown = (liveShown !== true);
    inputSwap(liveShown);
}

/// 直达指定态——**自动切换入口**（persist 新块驱动：**人工** user 块 → 流式 / roundsum → 输入；
/// 系统注入块不触发本入口——A204）；同态零动作（幂等）
function chatLiveShow(shown) {
    var want = (shown === true);
    if (liveShown === want) {
        return;
    }
    liveShown = want;
    inputSwap(want);
}

/// 区域显隐单点——输入框与 live 面板二选一（元素缺失即跳过）；按钮激活态随动
/// 🔴 面板高度**固定** = 输入框实测高度（同一把尺：切换前后区域高度一致；空态亦保持同高，不被压缩成一行）
function inputSwap(shown) {
    var input = document.getElementById('chatSendInput');
    var panel = liveContainer();
    if (panel) {
        if (shown === true && input) {
            var h = input.offsetHeight;
            panel.style.height = (h > 0 ? h : 0) + 'px';
        }
        panel.style.display = shown ? '' : 'none';
        if (shown) {
            // 切到流式区即贴底——面板是「看最新」的窥视窗（流式跟随步见 stream.js）
            scrollBoxBottom(panel);
        }
    }
    if (input) {
        input.style.display = shown ? 'none' : '';
    }
    var sw = document.getElementById('chatLiveToggle');
    if (sw) {
        if (shown) {
            sw.classList.add('on');
        } else {
            sw.classList.remove('on');
        }
    }
    // 单按钮标签随态改字（2026-10-06 合并）——输入态「输入」/ 流式态「流式」（上下位置随滑块，归 CSS）
    var label = document.getElementById('chatLiveLabel');
    if (label) {
        label.textContent = shown ? '流式' : '输入';
    }
}

/// 核心对话显隐——左预留带顶部开关（整体点击翻转；默认全显示，无持久化）
function chatCoreToggle() {
    coreSwap(coreOnly !== true);
}

/// 核心对话显隐单点——#chatMsgs 加 / 去 .core-only（隐藏非 user / text 行）；开关激活态与标签随动
/// 🔴 滚动带几何随 #chatMsgs 类变化自动重算（fx/scroll.js MutationObserver attributeFilter 含 class）
function coreSwap(on) {
    coreOnly = (on === true);
    var msgs = document.getElementById('chatMsgs');
    if (msgs) {
        if (coreOnly) {
            msgs.classList.add('core-only');
        } else {
            msgs.classList.remove('core-only');
        }
    }
    var sw = document.getElementById('chatCoreToggle');
    if (sw) {
        if (coreOnly) {
            sw.classList.add('on');
        } else {
            sw.classList.remove('on');
        }
    }
    var label = document.getElementById('chatCoreLabel');
    if (label) {
        label.textContent = coreOnly ? '精简' : '全部';
    }
}

/// 核心对话开关定位——fixed 顶边对齐对话区顶部（#chatbar 高度随字号变化，故用 JS 而非固定像素）
function coreToggleLayout() {
    var sw = document.getElementById('chatCoreToggle');
    var msgs = document.getElementById('chatMsgs');
    if (!sw || !msgs) {
        return;
    }
    sw.style.top = Math.round(msgs.getBoundingClientRect().top + 8) + 'px';
}

/// 输入区接线——元素缺失即跳过（防御式）；本件加载于页面尾部，DOM 已就绪
function inputBind() {
    var send = document.getElementById('chatSendBtn');
    if (send) {
        send.addEventListener('click', chatSend);
    }
    var input = document.getElementById('chatSendInput');
    if (input) {
        input.addEventListener('keydown', function (e) {
            // Enter 发送（阻止 textarea 插入换行）；Shift+Enter 走默认换行
            if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                chatSend();
            }
        });
    }
    var pause = document.getElementById('chatPause');
    if (pause) {
        pause.addEventListener('click', chatPause);
    }
    var cont = document.getElementById('chatContinue');
    if (cont) {
        cont.addEventListener('click', chatContinue);
    }
    var fresh = document.getElementById('chatNew');
    if (fresh) {
        fresh.addEventListener('click', chatNewSession);
    }
    var reload = document.getElementById('chatRefresh');
    if (reload) {
        reload.addEventListener('click', chatRefresh);
    }
    var toggle = document.getElementById('chatLiveToggle');
    if (toggle) {
        // 开关点击——单按钮：整体点击即在输入 / 流式之间翻转（标签随态改字，位置归 CSS）
        toggle.addEventListener('click', chatLiveToggle);
    }
    var core = document.getElementById('chatCoreToggle');
    if (core) {
        // 核心对话开关——整体点击翻转；顶边对齐对话区（字号缩放 / 窗口变化时重算）
        core.addEventListener('click', chatCoreToggle);
        coreToggleLayout();
        if (typeof window.addEventListener === 'function') {
            window.addEventListener('resize', coreToggleLayout);
            window.addEventListener('load', coreToggleLayout);
        }
    }
    var jump = document.getElementById('chatJumpBottom');
    if (jump) {
        jump.addEventListener('click', scrollBottomNow);
    }
}

inputBind();
