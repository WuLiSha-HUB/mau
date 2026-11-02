// CH4 群聊页（A301 · 每组一端口）——group.html 脚本：本组信息 / 消息流 / 发送 / 已读推进
// 端点（同源 = 本组专属端口）：GET /api/v1/group/info · GET /api/v1/group/messages
//                                POST /api/v1/group/messages · POST /api/v1/group/read · GET /api/v1/cats
// 语义：端口即组身份——页面不做组切换；成员与发送方一律按 cat key，显示名只是附属；已读只允许前进

var gName = '';
var gLastSeq = 0;

var gTitleEl = document.getElementById('groupTitle');
var gMetaEl = document.getElementById('groupMeta');
var gMsgEl = document.getElementById('msg');
var gMessagesEl = document.getElementById('messages');
var gFromSel = document.getElementById('fromSel');
var gTextInput = document.getElementById('textInput');

// [段1] 辅助——时刻 / 类型标签

// 时刻文本——本地时区 yyyy-MM-dd HH:mm:ss
function gTimeText(unixMs) {
    var ms = Number(unixMs) || 0;
    if (ms <= 0) {
        return '-';
    }
    var d = new Date(ms);
    var pad = function (n) {
        if (n < 10) {
            return '0' + n;
        }
        return String(n);
    };
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate())
        + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
}

// 消息类型标签——派单 / 回执 / 讨论
function gTypeLabel(type) {
    if (type === 'delegation') {
        return '派单';
    }
    if (type === 'receipt') {
        return '回执';
    }
    return '讨论';
}

// [段2] 组信息与消息流

// 拉本组信息——端口即组身份
function gLoadInfo() {
    fetch('/api/v1/group/info')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                gTitleEl.textContent = '组不可用';
                gMetaEl.textContent = d.error || '';
                return;
            }
            var g = d.group;
            gName = g.name;
            gLastSeq = Number(g.lastSeq) || 0;
            gTitleEl.textContent = g.name;
            var state = '运行中';
            if (g.enabled === false) {
                state = '已关闭';
            }
            var proj = '';
            if (g.project && g.project.length > 0) {
                proj = ' · 项目 ' + g.project;
            }
            gMetaEl.textContent = state + ' · 成员 ' + ((g.members || []).length) + ' · 消息 ' + (g.msgCount || 0) + proj;
            document.title = 'CH4 群聊 · ' + g.name;
        })
        .catch(function () {
            gTitleEl.textContent = '组不可用';
            gMetaEl.textContent = '读取失败——宿主未运行？';
        });
}

// 拉消息（最多 200 条，seq 升序）
function gLoadMessages() {
    fetch('/api/v1/group/messages?limit=200')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                gMessagesEl.textContent = '读取失败: ' + (d.error || '');
                return;
            }
            gLastSeq = Number(d.lastSeq) || 0;
            gRenderMessages(d.messages || []);
        })
        .catch(function () {
            gMessagesEl.textContent = '读取失败——宿主未运行？';
        });
}

// 渲染消息行——序号 / 发送方（显示名 + key）/ 类型 / 时刻 / 正文
function gRenderMessages(list) {
    gMessagesEl.textContent = '';
    if (list.length === 0) {
        var empty = document.createElement('div');
        empty.className = 'empty';
        empty.textContent = '（本组还没有消息——派单与回执链归 P5）';
        gMessagesEl.appendChild(empty);
        return;
    }
    for (var i = 0; i < list.length; i = i + 1) {
        var m = list[i];
        var row = document.createElement('div');
        row.className = 'msg';
        var head = document.createElement('div');
        head.className = 'msg-head';
        var sender = m.displayName || m.from || '人';
        if (m.from && m.displayName && m.displayName !== m.from) {
            sender = m.displayName + '（' + m.from + '）';
        }
        head.textContent = '#' + m.seq + ' · ' + sender + ' · ' + gTypeLabel(m.type) + ' · ' + gTimeText(m.ts);
        row.appendChild(head);
        var text = document.createElement('div');
        text.className = 'msg-text';
        text.textContent = m.text || '';
        row.appendChild(text);
        gMessagesEl.appendChild(row);
    }
    gMessagesEl.scrollTop = gMessagesEl.scrollHeight;
}

// [段3] 发送与已读

// 发送一条讨论消息（type=note；组名由端口决定，body 不带 group）
function gSend() {
    var text = gTextInput.value.trim();
    if (text.length === 0) {
        return;
    }
    var from = gFromSel.value;
    fetch('/api/v1/group/messages', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ from: from, type: 'note', text: text })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                gMsgEl.textContent = '发送失败: ' + (d.error || '');
                return;
            }
            gTextInput.value = '';
            gMsgEl.textContent = '已发送';
            gLoadMessages();
            gLoadInfo();
        })
        .catch(function (e) {
            gMsgEl.textContent = '发送失败: ' + e;
        });
}

// 标记已读——仅猫身份可用；推进到当前最新序号（只允许前进）
function gMarkRead() {
    var catKey = gFromSel.value;
    if (catKey.length === 0) {
        gMsgEl.textContent = '「标记已读」需把身份切到某只猫';
        return;
    }
    fetch('/api/v1/group/read', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ catKey: catKey, seq: gLastSeq })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                gMsgEl.textContent = '标记失败: ' + (d.error || '');
                return;
            }
            gMsgEl.textContent = '已读至 #' + gLastSeq + '（' + catKey + '）';
        })
        .catch(function (e) {
            gMsgEl.textContent = '标记失败: ' + e;
        });
}

// [段4] 身份下拉与初始化

// 猫列表——发送身份可选（空 = 人 / 匿名）
function gLoadCats() {
    fetch('/api/v1/cats')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            var cats = d.cats || [];
            gFromSel.textContent = '';
            var human = document.createElement('option');
            human.value = '';
            human.textContent = '人（匿名）';
            gFromSel.appendChild(human);
            for (var i = 0; i < cats.length; i = i + 1) {
                var opt = document.createElement('option');
                opt.value = cats[i].id;
                var prefix = '';
                if (cats[i].special) {
                    prefix = '★ ';
                }
                opt.textContent = prefix + (cats[i].name || cats[i].id);
                gFromSel.appendChild(opt);
            }
        })
        .catch(function () {
            gMsgEl.textContent = '猫列表读取失败（发送可用匿名）';
        });
}

document.getElementById('refreshBtn').onclick = function () {
    gLoadInfo();
    gLoadMessages();
};
document.getElementById('sendBtn').onclick = gSend;
document.getElementById('readBtn').onclick = gMarkRead;
gTextInput.addEventListener('keydown', function (e) {
    if (e.key === 'Enter') {
        gSend();
    }
});

gLoadCats();
gLoadInfo();
gLoadMessages();
