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
var gHasMore = false;    // 首屏尾部读取——更早还有未加载条目（仅提示，不翻页）
var gUserName = '默认用户';  // 使用者显示名——全局设置 ui.user_name 的组端口投影（info 每次加载刷新）
var gThemeIdx = {};      // catKey -> 主题色索引（A320；组信息每次加载刷新）
var gCatNodes = {};      // catKey -> 球列节点（A319；增量复用，不整块重建）

var gTitleEl = document.getElementById('chatTitle');
var gInfoEl = document.getElementById('chatInfo');
var gMsgEl = document.getElementById('groupMsg');
var gMsgsEl = document.getElementById('chatMsgs');
var gToSel = document.getElementById('toSel');
var gInputEl = document.getElementById('chatSendInput');
var gSendBtn = document.getElementById('chatSendBtn');
var gClearConfirm = document.getElementById('chatClearConfirm');
var gClearBtn = document.getElementById('chatClear');
var gCatsEl = document.getElementById('groupCats');

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

// 发送方文本——显示名优先；与 key 不同时附 key；人（无 from）显示使用者名（全局设置投影）
function gSenderText(m) {
    if (!m.from) {
        return gUserName;
    }
    if (m.displayName && m.displayName !== m.from) {
        return m.displayName + '（' + m.from + '）';
    }
    return m.from;
}

// 署名回填——info 晚于消息首屏到达时只改既有节点的文本（不重建行，滚动位置不动）
function gApplyUserName() {
    var whos = gMsgsEl.querySelectorAll('.chat-row.user .mm-who');
    for (var i = 0; i < whos.length; i = i + 1) {
        whos[i].textContent = gUserName;
    }
}

// [段2] 组信息与消息流

// 拉本组信息——端口即组身份；页头左段 = 组名 + 现状简介
// skipTargets = true 时不重建 @ 目标下拉（A319 轮询静默刷新球列与统计用，避免打断打开中的选择器）
function gLoadInfo(skipTargets) {
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
            // 使用者显示名——全局设置 ui.user_name（空 / 缺省回落「默认用户」）
            if (typeof d.userName === 'string' && d.userName.length > 0) {
                gUserName = d.userName;
                // info 可能晚于消息首屏到达——回填已渲染行，避免旧气泡停在上一次的署名
                gApplyUserName();
            }
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
            var members = g.members || [];
            gInfoEl.title = gMemberRoster(members);
            // A319/A320——主题色映射 + 球列（状态 / 前文）+ 已渲染气泡回填
            gRefreshThemeMap(members);
            gRenderCats(members);
            gPlaceCats();
            gApplyThemeColors();
            if (skipTargets !== true) {
                gFillTargets(members);
            }
            document.title = 'CH4 群聊 · ' + g.name;
        })
        .catch(function () {
            gTitleEl.textContent = '组不可用';
            gInfoEl.textContent = '读取失败——宿主未运行？';
        });
}

// [段2b] 成员球列（A319）——主题色球 + 状态灯点 + 前文 token

// 前文 token 文本——千以上折成 xxx.xK（用户口径）
function gTokText(n) {
    var v = Number(n) || 0;
    if (v >= 1000) {
        return (v / 1000).toFixed(1) + 'K';
    }
    return String(v);
}

// 球内首字——显示名首字符（无显示名回落 key 首字符）
function gCatInitial(name, key) {
    var base = (name && name.length > 0) ? name : (key || '?');
    return base.slice(0, 1);
}

// 单个球节点——只建一次，后续按 key 复用更新（避免每 2s 重建导致 hover 与滚动位置丢失）
function gCreateCatNode(key) {
    var item = document.createElement('div');
    item.className = 'gc-item';
    var crown = document.createElement('span');
    crown.className = 'gc-crown';
    crown.textContent = '👑';
    crown.style.display = 'none';
    var ball = document.createElement('div');
    ball.className = 'gc-ball';
    var initial = document.createElement('span');
    ball.appendChild(initial);
    var dot = document.createElement('span');
    dot.className = 'gc-dot';
    ball.appendChild(dot);
    var name = document.createElement('div');
    name.className = 'gc-name';
    var tok = document.createElement('div');
    tok.className = 'gc-tok';
    item.appendChild(crown);
    item.appendChild(ball);
    item.appendChild(name);
    item.appendChild(tok);
    var node = { item: item, crown: crown, ball: ball, initial: initial, dot: dot, name: name, tok: tok };
    gCatNodes[key] = node;
    return node;
}

