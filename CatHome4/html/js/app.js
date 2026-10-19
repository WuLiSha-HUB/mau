// CH4 外观层 v2——app.js：全局状态 + SSE 连接 + 快照/patch + 状态区 + 日志区 + 指令 + 配置区（2026-08-25 拆分自 index.html）
// 加载顺序：app.js 最先（全局变量与公共函数在此定义；chat.js/panel.js 依赖本文件）

// [段1] 全局状态与 DOM 引用
var metaEl = document.getElementById('meta');
var catsEl = document.getElementById('cats');
var oaEl = document.getElementById('oa');
var boxesEl = document.getElementById('boxes');
var sessionsEl = document.getElementById('sessions');
var toolregEl = document.getElementById('toolreg');
var boxFilterEl = document.getElementById('boxFilter');
var boxCountEl = document.getElementById('boxCount');
boxFilterEl.addEventListener('input', function () { renderBoxFiltered(); });
var logEvents = document.getElementById('logevents');
var logFilterEl = document.getElementById('logFilter');
var rawJsonEl = document.getElementById('rawJson');
var rawInfoEl = document.getElementById('rawInfo');
var rawPauseBtn = document.getElementById('rawPause');
var cmdResultEl = document.getElementById('cmdResult');
var rawPaused = false;
var lastFrame = -1;          // 快照帧号守卫——旧快照不覆盖新快照
var fullSnapshot = null;     // 本地全快照——增量流式接收层维护（各页面渲染唯一数据源）
var logs = [];               // 内存日志数组（Log 区真相源）
var logDedupeKey = '';       // 已渲染最后一条日志的 key——拉取合并去重（同帧精确比对）
var logDedupeFrame = -1;     // 已渲染最后一条日志的帧号——拉取合并去重（数值单调比较，不受字典序影响）

// [段2] 侧栏页签切换
var tabs = document.querySelectorAll('#sidebar button');
for (var i = 0; i < tabs.length; i++) {
    tabs[i].addEventListener('click', function () {
        for (var j = 0; j < tabs.length; j++) { tabs[j].classList.remove('active'); }
        this.classList.add('active');
        var target = this.getAttribute('data-tab');
        var sections = document.querySelectorAll('.tab');
        for (var k = 0; k < sections.length; k++) { sections[k].classList.remove('active'); }
        document.getElementById('tab-' + target).classList.add('active');
    });
}

// [段3] SSE 事件流——四类事件分派
// A141——按订阅推送：面板只订阅四类（对话页另订阅 view/note——管理面事件不入 chat 流，A181）
var es = new EventSource('/api/v1/stream?topics=snapshot,patch,log,cmd,state');
es.addEventListener('snapshot', function (ev) {
    // 增量流式——全量快照到达（helloFrame 重连兜底）整体替换本地全快照
    var s = JSON.parse(ev.data);
    fullSnapshot = s;
    applySnapshot(s);
});
es.addEventListener('patch', function (ev) { applyPatch(JSON.parse(ev.data)); });   // 增量流式——变化段合并进本地全快照
es.addEventListener('state', function (ev) {
    // A225c——猫卡实时状态（后端多猫列表变化即推；管理页猫卡自动跟随，不再靠手动刷新）
    var d = JSON.parse(ev.data);
    if (typeof applyCatState === 'function') { applyCatState(d.cats || []); }
});
es.addEventListener('log', function (ev) {
    // A141——服务端批量合帧：载荷为数组（兼容单条形态）
    var d = JSON.parse(ev.data);
    if (Object.prototype.toString.call(d) === '[object Array]') {
        for (var i = 0; i < d.length; i++) { pushLog(d[i]); }
    } else {
        pushLog(d);
    }
});
es.addEventListener('cmd', function (ev) {
    var d = JSON.parse(ev.data);
    pushLog({ time: '', frame: d.frame, level: d.ok ? 'INFO' : 'ERROR', category: 'CMD', module: d.cmdId, message: d.ok ? 'ok' : ('err=' + (d.error || '')) });
    cmdResultEl.textContent = '回执: ' + d.cmdId + ' ok=' + d.ok + ' frame=' + d.frame + (d.error ? ' error=' + d.error : '');
});
es.onerror = function () { metaEl.textContent = 'SSE 断线——自动重连...'; };
es.onopen = function () {
    // 对话已迁 chat.html 独立页（F2.1 主面板纯管理面）——重连无需对话历史兜底（A162：落差检测已退役）
};

