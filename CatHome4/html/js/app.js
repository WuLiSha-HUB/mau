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

// [段2] 侧栏页签切换——只取 data-tab 按钮（侧栏「当前用户」项无 data-tab，由 panel-user.js 接管双击）
var tabs = document.querySelectorAll('#sidebar button[data-tab]');
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
        // 会话详情入口——点击打开详情弹层（上一条 user / 回复 + 预设动作）
        card.setAttribute('data-cat', s.id);
        card.setAttribute('data-name', s.name);
        card.title = '点击查看会话详情';
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
        // 前文长度（2026-10-01 体验轮）——快照 sessions 段 context/contextCount；0 也是真实值（新会话 / 未落盘）
        var infoBits = [];
        if (typeof s.context !== 'undefined' && s.context !== null) {
            infoBits.push('前文 ' + s.context + ' tokens' + (s.contextCount ? ' / ' + s.contextCount + ' 条' : ''));
        }
        infoBits.push('轮次 ' + s.round + ' · 消息 ' + s.msgCount + ' · 待处理 ' + s.pending);
        info.textContent = infoBits.join(' · ');
        // 最近前文变动——绝对毫秒戳由后端给（快照 sessions 段），距今文本前端自算
        var ago = fmtAgo(s.lastContextChangeAt);
        if (ago) {
            info.textContent += ' · 前文变动 ' + ago;
        }
        card.appendChild(info);
        sessionsEl.appendChild(card);
    }
}

// [段6d] 会话详情弹层——会话状态卡点击打开（上一条 user / 回复 + 预设动作 /info · /new · 会话留档）
// 数据源：GET /api/v1/cat-detail?cat=<key>（状态字段与 cat.info 同源；本会话无消息时回落 sessions_old 留档末条）
var catDetailModal = document.getElementById('catDetailModal');
var catDetailTitle = document.getElementById('catDetailTitle');
var catDetailBody = document.getElementById('catDetailBody');
var catDetailMsg = document.getElementById('catDetailMsg');
var catNewModal = document.getElementById('catNewModal');
var catNewTitle = document.getElementById('catNewTitle');
var catNewText = document.getElementById('catNewText');
var catNewConfirmWrap = document.getElementById('catNewConfirmWrap');
var catNewConfirm = document.getElementById('catNewConfirm');
var catNewGo = document.getElementById('catNewGo');
var catNewMsg = document.getElementById('catNewMsg');
var catDetailKey = '';
var catDetailData = null;
var catDetailInfoOn = false;
var catDetailArchiveOn = false;
// 弹层动作面 DOM——停止按钮 + 一行输入（发送）/回执（2026-10-01 易用性轮）
var catDetailPauseBtn = document.getElementById('catDetailPause');
var catDetailInput = document.getElementById('catDetailInput');
var catDetailSendBtn = document.getElementById('catDetailSend');
var catDetailSendMsg = document.getElementById('catDetailSendMsg');

// /info 展示字段——与 cat.info 单猫条目同源（BuildCatInfoEntry 产物）
var CAT_DETAIL_FIELDS = ['id', 'name', 'special', 'running', 'port', 'isIdle', 'phase', 'runState', 'requests', 'round', 'msgCount', 'pending', 'noteActive', 'contextCount', 'context', 'lastActiveAt'];

// 会话卡点击——事件委托（卡片由 renderSessions 动态重建，逐卡绑定会随快照重建失效）
sessionsEl.addEventListener('click', function (e) {
    var el = e.target;
    while (el && el !== sessionsEl) {
        if (el.classList && el.classList.contains('session-card')) {
            openCatDetail(el.getAttribute('data-cat'), el.getAttribute('data-name'));
            return;
        }
        el = el.parentNode;
    }
});

function openCatDetail(key, name) {
    catDetailKey = key;
    catDetailTitle.textContent = name || key;
    catDetailMsg.textContent = '';
    catDetailInfoOn = false;
    catDetailArchiveOn = false;
    catDetailData = null;
    catDetailBody.textContent = '读取中…';
    catDetailModal.style.display = 'flex';
    loadCatDetail();
}

function closeCatDetail() {
    catDetailModal.style.display = 'none';
    catDetailKey = '';
    catDetailData = null;
}

function loadCatDetail() {
    if (catDetailKey.length === 0) { return; }
    fetch('/api/v1/cat-detail?cat=' + encodeURIComponent(catDetailKey))
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) { catDetailBody.textContent = '读取失败: ' + (d.error || '未知错误'); return; }
            catDetailData = d;
            renderCatDetail();
        })
        .catch(function (e) { catDetailBody.textContent = '读取失败: ' + e; });
}