// 球列定位——与对话页「全部/精简」开关同一条竖轴（左缘 / 宽由 CSS 取 --ch-col-left 与 --ch-send-w），
// 纵向由本函数按对话区矩形写入：顶部对齐对话区上缘 + 8，高度不越过对话区（fixed，不随对话滚动）
function gPlaceCats() {
    if (!gCatsEl || !gMsgsEl) {
        return;
    }
    var rect = gMsgsEl.getBoundingClientRect();
    gCatsEl.style.top = Math.round(rect.top + 8) + 'px';
    var h = Math.round(rect.height - 16);
    gCatsEl.style.maxHeight = (h > 120 ? h : 120) + 'px';
}

// 球列渲染——管理员在前（同级按成员名单原序），离组成员节点移除
function gRenderCats(members) {
    if (!gCatsEl) {
        return;
    }
    // [段1] 顺序——管理员优先；Array.sort 自 ES2019 起稳定，同级保持名单原序
    var ordered = [];
    for (var n = 0; n < members.length; n = n + 1) {
        var m0 = members[n];
        if (m0 && m0.key) {
            ordered.push(m0);
        }
    }
    ordered.sort(function (a, b) {
        var av = a.admin === true ? 0 : 1;
        var bv = b.admin === true ? 0 : 1;
        return av - bv;
    });
    // [段2] 逐球更新（节点复用，不重建）
    var alive = {};
    for (var i = 0; i < ordered.length; i = i + 1) {
        var m = ordered[i];
        alive[m.key] = true;
        var node = gCatNodes[m.key];
        if (!node) {
            node = gCreateCatNode(m.key);
            gCatsEl.appendChild(node.item);
        }
        var label = (m.displayName && m.displayName.length > 0) ? m.displayName : m.key;
        var idx = Number(m.themeColor) || 0;
        node.item.style.setProperty('--gc-color', catThemeColorValue(idx));
        node.initial.textContent = gCatInitial(m.displayName, m.key);
        node.name.textContent = label;
        node.name.title = label + '（' + m.key + '）';
        node.crown.style.display = m.admin === true ? 'block' : 'none';
        var running = m.running === true;
        var rs = m.runState || 'idle';
        node.item.classList.toggle('off', !running);
        node.dot.className = running ? ('gc-dot ' + rs) : 'gc-dot';
        node.item.classList.toggle('busy', running && rs !== 'idle');
        if (running) {
            node.tok.textContent = gTokText(m.context);
            node.tok.title = '前文 ' + String(Number(m.context) || 0) + ' token';
        } else {
            node.tok.textContent = '—';
            node.tok.title = '猫未运行';
        }
    }
    // [段3] 顺序校正——只在错位时移动（insertBefore 会整段改写 DOM 序，逐帧调用会引起重排抖动）
    var ref = gCatsEl.firstElementChild;
    for (var k = 0; k < ordered.length; k = k + 1) {
        var want = gCatNodes[ordered[k].key].item;
        if (ref === want) {
            ref = want.nextElementSibling;
        } else {
            gCatsEl.insertBefore(want, ref);
        }
    }
    var keys = Object.keys(gCatNodes);
    for (var j = 0; j < keys.length; j = j + 1) {
        if (alive[keys[j]] !== true) {
            var dead = gCatNodes[keys[j]];
            if (dead.item.parentNode) {
                dead.item.parentNode.removeChild(dead.item);
            }
            delete gCatNodes[keys[j]];
        }
    }
}

// 主题色映射——成员名单 → catKey:索引（info 到达时刷新）
function gRefreshThemeMap(members) {
    gThemeIdx = {};
    for (var i = 0; i < members.length; i = i + 1) {
        var m = members[i];
        if (!m || !m.key) {
            continue;
        }
        gThemeIdx[m.key] = Number(m.themeColor) || 0;
    }
}

