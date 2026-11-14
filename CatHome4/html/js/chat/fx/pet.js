// ═══════════════════════════════════════════
// chat/fx/pet.js —— 桌宠渲染（独立功能件 A171 · 纯前端运算）
//
// 定位：读状态段的 runState（六态）→ 态映射动画资源 → 调度空闲节律与交互反应。
// 启动：由 fx/registry.js 的 fxBoot 统一调用 chatPetInit（脚本加载不自启）
// 铁律：前端零业务逻辑——本件只做「态 / 交互 → 资源」映射与动画调度，不产生业务事件、不介入会话状态机。
// 数据源：`appState.runState`（六态） + `appState.pet.kind`（皮肤——A245）——经分发表 `state/registry.js::STATE_DECL` 的 pet 行调入本件入口 · SSE 断线由 main.js 的 onerror / onopen 置位。
// 资源：`html/pet/<skin>/*.webp`（256x256 原尺寸 1:1——整帧不裁剪 · 20ms/帧 · 透明底；skin 空 / 无匹配 = 无桌宠不显示，不回落——A251）；清单与时长 → `js/pet-manifest.js`（生成物，多套 {skin:{name:{h,ms}}}）。
//
// 形态语义（素材约定）：
//   loop-*   循环态（link/wait/think/tool/run/reply/sseErr/idle/idle-hook/idle-sleep）
//   before-* 过渡动画（播一遍）——before-idle-hook 移入 / before-idle-sleep 入睡
//            · before-idle 实为 **after-reply 语义**（整轮结束的收尾动作）——只在「活跃态 → 空闲」时播一次
//   after-*  后置反应（播一遍）——after-idle-check 点击后的摸头反应
// 空闲节律：进空闲播基础待机 5s → 入睡过渡 → 睡眠循环；hook / check 结束后回到该周期
// 渐隐规则：before → loop、loop → after 两段是连续动作——**瞬切**；其余切换走 0.25s 交叉溶解
// 最短驻留：tool / run 至少播满「≥2s 的真实循环整数倍」——极短态不闪断；期内真实态变化只记一笔，
//          期满按**当前真实态**跳转；断线态优先让位
// 素材加载：初始化即串行加载 → blob 持有 + Cache API 缓存（失效键 = 每张素材的内容哈希）
//          · 加载门禁——首图 loop-sseErr 就位前桌宠不启动；就位后常显该图作加载态；全量就绪才开放调度
//          · blob 持有 = 断线时零网络依赖；右键菜单「清理缓存」主动清桶（A179——按钮随桌宠本体，不再挂顶栏）
// 通知面（A179 收尾）：前端提示 / 告警的输出口 = 桌宠「说话」气泡 `chatPetSay`——不再写顶栏信息位
// ═══════════════════════════════════════════

// ── 状态与常量 ───────────────────────────────
var chatPetImgs = [null, null];   // 双层图片元素（交叉溶解——交替作为当前层）
var chatPetLayer = 0;             // 当前显示层下标
var chatPetCur = '';              // 当前资源名（不含扩展名）——同值不切换（防重复溶解）
var chatPetDrag = null;           // 拖动状态 {dx, dy, x0, y0}——null = 未按下
var chatPetDragMoved = false;     // 本次按下是否发生位移（决定随后 click 是否算摸头）
var chatPetOffline = false;       // SSE 断线标记（main.js 的 onerror / onopen 置位）
var chatPetMode = '';             // 调度模式：run（活跃态）/ idle（空闲）/ offline / loading
var chatPetKey = '';              // 当前态 key（六态名 / idle / offline）
var chatPetKeyAt = 0;             // 进入当前态的时间戳
var chatPetTimer = null;          // 显示调度定时器（单一定时器——任何切换先清）
var chatPetIdleMode = '';         // 空闲子模式：intro / base / hook / check / sleep
var chatPetIdleIdx = -1;          // 上次基础待机下标（避免连续重复）
var chatPetCheckIdx = -1;         // 上次摸头反应下标（避免连续重复）
var chatPetHovered = false;       // 鼠标是否停留在桌宠上（决定反应结束后回哪个子模式）
var chatPetLastInstant = false;   // 上一次换图是否为瞬切
var chatPetDwellActive = false;   // 最短驻留期内（tool / run）
var chatPetDwellPend = false;     // 驻留期内真实态有变化（期满按当前真实态跳转）
var chatPetDwellTimer = null;     // 驻留定时器（独立于显示调度定时器）
var chatPetRes = {};              // 素材持有表（名称 → blob URL）
var chatPetLoading = true;        // 加载门禁——true 期间不开放调度
var chatPetBooted = false;        // 首图（loop-sseErr）是否已就位
var chatPetFail = 0;              // 加载失败计数——全量结束后汇总告警（不静默）
var CHAT_PET_CACHE_PREFIX = 'ch4-pet';   // Cache API 桶名（固定——失效靠逐张内容哈希，不靠换桶名）
var chatPetManifest = (typeof chatPetManifestData !== 'undefined') ? chatPetManifestData : {};   // 素材清单（多套 {skin:{name:{h,ms}}}；缺失降级空表）
var chatPetSkin = '';             // 当前皮肤（state 段 pet.kind；空/无匹配=无桌宠不显示——A245/A251）
var chatPetLoadStarted = false;   // 素材加载哨兵（皮肤确定后启动一次——A245）
// 接缝溶解名单——before 末帧与 loop 首帧姿态不接的对，强制走溶解盖接缝
var CHAT_PET_SEAM_BLEND = {
    'loop-idle-sleep': true, 'loop-idle-hook': true, 'loop-idle': true
};