// 初始兜底——帧号守卫（旧快照不覆盖新）
fetch('/api/v1/snapshot?logs=200')
    .then(function (r) { return r.json(); })
    .then(function (s) {
        for (var i = 0; i < s.logs.length; i++) { unshiftLog(s.logs[i]); }
        if (s.frame >= lastFrame) { fullSnapshot = s; applySnapshot(s); lastFrame = s.frame; }
        refreshLogView();
    })
    .catch(function (e) { uiWarn('初始快照', e); });

// [段3b] patch 合并——变化段合并进本地全快照（增量流式：接收层处理，页面渲染逻辑不变）
function applyPatch(p) {
    if (!fullSnapshot) { return; }                    // 全量未到——忽略（helloFrame 会来）
    if (p.frame <= fullSnapshot.frame) { return; }    // 过期帧忽略
    if (p.cats) { fullSnapshot.cats = p.cats; }
    if (p.sessions) { fullSnapshot.sessions = p.sessions; }
    if (p.oa) { fullSnapshot.oa = p.oa; }
    if (p.deps) { fullSnapshot.deps = p.deps; }
    if (p.res) { fullSnapshot.res = p.res; }
    if (p.boxes) {
        var map = {};
        var cur = fullSnapshot.boxes || [];
        for (var i = 0; i < cur.length; i++) {
            map[cur[i].scope + '\u0001' + cur[i].key] = cur[i];
        }
        var set = p.boxes.set || [];
        for (var j = 0; j < set.length; j++) {
            map[set[j].scope + '\u0001' + set[j].key] = set[j];
        }
        var del = p.boxes.del || [];
        for (var k = 0; k < del.length; k++) {
            delete map[del[k].scope + '\u0001' + del[k].key];
        }
        var arr = [];
        for (var mk in map) {
            if (Object.prototype.hasOwnProperty.call(map, mk)) { arr.push(map[mk]); }
        }
        fullSnapshot.boxes = arr;
    }
    fullSnapshot.frame = p.frame;
    applySnapshot(fullSnapshot);
}

// [段4] 快照应用——状态区 + 原始区（同源渲染）
function applySnapshot(s) {
    if (s.frame < lastFrame) { return; }   // 帧号守卫
    lastFrame = s.frame;
    metaEl.textContent = 'pid=' + s.pid + ' frame=' + s.frame + ' version=' + s.version;
    renderCats(s.cats);
    renderSessions(s.sessions || []);
    renderTools(s.tools || []);
    oaEl.textContent = 'Open=' + s.oa.open + ' Work=' + s.oa.work + ' Closed=' + s.oa.closed + ' Timeout=' + s.oa.timeout;
    renderBoxes(s.boxes || []);
    if (typeof renderDeps === 'function') { renderDeps(s.deps || []); }
    if (typeof renderRes === 'function') { renderRes(s.res || null); }
    if (typeof renderBoot === 'function') { renderBoot(s.boot || null); }
    if (!rawPaused) {
        rawJsonEl.textContent = JSON.stringify(s, null, 2);
        rawInfoEl.textContent = 'frame=' + s.frame + ' · ' + s.cats.length + ' cats · ' + (s.boxes ? s.boxes.length : 0) + ' boxes';
    }
}

