// ═══════════════════════════════════════════
// chat/delay.js —— 定时指令面板（侧翼件 A170 · 规格 design-ch4-delay §六）
//
// 数据源：state 段 `delay` 字段（A185——原 GET /api/v1/delay 旁路端点退役；条目含绝对 dueAt，
//         倒计时本地自算，1 秒粒度，页面不可见停表）——本件退化为纯显示（经主干 stateRenderDelay 分发）
// 写面：指令总线（delay.add|sec|content · delay.addat · delay.addatloop · delay.set · delay.cancel · delay.loop）
//       ——复用 input.js 的 postCommand，零新写端点；变更后列表由 state 帧推送自更新（不回拉）
// 交互：按钮展开弹层（与 Note 弹层互斥）；分区 = 待触发（列表 · 倒计时主信息）+ 插入指令（快捷档 / 自定义 / 循环）
//
// 形态：弹层——DOM 在 chat.html（#delayWrap 段），样式 chat.css「延迟指令队列」段；本件零新增样式
// ═══════════════════════════════════════════

/// 当前列表（数据源快照——state 段投影）
var delayEntries = [];
/// 倒计时句柄（有可见条目且页面可见才跑）
var delayTimer = null;
/// 新建区「自定义」时刻行展开态
var delayCustomOpen = false;
/// 行内编辑中的条目 id（null = 无）
var delayEditingId = null;
/// 新建区选中档（秒）；null = 自定义时刻
var delaySelectedSec = 300;

/// 快捷档（相对时长·秒）——单一出口：新建区与行内编辑条共用
var delayQuickDefs = [
    { label: '5m', sec: 300 },
    { label: '15m', sec: 900 },
    { label: '30m', sec: 1800 },
    { label: '1h', sec: 3600 }
];

/// state 段投影——整段接收（由主干 stateRenderDelay 分发；空段 = 空表）
function delayApplyState(d) {
    delayEntries = (d && d.entries) ? d.entries : [];
    delayRender();
}

/// 面板展开 / 收起——与 Note 弹层互斥（两弹层同位置）
function delayToggle() {
    var pop = document.getElementById('delayPopover');
    if (!pop) {
        return;
    }
    if (pop.style.display === 'none' || pop.style.display === '') {
        noteClose();
        delayRender();
        pop.style.display = 'flex';
        delaySyncTimer();
    } else {
        delayClose();
    }
}

/// 收起——按钮切换 / 外部点击 / 与 Note 互斥三处共用
function delayClose() {
    var pop = document.getElementById('delayPopover');
    if (!pop) {
        return;
    }
    pop.style.display = 'none';
    delayEditingId = null;
    delayCustomOpen = false;
    delaySyncTimer();
}

/// 倒计时文本——剩余时长（>1 天显示天 + 时）
function delayFmtRemain(ms) {
    var s = Math.floor(Math.max(0, ms) / 1000);
    var d = Math.floor(s / 86400);
    var h = Math.floor((s % 86400) / 3600);
    var m = Math.floor((s % 3600) / 60);
    var sec = s % 60;
    if (d > 0) {
        return d + '天' + h + '时';
    }
    if (h > 0) {
        return h + ':' + delayPad(m) + ':' + delayPad(sec);
    }
    return m + ':' + delayPad(sec);
}

/// 两位补零
function delayPad(n) {
    return (n < 10 ? '0' : '') + n;
}

/// 到点时刻文本——本机时区（今天只显时刻，跨天带日期）
function delayFmtDue(ms) {
    var d = new Date(ms);
    var now = new Date();
    var hm = delayPad(d.getHours()) + ':' + delayPad(d.getMinutes()) + ':' + delayPad(d.getSeconds());
    if (d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate()) {
        return hm;
    }
    return (d.getMonth() + 1) + '-' + delayPad(d.getDate()) + ' ' + hm;
}

/// 来源标记——delay（人工）/ sleep（等待）/ timer（排程）/ restart（宿主重启）
function delaySrcMark(source) {
    if (source === 'sleep') { return '💤'; }
    if (source === 'restart') { return '🔄'; }
    if (source === 'timer') { return '⏳'; }
    return '⏰';
}