// 文本块——标题 + 元信息（source=archive 时标注「上一会话」）+ 正文
function catDetailMsgBlock(title, item, emptyText) {
    var box = document.createElement('div');
    box.className = 'cd-block';
    var head = document.createElement('div');
    head.className = 'cd-block-head';
    head.textContent = title;
    if (item) {
        var tag = document.createElement('span');
        tag.className = 'cd-block-meta';
        if (item.source === 'archive') {
            tag.textContent = '上一会话' + (item.timeText ? ' · ' + item.timeText : '');
        } else {
            tag.textContent = item.time ? fmtAgo(item.time) : '';
        }
        head.appendChild(tag);
    }
    box.appendChild(head);
    var body = document.createElement('div');
    body.className = 'cd-block-body';
    if (item) {
        body.textContent = item.text;
    } else {
        body.textContent = emptyText;
        body.classList.add('cd-empty');
    }
    box.appendChild(body);
    return box;
}

function renderCatDetail() {
    var d = catDetailData;
    catDetailBody.textContent = '';
    var line = document.createElement('div');
    line.className = 'cd-status';
    var bits = [];
    bits.push(d.special ? '主干' : '多猫');
    bits.push(d.running ? '运行中' : '静默');
    // 前文长度（2026-10-01 体验轮）——与 cat.info / 快照 sessions 段同源；端口让位给刷新左边的可点入口
    if (typeof d.context !== 'undefined' && d.context !== null) {
        bits.push('前文 ' + d.context + ' tokens' + (d.contextCount ? ' / ' + d.contextCount + ' 条' : ''));
    }
    bits.push('四相 ' + (d.phase || '-'));
    bits.push('轮次 ' + d.round + ' · 消息 ' + d.msgCount + ' · 待处理 ' + d.pending);
    if (d.noteActive) { bits.push('📋 Note'); }
    line.textContent = bits.join(' · ');
    catDetailBody.appendChild(line);
    // 端口入口——刷新按钮左边：状态行不再显示端口号，端口本身可点直达对话窗口（2026-10-01 体验轮）
    var portBtn = document.getElementById('catDetailPort');
    if (d.running && d.port) {
        portBtn.style.display = '';
        portBtn.textContent = ':' + d.port;
        portBtn.title = '点击打开对话窗口（http://127.0.0.1:' + d.port + '/）';
        portBtn.onclick = function () { window.open('http://127.0.0.1:' + d.port + '/', '_blank'); };
    } else {
        portBtn.style.display = 'none';
        portBtn.onclick = null;
    }
    if (catDetailInfoOn) { catDetailBody.appendChild(catDetailInfoBlock(d)); }
    // 两条输入按「上一次输入时间」升序——更晚的一条沉底（用户最后输入晚于猫 → 用户输入在最下方）
    var userBlock = catDetailMsgBlock('用户上一次的输入', d.lastUser, '无——本会话与留档均无用户消息');
    var replyBlock = catDetailMsgBlock('猫上一次的输入', d.lastReply, '无——本会话与留档均无回复');
    var userTime = d.lastUser ? (d.lastUser.time || 0) : 0;
    var replyTime = d.lastReply ? (d.lastReply.time || 0) : 0;
    if (userTime <= replyTime) {
        catDetailBody.appendChild(userBlock);
        catDetailBody.appendChild(replyBlock);
    } else {
        catDetailBody.appendChild(replyBlock);
        catDetailBody.appendChild(userBlock);
    }
    applyCatDetailActions(d);
    if (catDetailArchiveOn) { catDetailBody.appendChild(catDetailArchiveBlock(d)); }
}

// /info 区——状态字段键值（与 cat.info 单猫条目同源）
function catDetailInfoBlock(d) {
    var box = document.createElement('div');
    box.className = 'cd-block';
    var head = document.createElement('div');
    head.className = 'cd-block-head';
    head.textContent = '/info — 状态';
    box.appendChild(head);
    var lines = [];
    for (var i = 0; i < CAT_DETAIL_FIELDS.length; i++) {
        var k = CAT_DETAIL_FIELDS[i];
        if (typeof d[k] === 'undefined') { continue; }
        lines.push(k + ' = ' + d[k]);
    }
    var pre = document.createElement('pre');
    pre.className = 'cd-pre';
    pre.textContent = lines.join('\n');
    box.appendChild(pre);
    return box;
}