// [段5] Cat 卡片渲染——DOM API + textContent（安全铁律）；全字段消费：stateLines/sensors/slots/wires
function renderCats(cats) {
    catsEl.textContent = '';
    for (var i = 0; i < cats.length; i++) {
        var c = cats[i];
        var card = document.createElement('div');
        card.className = 'cat-card';
        var head = document.createElement('div');
        head.className = 'cat-head';
        var name = document.createElement('span');
        name.className = 'cat-name';
        name.textContent = c.name;
        var id = document.createElement('span');
        id.className = 'cat-id';
        id.textContent = '#' + c.id;
        head.appendChild(name);
        head.appendChild(id);
        if (c.type) {
            var type = document.createElement('span');
            type.className = 'cat-type';
            type.textContent = c.type + (c.kind ? ' · ' + c.kind : '');
            head.appendChild(type);
        }
        card.appendChild(head);
        if (c.faulted) {
            var fault = document.createElement('div');
            fault.className = 'cat-faulted';
            fault.textContent = 'FAULTED: ' + c.faultReason;
            card.appendChild(fault);
        } else {
            var st = c.status || {};
            // 自述行——实体声音（状态前醒目展示——先听它说什么，再看细节；多行自由形态；desc 在 cats[i] 顶层非 status）
            var descs = c.desc || [];
            for (var j = 0; j < descs.length; j++) {
                var d = document.createElement('div');
                d.className = 'cat-desc';
                d.textContent = descs[j];
                card.appendChild(d);
            }
            // 状态机行
            var states = document.createElement('div');
            states.className = 'cat-states';
            states.textContent = (st.stateLines || []).join('  ');
            card.appendChild(states);
            // 传感器行——信号真相（bool 着色）
            var sens = (st.sensors || []).map(function (x) {
                var span = document.createElement('span');
                span.className = (x.value === true || x.value === 'true') ? 'sen-on' : 'sen-off';
                span.textContent = (x.value === true || x.value === 'true') ? '● ' : '○ ';
                span.appendChild(document.createTextNode(x.name + '=' + String(x.value)));
                return span;
            });
            appendRow(card, 'sensors', sens);
            // 槽位行
            appendRow(card, 'slots', (st.slots || []).map(function (x) { return x.name + ' ' + x.available + '/' + x.capacity; }));
            // 导线行——busy/timedOut/lastTriggerFrame 全展示
            var wires = (st.wires || []).map(function (w) {
                var span = document.createElement('span');
                span.className = w.timedOut ? 'wire-timeout' : (w.busy ? 'wire-busy' : '');
                span.textContent = w.name + (w.busy ? '(busy)' : '') + (w.timedOut ? '(timeout)' : '') + '@' + w.lastTriggerFrame;
                return span;
            });
            appendRow(card, 'wires', wires);
        }
        catsEl.appendChild(card);
    }
}
function appendRow(card, label, spans) {
    if (spans.length === 0) { return; }
    var row = document.createElement('div');
    row.className = 'cat-row';
    var k = document.createElement('span');
    k.className = 'k';
    k.textContent = label + ': ';
    row.appendChild(k);
    for (var i = 0; i < spans.length; i++) {
        if (i > 0) { row.appendChild(document.createTextNode(' · ')); }
        // 条目兼容——DOM 元素（sensors/wires 着色 span）与纯文本（slots 字符串数组）
        var item = spans[i];
        if (typeof item === 'string') { row.appendChild(document.createTextNode(item)); }
        else { row.appendChild(item); }
    }
    card.appendChild(row);
}

// [段6] 盒子渲染——分组折叠 + 类型着色 + 过滤（外观层重构；textContent 防 XSS）
var boxGroupsCache = { groups: {}, order: [] };

function renderBoxes(boxes) {
    // FlowId → Flow 名映射（cats 段——数字 scope 显示为 Flow 名）
    var flowMap = {};
    if (fullSnapshot && fullSnapshot.cats) {
        for (var i = 0; i < fullSnapshot.cats.length; i++) {
            flowMap[String(fullSnapshot.cats[i].id)] = fullSnapshot.cats[i].name;
        }
    }
    // 按 scope 分组——global 优先；数字 scope 映射 Flow 名；其余原样
    var groups = {};
    var order = [];
    for (var j = 0; j < boxes.length; j++) {
        var b = boxes[j];
        var gName = b.scope;
        // 数字 scope → Flow 名（global / 未命中 scope 原样显示）
        if (flowMap[gName]) {
            gName = flowMap[gName];
        }
        if (!groups[gName]) {
            groups[gName] = [];
            order.push(gName);
        }
        groups[gName].push(b);
    }
    boxGroupsCache = { groups: groups, order: order };
    renderBoxFiltered();
}