/// 副列文本——到点时刻 + 循环周期 / 已响标记（渲染与测试共用单一出口）
function delayDueText(e) {
    var t = delayFmtDue(e.dueAt);
    if (e.loop) { t = t + ' · ' + delayLoopText(e); }
    if (e.fired > 0) { t = t + ' 已响 ' + e.fired; }
    return t;
}

/// 循环周期文本——按天（intervalMs = 1 天）单独成词，其余间隔按剩余时长口径
function delayLoopText(e) {
    var iv = e.intervalMs || 0;
    if (iv <= 0) { return '🔁'; }
    if (iv === 86400000) { return '每天'; }
    return '每 ' + delayFmtRemain(iv);
}

/// 即将触发判据——倒计时 ≤60 秒高亮
function delayCdSoon(ms) {
    return ms <= 60000;
}

/// 快捷档 → 绝对时刻（同刻取值——循环时长精确等于档位）
function delayQuickDueAt(sec, now) {
    var base = (typeof now === 'number') ? now : Date.now();
    return base + sec * 1000;
}

/// datetime-local 值（yyyy-MM-ddTHH:mm(:ss)）→ 毫秒（本机时区；空 / 非法返回 null）
function delayParseLocalValue(v) {
    var t = (v || '').trim();
    if (t.length === 0) {
        return null;
    }
    var m = t.match(/^(\d{4})-(\d{2})-(\d{2})[T](\d{2}):(\d{2})(?::(\d{2}))?$/);
    if (m === null) {
        return null;
    }
    var d = new Date(parseInt(m[1], 10), parseInt(m[2], 10) - 1, parseInt(m[3], 10),
        parseInt(m[4], 10), parseInt(m[5], 10), m[6] ? parseInt(m[6], 10) : 0, 0);
    if (isNaN(d.getTime())) {
        return null;
    }
    return d.getTime();
}

/// 毫秒 → datetime-local 值（编辑条回填；本机时区）
function delayFmtLocalValue(ms) {
    var d = new Date(ms);
    return d.getFullYear() + '-' + delayPad(d.getMonth() + 1) + '-' + delayPad(d.getDate()) + 'T'
        + delayPad(d.getHours()) + ':' + delayPad(d.getMinutes()) + ':' + delayPad(d.getSeconds());
}

/// 指令行拼装——单一出口（格式漂移防线）
function delayAddLine(sec, content) {
    return 'delay.add|' + String(sec) + '|' + content;
}

function delayAddAtLine(dueAt, content) {
    return 'delay.addat|' + String(dueAt) + '|' + content;
}

function delayAddAtLoopLine(dueAt, content) {
    return 'delay.addatloop|' + String(dueAt) + '|' + content;
}

function delaySetLine(id, dueAt) {
    return 'delay.set|' + String(id) + '|' + String(dueAt);
}

function delayCancelLine(id) {
    return 'delay.cancel|' + String(id);
}

function delayLoopLine(id, on) {
    return 'delay.loop|' + String(id) + '|' + (on ? '1' : '0');
}

/// 快捷档 chips HTML——单一出口（新建区与行内编辑条共用）
function delayChipsHtml(act, id, extra) {
    var html = '<div class="delay-chips">';
    for (var i = 0; i < delayQuickDefs.length; i = i + 1) {
        var d = delayQuickDefs[i];
        html = html + '<button class="delay-chip" data-act="' + act + '" data-sec="' + String(d.sec) + '"';
        if (id !== null && id !== undefined) {
            html = html + ' data-id="' + String(id) + '"';
        }
        html = html + '>' + d.label + '</button>';
    }
    if (extra) {
        html = html + extra;
    }
    html = html + '</div>';
    return html;
}