/// 六态 → 循环资源
var chatPetPhaseMap = { link: 'loop-link', wait: 'loop-wait', think: 'loop-think', tool: 'loop-tool', run: 'loop-run', reply: 'loop-reply' };

var CHAT_PET_IDLE_BASE = ['loop-idle'];   // 基础待机（单张；pick 逻辑兼容任意张数）
var CHAT_PET_CHECKS = [
    { name: 'after-idle-check', ms: 580 }
];
var CHAT_PET_IDLE_BASE_HOLD_MS = 5000;    // 基础待机时长——到点进入睡（前端节律，非素材时长）
var CHAT_PET_BEFORE_SLEEP_LOOPS = 2;      // 入睡过渡遍数（切点落在整遍边界不跳帧）
var CHAT_PET_MIN_LOOP_MS = 2000;          // 驻留目标下限（实际 = 循环时长 × 向上取整（下限 / 循环时长））
var CHAT_PET_MIN_KEYS = { tool: true, run: true };   // 有驻留义务的态
var CHAT_PET_LOOP_FALLBACK_MS = 320;      // 循环时长兜底（manifest 缺项时用）
// 以下为素材播放时长的**兜底值**——实际以 manifest 为准（素材重转后无需改这里）
var CHAT_PET_BEFORE_IDLE_MS = 1980;
var CHAT_PET_BEFORE_HOOK_MS = 1140;
var CHAT_PET_BEFORE_SLEEP_MS = 1980;

/// 全量资源名（预加载清单——须与 html/pet/ 目录一致）
var chatPetFiles = [
    'loop-link', 'loop-wait', 'loop-think', 'loop-tool', 'loop-run', 'loop-reply', 'loop-sseErr',
    'before-idle',
    'loop-idle',
    'loop-idle-hook', 'before-idle-hook',
    'loop-idle-sleep', 'before-idle-sleep',
    'after-idle-check'
];

// ── 基础操作 ─────────────────────────────────
/// 清显示调度定时器——任何切换先清（防陈旧定时器改图）
function chatPetClearTimer() {
    if (chatPetTimer !== null) {
        clearTimeout(chatPetTimer);
        chatPetTimer = null;
    }
}

/// 随机取下标——避免与上一次相同（n<=1 直接 0）
function chatPetPick(n, last) {
    if (n <= 1) {
        return 0;
    }
    var idx = last;
    var guard = 0;
    while (idx === last && guard < 20) {
        idx = Math.floor(Math.random() * n);
        guard = guard + 1;
    }
    return idx;
}

/// 换图——instant 时瞬切（连续动作），否则双层交叉溶解 0.25s；接缝名单内的目标强制溶解
function chatPetShow(name, instant) {
    if (chatPetImgs[0] === null) {
        return;
    }
    if (chatPetCur === name) {
        return;
    }
    var useInstant = (instant === true) && (CHAT_PET_SEAM_BLEND[name] !== true);
    chatPetCur = name;
    chatPetLastInstant = useInstant;
    var cur = chatPetImgs[chatPetLayer];
    var next = chatPetImgs[1 - chatPetLayer];
    if (useInstant === true) {
        next.style.transition = 'none';
        cur.style.transition = 'none';
    }
    next.src = chatPetSheet(name);
    next.style.opacity = '1';
    cur.style.opacity = '0';
    if (useInstant === true) {
        void next.offsetWidth;          // 强制应用后再恢复过渡设置（此后切换仍溶解）
        next.style.transition = '';
        cur.style.transition = '';
    }
    chatPetLayer = 1 - chatPetLayer;
}