// 留档区——上一个被销毁的会话内容（sessions_old 最近一份全文）
function catDetailArchiveBlock(d) {
    var box = document.createElement('div');
    box.className = 'cd-block';
    var head = document.createElement('div');
    head.className = 'cd-block-head';
    head.textContent = '上一个被销毁的会话内容';
    box.appendChild(head);
    if (!d.archive) {
        var none = document.createElement('div');
        none.className = 'cd-block-body cd-empty';
        none.textContent = '无——该猫尚无会话留档（sessions_old）';
        box.appendChild(none);
        return box;
    }
    var meta = document.createElement('div');
    meta.className = 'cd-block-meta';
    meta.textContent = d.archive.file;
    box.appendChild(meta);
    var pre = document.createElement('pre');
    pre.className = 'cd-pre';
    pre.textContent = d.archive.text;
    box.appendChild(pre);
    return box;
}

// 动作面态——停止 / 发送入口随猫类型：主干（special）由后端拒绝（cat.pause / cat.chat 不收主干自查），入口前置收口（不做假成功）
function applyCatDetailActions(d) {
    catDetailPauseBtn.style.display = d.special ? 'none' : '';
    catDetailInput.disabled = !!d.special;
    catDetailSendBtn.disabled = !!d.special;
    catDetailInput.placeholder = d.special ? '主干会话——请在对话窗口输入' : '输入内容——发送给该猫（cat.chat）';
    catDetailSendMsg.textContent = '';
}

// 会话详情指令投递——POST /api/v1/command；失败写 catDetailMsg，成功回调收尾
function catDetailCommand(cmd, onOk) {
    fetch('/api/v1/command', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: cmd })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) { catDetailMsg.textContent = '投递失败: ' + (d.error || ''); return; }
            if (onOk) { onOk(); }
        })
        .catch(function (e) { catDetailMsg.textContent = '投递失败: ' + e; });
}

// 停止本轮——cat.pause <key>
function catDetailPause() {
    if (catDetailKey.length === 0) { return; }
    catDetailMsg.textContent = '';
    catDetailCommand('cat.pause ' + catDetailKey, function () {
        catDetailMsg.textContent = '已投递停止本轮';
    });
}

// 一行输入发送——cat.chat <key> <内容>（成功才清空输入 + 延迟刷新详情）
function catDetailSend() {
    if (catDetailKey.length === 0) { return; }
    var text = catDetailInput.value.trim();
    if (text.length === 0) { catDetailSendMsg.textContent = '内容为空——未发送'; return; }
    catDetailSendMsg.textContent = '';
    catDetailCommand('cat.chat ' + catDetailKey + ' ' + text, function () {
        catDetailInput.value = '';
        catDetailSendMsg.textContent = '已发送';
        setTimeout(loadCatDetail, 500);
    });
}

// /new——先重取实时状态（isIdle 判定取当下值，不依赖快照相位），再开确认弹层
function catDetailNewSession() {
    if (catDetailKey.length === 0) { return; }
    catDetailMsg.textContent = '';
    fetch('/api/v1/cat-detail?cat=' + encodeURIComponent(catDetailKey))
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) { catDetailMsg.textContent = '状态读取失败: ' + (d.error || ''); return; }
            catDetailData = d;
            renderCatDetail();
            openCatNewConfirm(d);
        })
        .catch(function (e) { catDetailMsg.textContent = '状态读取失败: ' + e; });
}

// 确认弹层——Idle=确认即执行；非 Idle=警示 + 勾选「我确认」方可执行
function openCatNewConfirm(d) {
    catNewTitle.textContent = d.name || d.cat;
    catNewMsg.textContent = '';
    catNewConfirm.checked = false;
    if (d.isIdle) {
        catNewText.textContent = '当前会话前文将被清空，并按注入清单重新注入。';
        catNewConfirmWrap.style.display = 'none';
        catNewGo.disabled = false;
        catNewGo.style.opacity = '1';
    } else {
        catNewText.textContent = '这只猫可能还在运行，确定要开新会话？';
        catNewConfirmWrap.style.display = 'flex';
        catNewGo.disabled = true;
        catNewGo.style.opacity = '0.5';
    }
    catNewModal.style.display = 'flex';
}

catNewConfirm.addEventListener('change', function () {
    catNewGo.disabled = !catNewConfirm.checked;
    catNewGo.style.opacity = catNewConfirm.checked ? '1' : '0.5';
});