/// 列表渲染——重建（数据变化时调用）；倒计时由 delayTick 原地更新
function delayRender() {
    var list = document.getElementById('delayList');
    if (!list) {
        return;
    }
    var count = document.getElementById('delayCount');
    if (count) {
        count.textContent = delayEntries.length > 0 ? (' ' + delayEntries.length) : '';
    }
    var secCount = document.getElementById('delaySecCount');
    if (secCount) {
        secCount.textContent = delayEntries.length > 0 ? (' · ' + delayEntries.length + ' 条') : '';
    }
    list.textContent = '';
    if (delayEntries.length === 0) {
        var empty = document.createElement('div');
        empty.className = 'delay-empty';
        empty.textContent = '（空）';
        list.appendChild(empty);
        delaySyncTimer();
        return;
    }
    for (var i = 0; i < delayEntries.length; i = i + 1) {
        list.appendChild(delayBuildRow(delayEntries[i]));
        if (delayEditingId === delayEntries[i].id) {
            list.appendChild(delayBuildEditRow(delayEntries[i]));
        }
    }
    delaySyncTimer();
}

/// 单行构造——来源标记 / 倒计时 / 到点 / 内容 / 循环 · 改 · 取消
function delayBuildRow(e) {
    var remain = e.dueAt - Date.now();
    var row = document.createElement('div');
    row.className = 'delay-row';
    row.setAttribute('data-id', String(e.id));
    var mark = document.createElement('span');
    mark.className = 'delay-mark';
    mark.textContent = delaySrcMark(e.source);
    var cd = document.createElement('span');
    cd.className = delayCdSoon(remain) ? 'delay-cd soon' : 'delay-cd';
    cd.setAttribute('data-due', String(e.dueAt));
    cd.textContent = delayFmtRemain(remain);
    var due = document.createElement('span');
    due.className = 'delay-due';
    due.textContent = delayDueText(e);
    var text = document.createElement('span');
    text.className = 'delay-text';
    text.textContent = e.content;
    var loopBtn = document.createElement('button');
    loopBtn.className = 'delay-act';
    loopBtn.textContent = e.loop ? '🔁' : '单次';
    loopBtn.title = '切换循环（触发后按相同时长重排，不自动移除）';
    loopBtn.setAttribute('data-act', 'loop');
    loopBtn.setAttribute('data-id', String(e.id));
    loopBtn.setAttribute('data-loop', e.loop ? '1' : '0');
    var edit = document.createElement('button');
    edit.className = 'delay-act';
    edit.textContent = '改';
    edit.title = '改触发时刻（快捷档为相对当前时刻；也可直接选时刻）';
    edit.setAttribute('data-act', 'edit');
    edit.setAttribute('data-id', String(e.id));
    var cancel = document.createElement('button');
    cancel.className = 'delay-act';
    cancel.textContent = '×';
    cancel.title = '取消该条目';
    cancel.setAttribute('data-act', 'cancel');
    cancel.setAttribute('data-id', String(e.id));
    row.appendChild(mark);
    row.appendChild(cd);
    row.appendChild(due);
    row.appendChild(text);
    row.appendChild(loopBtn);
    row.appendChild(edit);
    row.appendChild(cancel);
    return row;
}

/// 行内编辑条——快捷档（相对当前时刻）+ 自定义时刻（回填当前 dueAt）
function delayBuildEditRow(e) {
    var wrap = document.createElement('div');
    wrap.className = 'delay-edit';
    wrap.innerHTML = '<span class="delay-edit-label">改为</span>'
        + delayChipsHtml('edit-quick', e.id, null)
        + '<input type="datetime-local" step="1" class="delay-edit-at" id="delayEditAt" value="' + delayFmtLocalValue(e.dueAt) + '">'
        + '<button class="delay-act" data-act="edit-ok" data-id="' + String(e.id) + '">确定</button>'
        + '<button class="delay-act" data-act="edit-cancel">取消</button>';
    return wrap;
}

/// 新建区快捷档渲染——四档 + 「自定义」（chips 出口 + 选中态标记）
function delayRenderQuick() {
    var row = document.getElementById('delayQuickRow');
    if (!row) {
        return;
    }
    row.innerHTML = delayChipsHtml('quick-pick', null, '<button class="delay-chip" id="delayChipCustom" data-act="custom-toggle">自定义</button>');
    delayMarkQuickSel();
    delayRenderCustom();
}