// ── 运行态与断线 ─────────────────────────────
/// 最短驻留时长——目标下限向上取真实循环时长的整数倍（至少一遍）
function chatPetMinMs(key) {
    var ms = chatPetMs(chatPetPhaseMap[key], CHAT_PET_LOOP_FALLBACK_MS);
    if (ms <= 0) {
        return 0;
    }
    var loops = Math.ceil(CHAT_PET_MIN_LOOP_MS / ms);
    if (loops < 1) {
        loops = 1;
    }
    return ms * loops;
}

/// 解除驻留约束——清定时器与待切标记
function chatPetClearDwell() {
    if (chatPetDwellTimer !== null) {
        clearTimeout(chatPetDwellTimer);
        chatPetDwellTimer = null;
    }
    chatPetDwellActive = false;
    chatPetDwellPend = false;
}

/// 起算驻留——仅 tool / run（其余态无约束）
function chatPetDwellStart(key) {
    chatPetClearDwell();
    if (CHAT_PET_MIN_KEYS[key] !== true) {
        return;
    }
    var ms = chatPetMinMs(key);
    if (ms <= 0) {
        return;
    }
    chatPetDwellActive = true;
    chatPetDwellTimer = setTimeout(function () {
        chatPetDwellTimer = null;
        chatPetDwellDone();
    }, ms);
}

/// 驻留期满——解除约束；期内真实态变过则按当前真实态跳转
function chatPetDwellDone() {
    chatPetDwellActive = false;
    var pend = chatPetDwellPend;
    chatPetDwellPend = false;
    if (pend === true) {
        chatPetSync();
    }
}

/// 驻留期内——真实态变化只记一笔，不换图（返回 true = 已挂起）
function chatPetDwellHold() {
    if (chatPetDwellActive !== true) {
        return false;
    }
    chatPetDwellPend = true;
    return true;
}

/// 活跃态——六态直接映射循环资源
function chatPetEnterRun(key) {
    if (chatPetMode === 'run' && chatPetKey === key) {
        return;
    }
    if (chatPetDwellHold()) {
        return;   // 驻留期内——只记变化，期满按当前真实态跳转（跳过中间态）
    }
    chatPetMode = 'run';
    chatPetKey = key;
    chatPetKeyAt = Date.now();
    chatPetClearTimer();
    chatPetShow(chatPetPhaseMap[key]);
    chatPetDwellStart(key);
}

/// 断线态——SSE 不通即显错图；恢复由 main.js 的 onopen 置位后重新同步
function chatPetEnterOffline() {
    if (chatPetMode === 'offline') {
        return;
    }
    chatPetClearTimer();
    chatPetClearDwell();   // 断线优先——驻留让位（链路状态最需要可见）
    chatPetMode = 'offline';
    chatPetKey = 'offline';
    chatPetKeyAt = Date.now();
    chatPetShow('loop-sseErr');
}

// ── 空闲行为（intro / base / hook / check / sleep）──
/// 进入空闲——活跃态回空闲（整轮结束）先播 before-idle 收尾动作再进基础待机
function chatPetEnterIdle() {
    if (chatPetMode === 'idle') {
        return;
    }
    if (chatPetDwellHold()) {
        return;   // 驻留期内——整轮结束也等播满
    }
    var fromRun = (chatPetMode === 'run');
    chatPetMode = 'idle';
    chatPetKey = 'idle';
    chatPetKeyAt = Date.now();
    chatPetClearTimer();
    chatPetClearDwell();
    if (fromRun) {
        chatPetIdleMode = 'intro';
        chatPetShow('before-idle');
        chatPetTimer = setTimeout(function () {
            chatPetTimer = null;
            if (chatPetMode === 'idle' && chatPetIdleMode === 'intro') {
                chatPetPlayBase(true);
            }
        }, chatPetMs('before-idle', CHAT_PET_BEFORE_IDLE_MS));
        return;
    }
    chatPetPlayBase(false);
}