// 单行着色——主题色写进行内变量（描边取该色；底色不动，保持助手气泡一致）
function gApplyCatTheme(row, fromKey) {
    var idx = 0;
    if (fromKey && Object.prototype.hasOwnProperty.call(gThemeIdx, fromKey)) {
        idx = gThemeIdx[fromKey];
    }
    row.style.setProperty('--gc-color', catThemeColorValue(idx));
}

// 已渲染行回填——组信息晚于消息首屏到达时按最新主题色重刷（不重建行，滚动位置不动）
function gApplyThemeColors() {
    if (!gMsgsEl) {
        return;
    }
    var rows = gMsgsEl.querySelectorAll('.chat-row.assistant[data-from]');
    for (var i = 0; i < rows.length; i = i + 1) {
        gApplyCatTheme(rows[i], rows[i].getAttribute('data-from'));
    }
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
            gHasMore = d.hasMore === true;
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
    if (!isHuman) {
        // A320 猫主题色——行上挂色变量；组信息晚到时由 gApplyThemeColors 回填
        row.setAttribute('data-from', m.from);
        gApplyCatTheme(row, m.from);
    }
    var bubble = document.createElement('div');
    bubble.className = 'chat-bubble';
    bubble.appendChild(gBuildMeta(m, isHuman));
    var md = document.createElement('div');
    md.className = 'md-block';
    md.innerHTML = mdToHtml(m.text || '');
    // 表格 / 代码块复制按钮（与对话页同一件：md.js 出结构，md-copy.js 挂按钮）
    mdBindCopy(md);
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
    who.textContent = gSenderText(m);
    if (isHuman) {
        who.className = 'mm-who';
    } else {
        // A313 @ 快捷填入——点发送方名字只把该猫填进 @ 选择器（纯快捷输入：不发送、不引用）
        who.className = 'mm-who mm-who-pick';
        who.title = '点击填入 @ 目标——只填选择器，不自动发送';
        who.setAttribute('role', 'button');
        who.addEventListener('click', function () {
            gPickTarget(m.from);
        });
    }
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
    meta.appendChild(gBuildCopyBtn(m));
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
    if (gHasMore) {
        var hint = document.createElement('div');
        hint.className = 'chat-plain group-hint';
        hint.textContent = '（仅显示最近 200 条——更早的消息未加载）';
        gMsgsEl.appendChild(hint);
    }
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

// 发送一条消息——选 @ 走派单（delegation，回执自动回组）；不选只记流水（note）
function gSend() {
    var text = gInputEl.value.trim();
    if (text.length === 0) {
        return;
    }
    var to = gToSel.value || '';
    var isDelegation = to.length > 0;
    gSendBtn.disabled = true;
    gMsgEl.textContent = isDelegation ? '派单中…' : '发送中…';
    fetch('/api/v1/group/messages', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: isDelegation ? 'delegation' : 'note', to: to, text: text })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            gSendBtn.disabled = false;
            if (!d.ok) {
                gMsgEl.textContent = '发送失败: ' + (d.error || '');
                return;
            }
            gInputEl.value = '';
            if (d.warning && d.warning.length > 0) {
                gMsgEl.textContent = '已入组，未投递（目标猫未运行）';
            } else if (isDelegation) {
                gMsgEl.textContent = '已派单';
            } else {
                gMsgEl.textContent = '已发送';
            }
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
            // A319——球列状态与前文每轮刷新（静默：不重建 @ 目标下拉，避免打断打开中的选择器）
            gLoadInfo(true);
            if (list.length > 0) {
                gAppendMessages(list);
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

// 成员名册文本——顶栏 title（姓名（key）+ 角色，一行一人）
function gMemberRoster(members) {
    if (members.length === 0) {
        return '（无成员）';
    }
    var lines = [];
    for (var i = 0; i < members.length; i = i + 1) {
        var m = members[i];
        if (!m || !m.key) {
            continue;
        }
        var role = '';
        if (m.admin) {
            role = role + ' 管理员';
        }
        if (m.subscriber) {
            role = role + ' 订阅';
        }
        lines.push((m.displayName || m.key) + '（' + m.key + '）' + role);
    }
    return lines.join('\n');
}

// @ 目标下拉——本组成员（★ 管理员 / 🔔 订阅）；首项「无 @」= 只记流水
function gFillTargets(members) {
    var keep = gToSel.value;
    gToSel.textContent = '';
    var none = document.createElement('option');
    none.value = '';
    none.textContent = '无 @（只记流水，订阅渠道推送）';
    gToSel.appendChild(none);
    for (var i = 0; i < members.length; i = i + 1) {
        var m = members[i];
        if (!m || !m.key) {
            continue;
        }
        var opt = document.createElement('option');
        opt.value = m.key;
        var prefix = '';
        if (m.admin) {
            prefix = '★ ';
        }
        if (m.subscriber) {
            prefix = prefix + '🔔 ';
        }
        opt.textContent = prefix + (m.displayName || m.key) + '（' + m.key + '）';
        gToSel.appendChild(opt);
    }
    gToSel.value = keep;
}

// [段6] 清空——勾选确认后归档旧流并重置（新流 #1 起）

// 清空本组消息流（旧流原地归档为 .bak；订阅游标随服务端一并归零）
function gClearMessages() {
    if (!gClearConfirm.checked) {
        gMsgEl.textContent = '请先勾选「确认清空」';
        return;
    }
    gClearConfirm.checked = false;
    gClearBtn.disabled = true;
    gMsgEl.textContent = '清空中…';
    fetch('/api/v1/group/clear', { method: 'POST' })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                gMsgEl.textContent = '清空失败: ' + (d.error || '');
                return;
            }
            gLastSeq = 0;
            gHasMore = false;
            gMsgsEl.textContent = '';
            gMsgEl.textContent = '已清空（旧消息已归档）';
            gLoadInfo();
            gLoadMessages();
        })
        .catch(function (e) {
            gMsgEl.textContent = '清空失败: ' + e;
        });
}

