// CH4 群聊页（A301 · 每组一端口；A311 复用对话页布局 / 气泡 / MD / 自绘滚动带）
// 端点（同源 = 本组专属端口）：GET /api/v1/group/info · GET /api/v1/group/messages
//                                POST /api/v1/group/messages · GET /api/v1/cats
// 语义：端口即组身份——页面不做组切换；成员与发送方一律按 cat key，显示名只是附属
// 渲染：消息 = 对话页 text 气泡（.chat-row + .chat-bubble + .md-block）——
//       人（from 空）走 user 气泡靠右；猫一律 assistant 气泡靠左；气泡首行 meta 描述来源 / 类型 / 目标
// 滚动：自绘带由 js/chat/fx/scroll.js 原样复用（#chatMsgs.scrollTop 为唯一滚动执行器）

var gName = '';
var gLastSeq = 0;
var gPollTimer = 0;     // 增量轮询句柄（0 = 未启动）
var gPollMs = 2000;     // 轮询间隔（毫秒）——A311：5s → 2s
var gPolling = false;   // 防重入——上一次增量拉取未回不叠发
var gBurstLeft = 0;     // 新消息后的快速跟拉余量（最多 2 次，间隔 800ms）

var gTitleEl = document.getElementById('chatTitle');
var gInfoEl = document.getElementById('chatInfo');
var gMsgEl = document.getElementById('groupMsg');
var gMsgsEl = document.getElementById('chatMsgs');
var gFromSel = document.getElementById('fromSel');
var gInputEl = document.getElementById('chatSendInput');
var gSendBtn = document.getElementById('chatSendBtn');

// [段0] 滚动带依赖桩
// scroll.js 按行 data-type 查块声明表取刻度角色（registry.js 的 tickRole）；
// 群聊页不引 registry.js（其 12 blocks 依赖链），本页只用 user / text 两型——同义最小实现
function tickRole(type) {
    if (type === 'user') {
        return 'user';
    }
    return 'reply';
}

// [段1] 辅助——时刻 / 类型标签 / 发送方文本

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

// 发送方文本——显示名优先；与 key 不同时附 key；人（无 from）显示「人」
function gSenderText(m) {
    if (!m.from) {
        return '人';
    }
    if (m.displayName && m.displayName !== m.from) {
        return m.displayName + '（' + m.from + '）';
    }
    return m.from;
}

// [段2] 组信息与消息流

// 拉本组信息——端口即组身份；页头左段 = 组名 + 现状简介
function gLoadInfo() {
    fetch('/api/v1/group/info', { cache: 'no-store' })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                gTitleEl.textContent = '组不可用';
                gInfoEl.textContent = d.error || '';
                return;
            }
            var g = d.group;
            gName = g.name;
            gLastSeq = Math.max(gLastSeq, Number(g.lastSeq) || 0);
            gTitleEl.textContent = g.name;
            var state = '运行中';
            if (g.enabled === false) {
                state = '已关闭';
            }
            var parts = [state, '成员 ' + ((g.members || []).length) + ' 人', '消息 ' + (g.msgCount || 0) + ' 条'];
            if (g.project && g.project.length > 0) {
                parts.push('项目 ' + g.project);
            }
            gInfoEl.textContent = parts.join(' · ');
            document.title = 'CH4 群聊 · ' + g.name;
        })
        .catch(function () {
            gTitleEl.textContent = '组不可用';
            gInfoEl.textContent = '读取失败——宿主未运行？';
        });
}

// 拉消息（最多 200 条，seq 升序）
function gLoadMessages() {
    fetch('/api/v1/group/messages?limit=200', { cache: 'no-store' })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                gMsgsEl.textContent = '读取失败: ' + (d.error || '');
                return;
            }
            gLastSeq = Math.max(gLastSeq, Number(d.lastSeq) || 0);
            gRenderMessages(d.messages || []);
            gStartPoll();
        })
        .catch(function () {
            gMsgsEl.textContent = '读取失败——宿主未运行？';
        });
}

// 单条消息——对话页 text 气泡形态；人走 user 泡靠右，猫走 assistant 泡靠左
function gBuildRow(m) {
    var isHuman = !m.from;
    var row = document.createElement('div');
    row.className = isHuman ? 'chat-row user' : 'chat-row assistant';
    row.setAttribute('data-type', isHuman ? 'user' : 'text');
    var bubble = document.createElement('div');
    bubble.className = 'chat-bubble';
    bubble.appendChild(gBuildMeta(m, isHuman));
    var md = document.createElement('div');
    md.className = 'md-block';
    md.innerHTML = mdToHtml(m.text || '');
    bubble.appendChild(md);
    row.appendChild(bubble);
    return row;
}