/// 基础待机——loop-idle 播 5s，到点直接进入睡
function chatPetPlayBase(instant) {
    chatPetIdleMode = 'base';
    var idx = chatPetPick(CHAT_PET_IDLE_BASE.length, chatPetIdleIdx);
    chatPetIdleIdx = idx;
    chatPetClearTimer();
    chatPetShow(CHAT_PET_IDLE_BASE[idx], instant === true);
    chatPetTimer = setTimeout(function () {
        chatPetTimer = null;
        if (chatPetMode === 'idle' && chatPetIdleMode === 'base') {
            chatPetStartSleep();
        }
    }, CHAT_PET_IDLE_BASE_HOLD_MS);
}

/// 鼠标移入 / 移出（仅空闲态响应——运行态优先，桌宠在陪干活）
function chatPetIdleHook(on) {
    if (chatPetMode !== 'idle') {
        return;
    }
    if (on) {
        if (chatPetIdleMode === 'hook') {
            return;
        }
        chatPetIdleMode = 'hook';
        chatPetClearTimer();
        chatPetShow('before-idle-hook');
        chatPetTimer = setTimeout(function () {
            chatPetTimer = null;
            if (chatPetMode === 'idle' && chatPetIdleMode === 'hook') {
                chatPetShow('loop-idle-hook', true);
            }
        }, chatPetMs('before-idle-hook', CHAT_PET_BEFORE_HOOK_MS));
        return;
    }
    if (chatPetIdleMode === 'hook') {
        chatPetPlayBase(false);
    }
}

/// 鼠标点击——after-idle-check 播一遍；结束后按悬停状态回 hook 或基础待机
function chatPetIdleCheck() {
    if (chatPetMode !== 'idle') {
        return;
    }
    chatPetIdleMode = 'check';
    var idx = chatPetPick(CHAT_PET_CHECKS.length, chatPetCheckIdx);
    chatPetCheckIdx = idx;
    chatPetClearTimer();
    chatPetShow(CHAT_PET_CHECKS[idx].name, true);   // loop → after：瞬切
    chatPetTimer = setTimeout(function () {
        chatPetTimer = null;
        if (chatPetMode !== 'idle' || chatPetIdleMode !== 'check') {
            return;
        }
        if (chatPetHovered) {
            chatPetIdleMode = '';
            chatPetIdleHook(true);
        } else {
            chatPetPlayBase(false);
        }
    }, chatPetMs(CHAT_PET_CHECKS[idx].name, CHAT_PET_CHECKS[idx].ms));
}

/// 入睡——before-idle-sleep 播整遍后瞬切进睡眠循环
function chatPetStartSleep() {
    chatPetIdleMode = 'sleep';
    chatPetClearTimer();
    chatPetShow('before-idle-sleep');
    chatPetTimer = setTimeout(function () {
        chatPetTimer = null;
        if (chatPetMode === 'idle' && chatPetIdleMode === 'sleep') {
            chatPetShow('loop-idle-sleep', true);
        }
    }, chatPetMs('before-idle-sleep', CHAT_PET_BEFORE_SLEEP_MS) * CHAT_PET_BEFORE_SLEEP_LOOPS);
}

// ── 对外接口 ─────────────────────────────────
/// 从 state 段确定皮肤——读到即锁定并触发加载（A245；kind 空 / 无匹配 → 无桌宠，A251）
function chatPetApplySkinFromState() {
    var kind = (typeof appState !== 'undefined' && appState && appState.pet && appState.pet.kind !== undefined) ? appState.pet.kind : '';
    var skin = chatPetNormalizeSkin(kind);
    if (chatPetSkin === skin) {
        return;
    }
    chatPetSkin = skin;
    chatPetSetVisible(skin.length > 0);
    if (skin.length === 0) {
        return;
    }
    chatPetEnsureLoaded();
}

/// 规范化皮肤名——对齐 manifest 实际键（大小写不敏感）；空 / 无匹配 → ''（无桌宠，不回落——A251）
/// A245 判例：池字段为人工输入，`DeepDeep` 须命中 `deepdeep`——原先仅靠文件系统大小写不敏感容错，
///          且 manifest 查不中 → URL 失内容哈希版本（素材更新后浏览器缓存不失效），故显式归一。
function chatPetNormalizeSkin(kind) {
    var k = (kind === null || kind === undefined) ? '' : String(kind);
    if (k.length === 0) {
        return '';
    }
    var lower = k.toLowerCase();
    if (chatPetManifest[lower] !== undefined) {
        return lower;
    }
    if (chatPetManifest[k] !== undefined) {
        return k;
    }
    return '';
}