// 盒子过滤渲染——按 #boxFilter 输入过滤（key/scope 包含匹配）+ 组折叠
function renderBoxFiltered() {
    var filter = boxFilterEl.value;
    boxesEl.textContent = '';
    var total = 0;
    var groups = boxGroupsCache.groups || {};
    var order = boxGroupsCache.order || [];
    for (var i = 0; i < order.length; i++) {
        var gName = order[i];
        var items = groups[gName];
        var kept = [];
        for (var k = 0; k < items.length; k++) {
            if (filter === '' || (items[k].scope + '.' + items[k].key).indexOf(filter) >= 0) {
                kept.push(items[k]);
            }
        }
        if (kept.length === 0) {
            continue;
        }
        total = total + kept.length;
        var head = document.createElement('div');
        head.className = 'box-group-head';
        head.textContent = '▾ ' + gName + '（' + kept.length + '）';
        head._open = true;
        head._gname = gName;
        head._kept = kept;
        var body = document.createElement('div');
        body.className = 'box-group-body';
        for (var m = 0; m < kept.length; m++) {
            body.appendChild(buildBoxItem(kept[m]));
        }
        head.addEventListener('click', function () {
            this._open = !this._open;
            this.textContent = (this._open ? '▾ ' : '▸ ') + this._gname + '（' + this._kept.length + '）';
            var b = this.nextSibling;
            if (b) {
                b.style.display = this._open ? '' : 'none';
            }
        });
        boxesEl.appendChild(head);
        boxesEl.appendChild(body);
    }
    boxCountEl.textContent = '共 ' + total + ' 条';
}

// 单盒子条目——类型着色（b bool ●○ / n number / s string / o object）
function buildBoxItem(b) {
    var row = document.createElement('div');
    row.className = 'box-item';
    if (b.t === 'b') {
        var dot = document.createElement('span');
        dot.className = (b.value === true) ? 'box-bool-on' : 'box-bool-off';
        dot.textContent = (b.value === true) ? '● ' : '○ ';
        row.appendChild(dot);
    }
    var key = document.createElement('span');
    key.className = 'box-key';
    key.textContent = b.scope + '.' + b.key + ' = ';
    var val = document.createElement('span');
    val.className = 'box-val-' + b.t;
    val.textContent = String(b.value);
    row.appendChild(key);
    row.appendChild(val);
    return row;
}

// [段6b] 会话状态渲染——Majordomo + 多猫四相环预览（快照 sessions 段）
// 距今文本——绝对毫秒戳 → 人读时长（刚刚 / N 分钟前 / N 小时前 / N 天前）；0/缺省 → 空串（不显示）
function fmtAgo(ms) {
    if (!ms || ms <= 0) { return ''; }
    var delta = Date.now() - ms;
    if (delta < 0) { delta = 0; }
    var sec = Math.floor(delta / 1000);
    if (sec < 60) { return '刚刚'; }
    var min = Math.floor(sec / 60);
    if (min < 60) { return min + ' 分钟前'; }
    var hour = Math.floor(min / 60);
    if (hour < 24) { return hour + ' 小时前'; }
    return Math.floor(hour / 24) + ' 天前';
}

function renderSessions(sessions) {
    sessionsEl.textContent = '';
    if (!sessions || sessions.length === 0) {
        var empty = document.createElement('div');
        empty.className = 'session-empty';
        empty.textContent = '无活跃会话';
        sessionsEl.appendChild(empty);
        return;
    }
    for (var i = 0; i < sessions.length; i++) {
        var s = sessions[i];
        var card = document.createElement('div');
        card.className = 'session-card';
        var head = document.createElement('div');
        head.className = 'session-head';
        var name = document.createElement('span');
        name.className = 'session-name';
        name.textContent = s.name;
        head.appendChild(name);
        var badge = document.createElement('span');
        badge.className = 'session-badge ph-' + (s.phase || 'Idle');
        badge.textContent = s.phase || 'Idle';
        head.appendChild(badge);
        if (s.noteActive) {
            var noteTag = document.createElement('span');
            noteTag.className = 'session-note';
            noteTag.textContent = '📋 Note';
            head.appendChild(noteTag);
        }
        card.appendChild(head);
        var info = document.createElement('div');
        info.className = 'session-info';
        info.textContent = '轮次 ' + s.round + ' · 消息 ' + s.msgCount + ' · 待处理 ' + s.pending;
        // 最近前文变动——绝对毫秒戳由后端给（快照 sessions 段），距今文本前端自算
        var ago = fmtAgo(s.lastContextChangeAt);
        if (ago) {
            info.textContent += ' · 前文变动 ' + ago;
        }
        card.appendChild(info);
        sessionsEl.appendChild(card);
    }
}