// 气泡首行——略大字号描述来源类型与目标（A311 莎定）
function gBuildMeta(m, isHuman) {
    var meta = document.createElement('div');
    meta.className = 'msg-meta';
    var kind = document.createElement('span');
    kind.className = 'mm-kind';
    kind.textContent = gTypeLabel(m.type);
    meta.appendChild(kind);
    var who = document.createElement('span');
    who.className = 'mm-who';
    who.textContent = gSenderText(m);
    meta.appendChild(who);
    if (m.to) {
        var target = document.createElement('span');
        target.className = 'mm-target';
        target.textContent = '→ @' + (m.toDisplayName || m.to);
        meta.appendChild(target);
    }
    if (m.refs) {
        var refs = document.createElement('span');
        refs.className = 'mm-refs';
        refs.textContent = '关联 #' + m.refs;
        meta.appendChild(refs);
    }
    if (!isHuman)
    {
        var seq = document.createElement('span');
        seq.className = 'mm-seq';
        seq.textContent = '#' + m.seq;
        meta.appendChild(seq);
    }
    var time = document.createElement('span');
    time.className = 'mm-time';
    time.textContent = gTimeText(m.ts);
    meta.appendChild(time);
    return meta;
}

// 空态提示（朴素件——不占滚动带刻度）
function gBuildEmpty() {
    var empty = document.createElement('div');
    empty.className = 'chat-plain group-empty';
    empty.textContent = '（本组还没有消息）';
    return empty;
}

// 全量重建——首次加载 / 手动刷新（滚到最新）
function gRenderMessages(list) {
    gMsgsEl.textContent = '';
    if (list.length === 0) {
        gMsgsEl.appendChild(gBuildEmpty());
        return;
    }
    for (var i = 0; i < list.length; i = i + 1) {
        gMsgsEl.appendChild(gBuildRow(list[i]));
    }
    gMsgsEl.scrollTop = gMsgsEl.scrollHeight;
}

// 增量追加——轮询用（不重建；贴底时自动跟底，读旧消息时不打断位置）
function gAppendMessages(list) {
    if (list.length === 0) {
        return;
    }
    var atBottom = (gMsgsEl.scrollHeight - gMsgsEl.scrollTop - gMsgsEl.clientHeight) < 24;
    // 空态两种来源都清：朴素件（本页产出）与旧的纯文本提示（读取失败后恢复）
    var empty = gMsgsEl.querySelector('.group-empty');
    if (empty) {
        gMsgsEl.removeChild(empty);
    }
    for (var i = 0; i < list.length; i = i + 1) {
        gMsgsEl.appendChild(gBuildRow(list[i]));
    }
    if (atBottom) {
        gMsgsEl.scrollTop = gMsgsEl.scrollHeight;
    }
}

// [段3] 发送

// 发送一条讨论消息（type=note；组名由端口决定，body 不带 group）
function gSend() {
    var text = gInputEl.value.trim();
    if (text.length === 0) {
        return;
    }
    var from = gFromSel.value;
    gSendBtn.disabled = true;
    fetch('/api/v1/group/messages', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ from: from, type: 'note', text: text })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            gSendBtn.disabled = false;
            if (!d.ok) {
                gMsgEl.textContent = '发送失败: ' + (d.error || '');
                return;
            }
            gInputEl.value = '';
            gMsgEl.textContent = '已发送';
            gPollTick();
            gLoadInfo();
        })
        .catch(function (e) {
            gSendBtn.disabled = false;
            gMsgEl.textContent = '发送失败: ' + e;
        });
}

// [段4] 增量轮询——after 增量拉取；新消息后快速跟拉两次；页面隐藏暂停，回到前台立即补一次

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
            gLastSeq = Math.max(gLastSeq, Number(d.lastSeq) || 0);
            if (list.length > 0) {
                gAppendMessages(list);
                gLoadInfo();
                if (gBurstLeft < 2) {
                    gBurstLeft = gBurstLeft + 1;
                    setTimeout(gPollTick, 800);
                }
            } else {
                gBurstLeft = 0;
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
    fetch('/api/v1/cats', { cache: 'no-store' })
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

document.getElementById('chatRefresh').onclick = function () {
    gLoadInfo();
    gLoadMessages();
};
gSendBtn.onclick = gSend;
gInputEl.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        gSend();
    }
});
document.addEventListener('visibilitychange', gOnVisibilityChange);

// 引导——版本标记 / 身份列表 / 组信息 / 消息流 / 自绘滚动带接线
loadUiVersion();
gLoadCats();
gLoadInfo();
gLoadMessages();
chatScrollInit();