/// 桌宠显隐——皮肤空 / 无匹配即隐藏（空值即无桌宠，A251）
function chatPetSetVisible(on) {
    var box = document.getElementById('chatPet');
    if (box === null) {
        return;
    }
    box.style.display = (on === true) ? '' : 'none';
}

/// 素材加载哨兵——皮肤确定后启动一次（皮肤为空不加载——A251）
function chatPetEnsureLoaded() {
    if (chatPetLoadStarted === true) {
        return;
    }
    chatPetLoadStarted = true;
    chatPetLoadAll();
}

/// 状态同步——state 段分发表（`state/registry.js::STATE_DECL` 的 pet 行）调入（每次状态段渲染后）
function chatPetSync() {
    chatPetApplySkinFromState();
    if (chatPetImgs[0] === null) {
        return;
    }
    if (chatPetSkin.length === 0) {
        return;   // 无桌宠——皮肤空 / 无匹配：不加载不调度，容器保持隐藏（A251）
    }
    if (chatPetLoading === true) {
        return;   // 加载门禁——未就绪不调度（保持 sseErr 加载态）
    }
    if (chatPetOffline) {
        chatPetEnterOffline();
        return;
    }
    var st = (appState && appState.runState) ? appState.runState : '';
    if (st !== '' && chatPetPhaseMap[st] !== undefined) {
        chatPetEnterRun(st);
        return;
    }
    chatPetEnterIdle();
}

/// 断线置位 / 恢复——main.js 的 EventSource onerror / onopen 调用
function chatPetSetOffline(on) {
    chatPetOffline = (on === true);
    chatPetSync();
}

/// 当前皮肤的素材清单项——清单为多套结构 {skin:{name:{h,ms}}}（A245；皮肤空 = 无项——A251）
function chatPetManifestItem(name) {
    var skinData = chatPetManifest[chatPetSkin];
    if (skinData === undefined || skinData === null) {
        return undefined;
    }
    return skinData[name];
}

/// 素材 URL——按皮肤分目录 + 内容哈希版本（改图 / 换皮肤即换 URL，缓存自动失效且只重下变动那张）
/// A362——资产基址：页面从 console 端口取资产时由服务端注入 window.__ASSET_BASE__（同源时为空串，行为不变）
function chatPetAssetBase() {
    if (typeof window !== 'undefined' && window.__ASSET_BASE__) {
        return window.__ASSET_BASE__;
    }
    return '';
}

/// 缓存键归一（A362）——带资产基址时素材 URL 为绝对形态（http://host:port/pet/…），
/// 桶内比对一律取 /pet/ 之后的相对段：否则剪枝把每条都当死条目删光，每次启动重下 14.5 MB
function chatPetUrlTail(url) {
    var k = url.indexOf('/pet/');
    if (k < 0) {
        return url;
    }
    return url.substring(k + 1);
}

function chatPetUrl(name) {
    var base = chatPetAssetBase() + 'pet/' + chatPetSkin + '/' + name + '.webp';
    var m = chatPetManifestItem(name);
    if (m === undefined || m.h === undefined) {
        return base;
    }
    return base + '?v=' + m.h;
}

/// 素材播放时长——单一真相在 manifest（素材重转即跟随，前端不留漂移常量）
function chatPetMs(name, fallback) {
    var m = chatPetManifestItem(name);
    if (m === undefined || m.ms === undefined || m.ms <= 0) {
        return fallback;
    }
    return m.ms;
}

/// 换图用 URL——持有表命中用 blob（宿主不可达时仍可切图），未就绪回落网络路径
function chatPetSheet(name) {
    var url = chatPetRes[name];
    if (url === undefined) {
        return chatPetUrl(name);
    }
    return url;
}

/// 加载序——首图 loop-sseErr（加载态常显），其余按清单顺序
function chatPetLoadOrder() {
    var names = ['loop-sseErr'];
    for (var i = 0; i < chatPetFiles.length; i = i + 1) {
        if (chatPetFiles[i] !== 'loop-sseErr') {
            names.push(chatPetFiles[i]);
        }
    }
    return names;
}

