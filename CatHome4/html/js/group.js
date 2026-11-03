// CH4 群聊页（A301 · 每组一端口）——group.html 脚本：本组信息 / 消息流 / 发送 / 增量轮询
// 端点（同源 = 本组专属端口）：GET /api/v1/group/info · GET /api/v1/group/messages
//                                POST /api/v1/group/messages · GET /api/v1/cats
// 语义：端口即组身份——页面不做组切换；成员与发送方一律按 cat key，显示名只是附属

var gName = '';
var gLastSeq = 0;
var gPollTimer = 0;     // 增量轮询句柄（0 = 未启动）
var gPollMs = 5000;     // 轮询间隔（毫秒）
var gPolling = false;   // 防重入——上一次增量拉取未回不叠发

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
            gStartPoll();
        })
        .catch(function () {
            gMessagesEl.textContent = '读取失败——宿主未运行？';
        });
}

// 单条消息行——序号 / 发送方（显示名 + key）/ 类型 / 时刻 / 正文
function gBuildRow(m) {
    var row = document.createElement('div');
    row.className = 'msg';
    var head = document.createElement('div');
    head.className = 'msg-head';
    var sender = m.displayName || m.from || '人';
    if (m.from && m.displayName && m.displayName !== m.from) {
        sender = m.displayName + '（' + m.from + '）';
    }
    var target = '';
    if (m.to) {
        target = ' → @' + (m.toDisplayName || m.to);
    }
    head.textContent = '#' + m.seq + ' · ' + sender + target + ' · ' + gTypeLabel(m.type) + ' · ' + gTimeText(m.ts);
    row.appendChild(head);
    var text = document.createElement('div');
    text.className = 'msg-text';
    text.textContent = m.text || '';
    row.appendChild(text);
    return row;
}

// 全量重建——首次加载 / 手动刷新（滚到最新）
function gRenderMessages(list) {
    gMessagesEl.textContent = '';
    if (list.length === 0) {
        var empty = document.createElement('div');
        empty.className = 'empty';
        empty.textContent = '（本组还没有消息）';
        gMessagesEl.appendChild(empty);
        return;
    }
    for (var i = 0; i < list.length; i = i + 1) {
        gMessagesEl.appendChild(gBuildRow(list[i]));
    }
    gMessagesEl.scrollTop = gMessagesEl.scrollHeight;
}

// 增量追加——轮询用（不重建；贴底时自动跟底，读旧消息时不打断位置）
function gAppendMessages(list) {
    if (list.length === 0) {
        return;
    }
    var empty = gMessagesEl.querySelector('.empty');
    if (empty) {
        gMessagesEl.removeChild(empty);
    }
    var atBottom = (gMessagesEl.scrollHeight - gMessagesEl.scrollTop - gMessagesEl.clientHeight) < 24;
    for (var i = 0; i < list.length; i = i + 1) {
        gMessagesEl.appendChild(gBuildRow(list[i]));
    }
    if (atBottom) {
        gMessagesEl.scrollTop = gMessagesEl.scrollHeight;
    }
}

// [段3] 发送

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
            gPollTick();
            gLoadInfo();
        })
        .catch(function (e) {
            gMsgEl.textContent = '发送失败: ' + e;
        });
}

// [段4] 增量轮询——after 增量拉取；页面隐藏暂停，回到前台立即补一次

// 单次增量拉取——新消息追加进消息区，并刷新页头统计
function gPollTick() {
    if (gPolling) {
        return;
    }
    if (document.hidden) {
        return;
    }
    gPolling = true;
    fetch('/api/v1/group/messages?after=' + gLastSeq + '&limit=200', { cache: 'no-store' })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            gPolling = false;
            if (!d.ok) {
                return;
            }
            var list = d.messages || [];
            gLastSeq = Number(d.lastSeq) || gLastSeq;
            if (list.length > 0) {
                gAppendMessages(list);
                gLoadInfo();
            }
        })
        .catch(function () {
            gPolling = false;
        });
}

// 启动轮询——已启动则幂等（句柄类型不定：浏览器 number / Node Timeout 对象 → 只比 0）
function gStartPoll() {
    if (gPollTimer !== 0) {
        return;
    }
    gPollTimer = setInterval(gPollTick, gPollMs);
}

// 停止轮询——页面隐藏时释放定时器
function gStopPoll() {
    if (gPollTimer === 0) {
        return;
    }
    clearInterval(gPollTimer);
    gPollTimer = 0;
}

// 可见性联动——回到前台立即补一次（不等到下一个 tick）
function gOnVisibilityChange() {
    if (document.hidden) {
        gStopPoll();
        return;
    }
    gPollTick();
    gStartPoll();
}

// [段5] 身份下拉与初始化

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
gTextInput.addEventListener('keydown', function (e) {
    if (e.key === 'Enter') {
        gSend();
    }
});
document.addEventListener('visibilitychange', gOnVisibilityChange);

gLoadCats();
gLoadInfo();
gLoadMessages();