function catNewSubmit() {
    if (catNewGo.disabled) { return; }
    catNewGo.disabled = true;
    catNewGo.style.opacity = '0.5';
    fetch('/api/v1/command', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: 'cat.new-session ' + catDetailKey })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                catNewMsg.textContent = '投递失败: ' + (d.error || '');
                catNewGo.disabled = false;
                catNewGo.style.opacity = '1';
                return;
            }
            catNewMsg.textContent = '已请求新会话——前文清空并重新注入中';
            setTimeout(function () {
                catNewModal.style.display = 'none';
                catDetailMsg.textContent = '已请求新会话';
                loadCatDetail();
            }, 1500);
        })
        .catch(function (e) {
            catNewMsg.textContent = '投递失败: ' + e;
            catNewGo.disabled = false;
            catNewGo.style.opacity = '1';
        });
}

// 接线——详情弹层 + 新会话确认弹层（静态元素，一次绑定）
document.getElementById('catDetailClose').addEventListener('click', closeCatDetail);
document.getElementById('catDetailReload').addEventListener('click', function () { catDetailMsg.textContent = ''; loadCatDetail(); });
document.getElementById('catDetailInfo').addEventListener('click', function () { catDetailInfoOn = !catDetailInfoOn; renderCatDetail(); });
document.getElementById('catDetailArchive').addEventListener('click', function () { catDetailArchiveOn = !catDetailArchiveOn; renderCatDetail(); });
document.getElementById('catDetailNew').addEventListener('click', catDetailNewSession);
document.getElementById('catDetailPause').addEventListener('click', catDetailPause);
document.getElementById('catDetailSend').addEventListener('click', catDetailSend);
// 遮罩点击关闭——点弹层外区域（事件目标 = 遮罩本体）快速关闭
catDetailModal.addEventListener('click', function (e) {
    if (e.target === catDetailModal) { closeCatDetail(); }
});
document.getElementById('catNewCancel').addEventListener('click', function () { catNewModal.style.display = 'none'; });
document.getElementById('catNewGo').addEventListener('click', catNewSubmit);

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
// [段13] 配置区大项收起 / 展开——头部点击切换（收起态只留「标题 + 说明」一行）
// 展开态记录：localStorage 键 cfg-sec:<data-cfg>（'1' 收起 / '0' 展开）——有记录按记录落态，无记录用页面默认态（首两项展开 / 后四项收起）
var CFG_SEC_STORE = 'cfg-sec:';
var cfgSectionList = document.querySelectorAll('#tab-config .cfg-sec');
for (var cfgIdx = 0; cfgIdx < cfgSectionList.length; cfgIdx++) {
    wireCfgSection(cfgSectionList[cfgIdx]);
}
function wireCfgSection(sec) {
    var head = sec.querySelector('.cfg-sec-head');
    if (!head) { return; }
    var mark = head.querySelector('.cfg-sec-mark');
    var key = sec.getAttribute('data-cfg') || '';
    var saved = cfgSecLoad(key);
    if (saved !== null) { cfgSecSet(sec, mark, saved); }
    head.addEventListener('click', function () {
        var collapsed = sec.classList.toggle('collapsed');
        if (mark) { mark.textContent = collapsed ? '▸' : '▾'; }
        cfgSecSave(key, collapsed);
    });
}
// 记录读取——三态：true 收起 / false 展开 / null 无记录（含存储不可用——回落页面默认态）
function cfgSecLoad(key) {
    if (key.length === 0) { return null; }
    try {
        var v = localStorage.getItem(CFG_SEC_STORE + key);
        if (v === '1') { return true; }
        if (v === '0') { return false; }
    } catch (e) {
        console.warn('[UI] 配置区展开态读取失败——回落页面默认态: ' + e);
    }
    return null;
}
// 记录写入——失败可见（console 具名），不中断交互
function cfgSecSave(key, collapsed) {
    if (key.length === 0) { return; }
    try {
        localStorage.setItem(CFG_SEC_STORE + key, collapsed ? '1' : '0');
    } catch (e) {
        console.warn('[UI] 配置区展开态记录失败: ' + e);
    }
}
// 落态——类与箭头一并同步（记录回放与点击共用同一出口）
function cfgSecSet(sec, mark, collapsed) {
    if (collapsed) { sec.classList.add('collapsed'); }
    else { sec.classList.remove('collapsed'); }
    if (mark) { mark.textContent = collapsed ? '▸' : '▾'; }
}