/// 选中态标记——chip 与「自定义」按 delaySelectedSec 高亮（单一出口）
function delayMarkQuickSel() {
    var row = document.getElementById('delayQuickRow');
    if (!row) {
        return;
    }
    var chips = row.getElementsByClassName('delay-chip');
    for (var i = 0; i < chips.length; i = i + 1) {
        var sec = chips[i].getAttribute('data-sec');
        var on = (sec !== null && delaySelectedSec !== null && parseInt(sec, 10) === delaySelectedSec);
        chips[i].className = on ? 'delay-chip on' : 'delay-chip';
    }
    var custom = document.getElementById('delayChipCustom');
    if (custom) {
        custom.className = (delaySelectedSec === null) ? 'delay-chip on' : 'delay-chip';
    }
}

/// 自定义时刻行显隐——默认值 = 当前 + 30 分钟（仅空值回填，不覆盖用户输入）
function delayRenderCustom() {
    var row = document.getElementById('delayCustomRow');
    if (row) {
        row.style.display = delayCustomOpen ? 'flex' : 'none';
    }
    if (delayCustomOpen) {
        var at = document.getElementById('delayCustomAt');
        if (at && at.value.length === 0) {
            at.value = delayFmtLocalValue(delayQuickDueAt(1800));
        }
    }
}

/// 倒计时刷新——原地更新（不重建 DOM）
function delayTick() {
    var list = document.getElementById('delayList');
    if (!list) {
        return;
    }
    var nodes = list.getElementsByClassName('delay-cd');
    for (var i = 0; i < nodes.length; i = i + 1) {
        var due = parseInt(nodes[i].getAttribute('data-due'), 10);
        var remain = due - Date.now();
        nodes[i].textContent = delayFmtRemain(remain);
        var want = delayCdSoon(remain) ? 'delay-cd soon' : 'delay-cd';
        if (nodes[i].className !== want) {
            nodes[i].className = want;
        }
    }
}

/// 计时器同步——有可见条目且页面可见才跑表（空表 / 隐藏页零开销）
function delaySyncTimer() {
    var need = false;
    if (delayEntries.length > 0 && document.visibilityState !== 'hidden') {
        var pop = document.getElementById('delayPopover');
        if (pop && pop.style.display === 'flex') {
            need = true;
        }
    }
    if (need && delayTimer === null) {
        delayTimer = setInterval(delayTick, 1000);
    }
    if (!need && delayTimer !== null) {
        clearInterval(delayTimer);
        delayTimer = null;
    }
}

/// 事件委托——列表行 / 编辑条 / 快捷档三面统一分派（列表重建无需重绑）
function delayOnClick(ev) {
    var t = ev.target;
    if (!t || !t.getAttribute) {
        return;
    }
    var act = t.getAttribute('data-act');
    if (!act) {
        return;
    }
    var id = t.getAttribute('data-id');
    if (act === 'cancel') {
        delaySend(delayCancelLine(id));
        return;
    }
    if (act === 'loop') {
        delayLoopClick(t);
        return;
    }
    if (act === 'edit') {
        delayToggleEdit(id);
        return;
    }
    if (act === 'edit-cancel') {
        delayEditingId = null;
        delayRender();
        return;
    }
    if (act === 'edit-ok') {
        delayEditOk(id);
        return;
    }
    if (act === 'edit-quick') {
        delayEditQuick(id, t.getAttribute('data-sec'));
        return;
    }
    if (act === 'quick-pick') {
        delayPickQuick(t.getAttribute('data-sec'));
        return;
    }
    if (act === 'custom-toggle') {
        delayPickCustom();
        return;
    }
}

/// 行内编辑开关——同一条目再点收起
function delayToggleEdit(id) {
    var n = parseInt(id, 10);
    delayEditingId = (delayEditingId === n) ? null : n;
    delayRender();
}

/// 新建区档位选择——快捷档（相对）；清自定义行
function delayPickQuick(secRaw) {
    delaySelectedSec = parseInt(secRaw, 10);
    delayCustomOpen = false;
    delayMarkQuickSel();
    delayRenderCustom();
}