/// 桶内清理——删掉不在当前清单里的条目（素材改名 / 删除后不留死条目）
function chatPetPruneEntries(cache, names) {
    if (cache === null || cache === undefined) {
        return Promise.resolve(0);
    }
    var want = {};
    for (var i = 0; i < names.length; i = i + 1) {
        want[chatPetUrlTail(chatPetUrl(names[i]))] = true;
    }
    return cache.keys().then(function (reqs) {
        var jobs = [];
        for (var j = 0; j < reqs.length; j = j + 1) {
            var k = reqs[j].url.indexOf('/pet/');
            if (k < 0) {
                continue;
            }
            if (want[reqs[j].url.substring(k + 1)] !== true) {
                jobs.push(cache.delete(reqs[j]));
            }
        }
        return Promise.all(jobs).then(function () {
            return jobs.length;
        });
    }).catch(function () {
        return 0;
    });
}

/// 缓存桶——固定名；无 Cache API 环境降级 null（每次走网络）
function chatPetOpenCache() {
    if (typeof caches === 'undefined' || caches === null) {
        return Promise.resolve(null);
    }
    return caches.open(CHAT_PET_CACHE_PREFIX).catch(function () {
        return null;
    });
}

/// 网络取回 + 回写缓存——非 2xx 不写（避免把 404 固化）；写缓存失败不致命
function chatPetFetch(url, cache) {
    return fetch(url).then(function (r) {
        if (r.ok === true && cache !== null && cache !== undefined) {
            try {
                cache.put(url, r.clone()).catch(function () {
                });
            } catch (e) {
            }
        }
        return r;
    });
}

/// 单张素材——缓存命中直取（零网络）；未命中走网络
function chatPetLoadOne(name, cache) {
    var url = chatPetUrl(name);
    var get = null;
    if (cache !== null && cache !== undefined) {
        get = cache.match(url).then(function (hit) {
            if (hit === undefined || hit === null) {
                return chatPetFetch(url, cache);
            }
            return hit;
        });
    } else {
        get = chatPetFetch(url, cache);
    }
    return get.then(function (r) {
        if (r.ok !== true) {
            throw new Error('HTTP ' + r.status);
        }
        return r.blob();
    }).then(function (b) {
        chatPetRes[name] = URL.createObjectURL(b);
        return name;
    });
}

/// 首图就位后的启动——桌宠显形并常显 sseErr 作加载态；首图失败则保持不可见
function chatPetBoot() {
    if (chatPetBooted === true) {
        return;
    }
    chatPetBooted = true;
    chatPetSetVisible(true);   // 加载态起即显形（皮肤非空才走得到这里——A251）
    chatPetMode = 'loading';
    chatPetKey = 'loading';
    chatPetKeyAt = Date.now();
    if (chatPetRes['loop-sseErr'] !== undefined) {
        chatPetShow('loop-sseErr');
    }
}

/// 全量结束——解除门禁并开放调度；有失败则汇总告警（具名不静默）
function chatPetLoadDone() {
    chatPetLoading = false;
    if (chatPetFail > 0) {
        warn('桌宠素材：加载失败 ' + chatPetFail + ' 张——相关态回落网络路径');
    }
    chatPetSync();
}

/// 串行逐张——前后不并发（限带宽 / 高延迟场景不拥塞）；失败继续下一张
function chatPetLoadSeq(names, i, cache) {
    if (i >= names.length) {
        chatPetLoadDone();
        return;
    }
    chatPetLoadOne(names[i], cache)
        .then(function () {
            if (i === 0) {
                chatPetBoot();
            }
            chatPetLoadSeq(names, i + 1, cache);
        })
        .catch(function (e) {
            chatPetFail = chatPetFail + 1;
            warn('桌宠素材 ' + names[i] + '：加载失败', e);
            if (i === 0) {
                chatPetBoot();
            }
            chatPetLoadSeq(names, i + 1, cache);
        });
}

/// 加载总入口——无加载通道（非浏览器环境）直接解除门禁
function chatPetLoadAll() {
    if (typeof fetch !== 'function') {
        chatPetLoading = false;
        return;
    }
    if (typeof URL.createObjectURL !== 'function') {
        chatPetLoading = false;
        return;
    }
    var names = chatPetLoadOrder();
    chatPetOpenCache().then(function (cache) {
        chatPetPruneEntries(cache, names);
        chatPetLoadSeq(names, 0, cache);
    });
}