// [段6c] 工具注册表渲染——按组分类 chips（快照 tools 段）
function renderTools(tools) {
    toolregEl.textContent = '';
    if (!tools || tools.length === 0) {
        return;
    }
    var groups = {};
    var order = [];
    for (var i = 0; i < tools.length; i++) {
        var t = tools[i];
        var gName = t.builtin ? '内置' : (t.group || '其他');
        if (!groups[gName]) {
            groups[gName] = [];
            order.push(gName);
        }
        groups[gName].push(t.name);
    }
    for (var g = 0; g < order.length; g++) {
        var gn = order[g];
        var names = groups[gn];
        var block = document.createElement('div');
        block.className = 'tool-group';
        var head = document.createElement('span');
        head.className = 'tool-group-name';
        head.textContent = gn + '（' + names.length + '）';
        block.appendChild(head);
        for (var n = 0; n < names.length; n++) {
            var chip = document.createElement('span');
            chip.className = 'tool-chip';
            chip.textContent = names[n];
            block.appendChild(chip);
        }
        toolregEl.appendChild(block);
    }
}

// [段7] Log 流——内存数组 + 筛选 + 去重
// 日志批量渲染（A143）——原实现每条日志一次 DOM 追加 + 一次强制滚动（日志爆发时拖垮主线程）；
// 合并到一帧一批（rAF 不可用时同步执行，行为保真）
var logPending = [];
var logFlushRaf = 0;

function pushLog(e) {
    logs.push({ time: e.time, frame: e.frame, level: e.level, category: e.category, module: e.module, message: e.message });
    if (logs.length > 3000) { logs.shift(); }
    logPending.push(logs[logs.length - 1]);
    scheduleLogFlush();
}

function scheduleLogFlush() {
    if (typeof requestAnimationFrame !== 'function') { flushLogDom(); return; }
    if (logFlushRaf !== 0) { return; }
    logFlushRaf = requestAnimationFrame(function () {
        logFlushRaf = 0;
        flushLogDom();
    });
}

function flushLogDom() {
    if (logPending.length === 0) { return; }
    var batch = logPending;
    logPending = [];
    var frag = document.createDocumentFragment();
    for (var i = 0; i < batch.length; i++) {
        var div = document.createElement('div');
        div.className = logClass(batch[i]);
        div.textContent = logText(batch[i]);
        frag.appendChild(div);
        applyFilterToEl(div);
    }
    logEvents.appendChild(frag);
    while (logEvents.children.length > 2000) { logEvents.removeChild(logEvents.firstChild); }
    logEvents.scrollTop = logEvents.scrollHeight;
}
function unshiftLog(e) {
    // 拉取合并去重——帧号数值单调比较（旧实现用字符串字典序，帧号位数变化时判定失真）
    var f = (typeof e.frame === 'number') ? e.frame : (parseInt(e.frame, 10) || 0);
    if (logDedupeFrame >= 0 && f < logDedupeFrame) { return; }
    var key = e.time + '|' + e.frame + '|' + e.message;
    if (f === logDedupeFrame && key === logDedupeKey) { return; }
    logDedupeFrame = f;
    logDedupeKey = key;
    logs.push(e);
}
function logText(e) {
    var t = e.time ? e.time + ' ' : '';
    return t + (e.frame ? 'F' + e.frame + ' ' : '') + e.level + ' | ' + (e.category || '-') + ' | ' + (e.module || '') + (e.message ? ' ' + e.message : '');
}
function logClass(e) {
    if (e.level === 'ERROR') { return 'lg lg-error'; }
    if (e.level === 'WARN') { return 'lg lg-warn'; }
    if (e.category === 'CMD') { return 'lg lg-cmd'; }
    if (e.category === 'LLM') { return 'lg lg-llm'; }
    return 'lg';
}