/// 新建区档位选择——自定义时刻（绝对）；展开 datetime-local 行
function delayPickCustom() {
    delaySelectedSec = null;
    delayCustomOpen = true;
    delayMarkQuickSel();
    delayRenderCustom();
}

/// 新建提交——选中档（相对·后端算 dueAt，零漂移）或自定义时刻（绝对）
function delayAddSubmit() {
    var textInput = document.getElementById('delayText');
    if (!textInput) {
        return;
    }
    var content = textInput.value.trim();
    if (content.length === 0) {
        warn('延迟指令新建：内容为空');
        return;
    }
    var loopInput = document.getElementById('delayLoop');
    var loop = (loopInput && loopInput.checked) ? true : false;
    var line;
    if (delaySelectedSec === null) {
        var at = document.getElementById('delayCustomAt');
        var dueAt = at ? delayParseLocalValue(at.value) : null;
        if (dueAt === null || dueAt <= Date.now()) {
            warn('延迟指令新建：时刻须为未来（yyyy-MM-dd HH:mm:ss）');
            return;
        }
        line = loop ? delayAddAtLoopLine(dueAt, content) : delayAddAtLine(dueAt, content);
    } else if (loop) {
        line = delayAddAtLoopLine(delayQuickDueAt(delaySelectedSec), content);
    } else {
        line = delayAddLine(delaySelectedSec, content);
    }
    textInput.value = '';
    delaySend(line);
}

/// 行内改时刻——快捷档（相对当前时刻，点即提交）
function delayEditQuick(id, secRaw) {
    delayEditingId = null;
    delaySend(delaySetLine(id, delayQuickDueAt(parseInt(secRaw, 10))));
}

/// 行内改时刻——自定义时刻（绝对，须为未来）
function delayEditOk(id) {
    var at = document.getElementById('delayEditAt');
    if (!at) {
        return;
    }
    var dueAt = delayParseLocalValue(at.value);
    if (dueAt === null || dueAt <= Date.now()) {
        warn('延迟指令改时刻：时刻须为未来（yyyy-MM-dd HH:mm:ss）');
        return;
    }
    delayEditingId = null;
    delaySend(delaySetLine(id, dueAt));
}

/// 循环开关——切换条目 loop 标记（触发后按相同时长重排）
function delayLoopClick(t) {
    var id = t.getAttribute('data-id');
    var next = t.getAttribute('data-loop') === '1' ? '0' : '1';
    delaySend(delayLoopLine(id, next === '1'));
}

/// 指令投递——写面复用指令总线；列表变更由 state 帧推送自更新（A185：不再回拉列表）
function delaySend(line) {
    postCommand(line);
}

/// 初始化——按钮 / 输入绑定 + 快捷档渲染 + 首次拉取 + 页面可见性联动
function delayInit() {
    var btn = document.getElementById('delayBtn');
    if (btn) {
        btn.addEventListener('click', function (e) {
            e.stopPropagation();
            delayToggle();
        });
    }
    var pop = document.getElementById('delayPopover');
    if (pop) {
        pop.addEventListener('click', delayOnClick);
    }
    var addBtn = document.getElementById('delayAddBtn');
    if (addBtn) {
        addBtn.addEventListener('click', delayAddSubmit);
    }
    var textInput = document.getElementById('delayText');
    if (textInput) {
        textInput.addEventListener('keydown', function (e) {
            if (e.key === 'Enter') {
                delayAddSubmit();
            }
        });
    }
    delayRenderQuick();
    // 外部点击收起
    document.addEventListener('click', function (e) {
        var p = document.getElementById('delayPopover');
        var wrap = document.getElementById('delayWrap');
        if (!p || !wrap) {
            return;
        }
        if (p.style.display !== 'flex') {
            return;
        }
        if (wrap.contains(e.target)) {
            return;
        }
        delayClose();
    });
    // 页面可见性——隐藏停表（dueAt 权威，回来自算补齐）
    document.addEventListener('visibilitychange', function () {
        if (document.visibilityState === 'hidden') {
            if (delayTimer !== null) {
                clearInterval(delayTimer);
                delayTimer = null;
            }
        } else {
            delayTick();
            delaySyncTimer();
        }
    });
}

delayInit();