/// 初始化——容器缺失时静默退出（防御式，不影响对话主功能）
function chatPetInit() {
    chatPetImgs = [document.getElementById('chatPetImgA'), document.getElementById('chatPetImgB')];
    var box = document.getElementById('chatPet');
    if (chatPetImgs[0] === null || chatPetImgs[1] === null || box === null) {
        return;
    }
    chatPetSetVisible(false);   // 皮肤确定前不显示——空 / 无匹配即无桌宠（A251）
    box.addEventListener('mouseenter', function () {
        chatPetHovered = true;
        chatPetIdleHook(true);
    });
    box.addEventListener('mouseleave', function () {
        chatPetHovered = false;
        chatPetIdleHook(false);
    });
    box.addEventListener('click', function () {
        if (chatPetDragMoved) {
            return;   // 拖动收尾的 click 不算摸头
        }
        chatPetIdleCheck();
    });
    box.addEventListener('mousedown', chatPetDragStart);
    document.addEventListener('mousemove', chatPetDragMove);
    document.addEventListener('mouseup', chatPetDragEnd);
    box.addEventListener('contextmenu', function (e) {
        e.preventDefault();
        chatPetMenuToggle();
    });
    // A245/A251：皮肤待 state 段 pet.kind 确定后加载（首帧到达即触发）——空 / 无匹配即无桌宠，
    //          不做超时兜底（原先「超时回落默认皮肤」已退役）
}

/// 按下——记录抓取偏移；right/bottom 定位换算为 left/top（此后以左上角定位）
function chatPetDragStart(e) {
    if (chatPetLoading === true) {
        return;   // 加载门禁——未启动前不可拖
    }
    if (e && e.button !== 0) {
        return;   // 仅左键拖拽——右键归菜单（A179）
    }
    chatPetMenuClose();
    var box = document.getElementById('chatPet');
    if (box === null) {
        return;
    }
    var r = box.getBoundingClientRect();
    chatPetDragMoved = false;
    chatPetDrag = { dx: e.clientX - r.left, dy: e.clientY - r.top, x0: e.clientX, y0: e.clientY };
    box.style.left = r.left + 'px';
    box.style.top = r.top + 'px';
    box.style.right = 'auto';
    box.style.bottom = 'auto';
    e.preventDefault();
}

/// 拖动——位移超阈值才算拖动（避免手抖吃掉摸头点击）；限制在视口内
function chatPetDragMove(e) {
    if (chatPetDrag === null) {
        return;
    }
    var box = document.getElementById('chatPet');
    if (box === null) {
        return;
    }
    var moved = Math.abs(e.clientX - chatPetDrag.x0) + Math.abs(e.clientY - chatPetDrag.y0);
    if (chatPetDragMoved === false && moved < 4) {
        return;
    }
    chatPetDragMoved = true;
    var x = e.clientX - chatPetDrag.dx;
    var y = e.clientY - chatPetDrag.dy;
    var maxX = window.innerWidth - box.offsetWidth;
    var maxY = window.innerHeight - box.offsetHeight;
    if (x < 0) {
        x = 0;
    }
    if (y < 0) {
        y = 0;
    }
    if (x > maxX) {
        x = maxX;
    }
    if (y > maxY) {
        y = maxY;
    }
    box.style.left = x + 'px';
    box.style.top = y + 'px';
}

/// 释放——结束拖动；位置换算回 right/bottom 定位（拖动期用 left/top；就地保留会在窗口缩小时把桌宠挤出视口）
function chatPetDragEnd() {
    var box = document.getElementById('chatPet');
    if (box !== null && chatPetDragMoved === true) {
        var r = box.getBoundingClientRect();
        box.style.left = 'auto';
        box.style.top = 'auto';
        box.style.right = Math.max(0, window.innerWidth - r.right) + 'px';
        box.style.bottom = Math.max(0, window.innerHeight - r.bottom) + 'px';
    }
    chatPetDrag = null;
}

// ── 通知面（桌宠气泡 · A179 收尾）──────────────
var chatPetSayEl = null;      // 气泡元素（首次说话时创建——单例）
var chatPetSayTimer = null;   // TTL 定时器（新消息重置）
var CHAT_PET_SAY_MS = 5000;   // 气泡驻留时长（ms）