// [段8] 筛选——level=X / cat=X / 裸词=关键词（重新渲染）
logFilterEl.addEventListener('input', refreshLogView);
function refreshLogView() {
    logEvents.textContent = '';
    // 与 appendLogDom 的 DOM 上限一致——只渲染最近 2000 条（防重建后 DOM 膨胀到内存上限 3000）
    var start = logs.length > 2000 ? logs.length - 2000 : 0;
    for (var i = start; i < logs.length; i++) {
        var div = document.createElement('div');
        div.className = logClass(logs[i]);
        div.textContent = logText(logs[i]);
        logEvents.appendChild(div);
    }
    var filter = logFilterEl.value.trim();
    if (filter.length > 0) { applyFilterAll(filter); }
    logEvents.scrollTop = logEvents.scrollHeight;
}
function applyFilterAll(filter) {
    var tokens = filter.split(/\s+/);
    var children = logEvents.children;
    for (var i = 0; i < children.length; i++) {
        var el = children[i];
        var text = el.textContent;
        var pass = true;
        for (var j = 0; j < tokens.length; j++) {
            var t = tokens[j];
            if (t.indexOf('level=') === 0) {
                if (text.indexOf('| ' + t.substring(6).toUpperCase() + ' |') < 0) { pass = false; break; }
            } else if (t.indexOf('cat=') === 0) {
                if (text.indexOf('| ' + t.substring(4).toUpperCase() + ' |') < 0) { pass = false; break; }
            } else {
                if (text.indexOf(t) < 0) { pass = false; break; }
            }
        }
        el.style.display = pass ? '' : 'none';
    }
}
function applyFilterToEl(el) {
    var filter = logFilterEl.value.trim();
    if (filter.length === 0) { return; }
    var tokens = filter.split(/\s+/);
    var text = el.textContent;
    for (var j = 0; j < tokens.length; j++) {
        var t = tokens[j];
        if (t.indexOf('level=') === 0) {
            if (text.indexOf('| ' + t.substring(6).toUpperCase() + ' |') < 0) { el.style.display = 'none'; return; }
        } else if (t.indexOf('cat=') === 0) {
            if (text.indexOf('| ' + t.substring(4).toUpperCase() + ' |') < 0) { el.style.display = 'none'; return; }
        } else {
            if (text.indexOf(t) < 0) { el.style.display = 'none'; return; }
        }
    }
}

// [段9] 拉取 N 条——?logs=N 服务端合成 + 尾部去重合并
document.getElementById('logRefresh').addEventListener('click', function () {
    fetch('/api/v1/snapshot?logs=200')
        .then(function (r) { return r.json(); })
        .then(function (s) {
            logs = [];
            logDedupeKey = '';
            logDedupeFrame = -1;
            for (var i = 0; i < s.logs.length; i++) { unshiftLog(s.logs[i]); }
            refreshLogView();
        })
        .catch(function (e) { uiWarn('日志拉取', e); });
});

// [段10] 原始区暂停/继续
rawPauseBtn.addEventListener('click', function () {
    rawPaused = !rawPaused;
    rawPauseBtn.textContent = rawPaused ? '继续' : '暂停';
    rawPauseBtn.classList.toggle('paused', rawPaused);
    if (!rawPaused) {
        fetch('/api/v1/snapshot')
            .then(function (r) { return r.json(); })
            .then(function (s) { if (s.frame >= lastFrame) { fullSnapshot = s; applySnapshot(s); } })
            .catch(function (e) { uiWarn('原始区快照恢复', e); });
    }
});

// [段11] 指令入口——POST /api/v1/command
function sendCmd() {
    var input = document.getElementById('cmdInput');
    var text = input.value.trim();
    if (text.length === 0) { return; }
    input.value = '';
    fetch('/api/v1/command', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: text })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            cmdResultEl.textContent = '回执: cmdId=' + d.cmdId + ' ok=' + d.ok + ' frame=' + d.frame + (d.error ? ' error=' + d.error : '');
        })
        .catch(function (err) {
            cmdResultEl.textContent = '请求失败: ' + err;
        });
}
// 指令入口按钮绑定（F2.3 内联 onclick 迁出——与 chat.html 规范统一）
document.getElementById('cmdBtn').addEventListener('click', sendCmd);
document.getElementById('cmdInput').addEventListener('keydown', function (e) {
    if (e.key === 'Enter') { sendCmd(); }
});