// 勾选态联动按钮可用性
function gSyncClearState() {
    gClearBtn.disabled = !gClearConfirm.checked;
}

document.getElementById('chatRefresh').onclick = function () {
    gLoadInfo();
    gLoadMessages();
};
gSendBtn.onclick = gSend;
gClearBtn.onclick = gClearMessages;
gClearConfirm.onchange = gSyncClearState;
gInputEl.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        gSend();
    }
});
document.addEventListener('visibilitychange', gOnVisibilityChange);

// 引导——版本标记 / @ 目标（随组信息填充）/ 组信息 / 消息流 / 自绘滚动带接线
loadUiVersion();
gLoadInfo();
gLoadMessages();
chatScrollInit();
gPlaceCats();
window.addEventListener('resize', gPlaceCats);

// [段7] 消息操作（A313）——@ 快捷填入 · 单条复制（纯前端，无业务语义）

// 点某条猫消息的发送方名字——把该猫填进 @ 目标选择器（唯一语义：快捷输入，不发送、不引用）
function gPickTarget(key) {
    if (!key) {
        return;
    }
    var hit = '';
    for (var i = 0; i < gToSel.options.length; i = i + 1) {
        if (gToSel.options[i].value === key) {
            hit = key;
            break;
        }
    }
    if (hit === '') {
        gMsgEl.textContent = '该发送方不在组内成员，无法 @';
        return;
    }
    gToSel.value = hit;
    gMsgEl.textContent = '已填入 @' + key;
    gInputEl.focus();
}

// 复制一条消息的原文——纯前端剪贴板（写入通道复用 md-copy：127.0.0.1 / localhost 属安全上下文）
function gCopyMessage(text, btn) {
    mdWriteClipboard(text || '').then(function () {
        btn.classList.add('done');
        window.setTimeout(function () { btn.classList.remove('done'); }, 1200);
    }).catch(function (e) {
        gMsgEl.textContent = '复制失败: ' + (e && e.message ? e.message : e);
    });
}

// 单条复制按钮——线条图标（复用 mdCopyIcon：零文本节点，不污染 textContent 断言与朗读）
function gBuildCopyBtn(m) {
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'msg-copy-btn';
    btn.title = '复制本条原文';
    btn.setAttribute('aria-label', '复制本条原文');
    btn.appendChild(mdCopyIcon());
    btn.addEventListener('click', function () {
        gCopyMessage(m.text, btn);
    });
    return btn;
}