/// 前端通知输出——桌宠「说话」气泡（挂桌宠下方 · TTL 自动消失）；无桌宠（容器缺失 / 已隐藏）落控制台保底（A251）
/// 归位（A179 收尾）：前端提示 / 告警不再写顶栏信息位——那是 state 段的后端信息输出口
function chatPetSay(msg) {
    if (!msg) {
        return;
    }
    var box = document.getElementById('chatPet');
    if (box === null || box.style.display === 'none') {
        if (typeof console !== 'undefined' && console.log) {
            console.log('[chat] ' + msg);
        }
        return;
    }
    if (chatPetSayEl === null) {
        chatPetSayEl = document.createElement('div');
        chatPetSayEl.className = 'chat-pet-say';
        chatPetSayEl.style.display = 'none';
        box.appendChild(chatPetSayEl);
    }
    chatPetSayEl.textContent = msg;
    chatPetSayEl.style.display = 'block';
    if (chatPetSayTimer !== null) {
        clearTimeout(chatPetSayTimer);
    }
    chatPetSayTimer = setTimeout(function () {
        chatPetSayTimer = null;
        if (chatPetSayEl !== null) {
            chatPetSayEl.style.display = 'none';
        }
    }, CHAT_PET_SAY_MS);
}

/// 硬保底——清空素材缓存桶（改图不改名 / 缓存脏数据时的主动恢复手段）
function chatPetClearCache() {
    if (typeof caches === 'undefined' || caches === null) {
        chatPetSay('本环境不支持缓存');
        return;
    }
    caches.keys().then(function (keys) {
        var jobs = [];
        for (var i = 0; i < keys.length; i = i + 1) {
            if (keys[i].indexOf(CHAT_PET_CACHE_PREFIX) === 0) {
                jobs.push(caches.delete(keys[i]));
            }
        }
        return Promise.all(jobs).then(function () {
            return jobs.length;
        });
    }).then(function (n) {
        chatPetSay('素材缓存已清理（' + n + ' 桶）——刷新后重新加载');
    }).catch(function (e) {
        warn('桌宠缓存清理失败', e);
    });
}

// ── 右键菜单（A179 归位——「清理缓存」随桌宠本体，不再挂顶栏）──────────
var chatPetMenuEl = null;   // 菜单元素（首次打开时创建——单例）

/// 创建菜单——挂桌宠容器内（随桌宠定位）；件内指针事件不冒泡（按下不触发拖拽、点击不当摸头）
function chatPetMenuEnsure() {
    if (chatPetMenuEl !== null) {
        return chatPetMenuEl;
    }
    var box = document.getElementById('chatPet');
    if (box === null) {
        return null;
    }
    var menu = document.createElement('div');
    menu.className = 'chat-pet-menu';
    menu.style.display = 'none';
    menu.addEventListener('mousedown', function (e) {
        e.stopPropagation();
    });
    menu.addEventListener('click', function (e) {
        e.stopPropagation();
    });
    menu.addEventListener('contextmenu', function (e) {
        e.stopPropagation();
        e.preventDefault();
    });
    var btn = document.createElement('button');
    btn.id = 'chatPetCacheClear';
    btn.textContent = '清理缓存';
    btn.title = '清空桌宠素材缓存桶——改图不改名 / 缓存脏数据时用；下次刷新重新下载';
    btn.addEventListener('click', function () {
        chatPetMenuClose();
        chatPetClearCache();
    });
    menu.appendChild(btn);
    box.appendChild(menu);
    chatPetMenuEl = menu;
    return menu;
}

/// 打开菜单——贴近桌宠：上方空间不足（贴顶）时改挂下方
function chatPetMenuOpen() {
    var menu = chatPetMenuEnsure();
    var box = document.getElementById('chatPet');
    if (menu === null || box === null) {
        return;
    }
    var above = box.getBoundingClientRect().top >= 44;
    menu.className = 'chat-pet-menu ' + (above ? 'up' : 'down');
    menu.style.display = 'block';
}

/// 关闭菜单
function chatPetMenuClose() {
    if (chatPetMenuEl !== null) {
        chatPetMenuEl.style.display = 'none';
    }
}

/// 开关——右键点击同一入口（开着即关，再右键消失）
function chatPetMenuToggle() {
    if (chatPetMenuEl !== null && chatPetMenuEl.style.display !== 'none') {
        chatPetMenuClose();
        return;
    }
    chatPetMenuOpen();
}