// [段12] 配置区——GET/POST /api/v1/config（v3 配置区；敏感键服务端掩码，POST 拒绝掩码回写）
var configTableBody = document.querySelector('#configTable tbody');
var configMsg = document.getElementById('configMsg');
function loadConfig() {
    fetch('/api/v1/config')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            var items = d.items || [];
            renderConfig(items);
            applyUiConfig(items);
        })
        .catch(function (e) { uiWarn('配置加载', e); });
}
function renderConfig(items) {
    configTableBody.textContent = '';
    for (var i = 0; i < items.length; i++) {
        var it = items[i];
        var tr = document.createElement('tr');
        var td1 = document.createElement('td');
        td1.className = 'box-key';
        td1.textContent = it.key;
        // 项注释——schema desc 单源（后端下发；未声明项无 desc，来源列已标「·未声明」）
        var noteText = it.desc || '';
        if (noteText.length > 0) {
            var noteEl = document.createElement('div');
            noteEl.className = 'cfg-note';
            noteEl.textContent = noteText;
            td1.appendChild(noteEl);
        }
        var td2 = document.createElement('td');
        var inp;
        if (it.key === 'audio.voice') {
            // 音色专用——schema 值域四项渲染下拉（注释来自 ui-common.KOKORO_VOICES；空=服务默认）
            inp = document.createElement('select');
            inp.className = 'input-mini';
            inp.style.width = '100%';
            var emptyOpt = document.createElement('option');
            emptyOpt.value = '';
            emptyOpt.textContent = '（空 = 服务默认音色）';
            inp.appendChild(emptyOpt);
            for (var vi = 0; vi < KOKORO_VOICES.length; vi++) {
                var voiceOpt = document.createElement('option');
                voiceOpt.value = KOKORO_VOICES[vi].id;
                voiceOpt.textContent = kokoroVoiceLabel(KOKORO_VOICES[vi].id);
                inp.appendChild(voiceOpt);
            }
            inp.value = String(it.value || '');
            inp.title = (it.desc || '') + (it.emptyDesc ? '｜' + it.emptyDesc : '');
        } else {
            inp = document.createElement('input');
            inp.value = it.value;
            inp.className = 'input-mini text';
            inp.style.width = '100%';
            // §4.3 描述 + 空值语义进 title（反直觉默认可观测）
            inp.title = (it.desc || '') + (it.emptyDesc ? '｜' + it.emptyDesc : '');
            if (it['default'] !== undefined && it['default'] !== '' && (it.value === undefined || it.value === '')) {
                inp.placeholder = '默认值 ' + it['default'] + '（落盘后生效）';
            }
        }
        td2.appendChild(inp);
        var td3 = document.createElement('td');
        td3.className = 'box-tag';
        // §4.5 未声明键显式标注——落盘存在但 schema 未声明（只读；声明后可见可改）
        td3.textContent = (it.source || '') + (it.declared === false ? '·未声明' : '');
        var td4 = document.createElement('td');
        var btn = document.createElement('button');
        btn.textContent = '保存';
        btn.className = 'btn-mini tight';
        // §4.5 只读项（schema writable=false）——输入框同禁 + 可见标注（避免「可改却点不动」的错觉）
        if (it.writable === false) {
            inp.readOnly = true;
            inp.className = inp.className + ' ro';
            inp.title = '只读项——不可修改（schema writable=false）｜' + (it.desc || '');
            btn.disabled = true;
            btn.title = '只读项（schema writable=false）';
        }
        btn.addEventListener('click', (function (k, input) {
            return function () { saveConfig(k, input.value); };
        })(it.key, inp));
        td4.appendChild(btn);
        tr.appendChild(td1); tr.appendChild(td2); tr.appendChild(td3); tr.appendChild(td4);
        configTableBody.appendChild(tr);
    }
}
function saveConfig(key, value) {
    fetch('/api/v1/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key: key, value: value })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (d.ok) {
                configMsg.textContent = '已保存 ' + key + ' @F' + d.frame + '（即时生效：llm.* 实时读取 / ui.* 前端刷新可见）';
                loadConfig();
            } else {
                configMsg.textContent = '失败: ' + (d.error || '未知错误');
            }
        })
        .catch(function (err) { configMsg.textContent = '请求失败: ' + err; });
}
document.getElementById('configRefresh').addEventListener('click', loadConfig);
// 打开数据目录——调后端端点（浏览器 JS 沙箱无法直接开资源管理器，必须后端 explorer.exe）
var openDataBtn = document.getElementById('openDataDir');
if (openDataBtn) {
    openDataBtn.addEventListener('click', function () {
        fetch('/api/v1/open-data-dir', { method: 'POST' })
            .then(function (r) { return r.json(); })
            .then(function (d) {
                if (d.ok) {
                    configMsg.textContent = '已打开数据目录: ' + d.path;
                } else {
                    configMsg.textContent = '打开失败: ' + (d.error || '未知错误');
                }
            })
            .catch(function (err) { configMsg.textContent = '请求失败: ' + err; });
    });
}
loadConfig();
