// CH4 外观层——panel-qqbot.js：QQ 机器人配置池管理（config 页签，A379）——panel-appearance.js 同构
// 依赖：app.js + panel.js 先加载（全局状态已就位）；本文件只承载机器人池的列表/新建/编辑/删除/设默认
// 一条配置 = 名称 + AppID + 接入环境（sandbox / production）；AppSecret 落 Data/secrets/qqbot.cfg，读面只回掩码

// [段1] QQ 机器人配置池管理
var qbcTableBody = document.querySelector('#qbcTable tbody');
var qbcMsgEl = document.getElementById('qbcMsg');
var editingQbcId = '';

function loadQqBots() {
    fetch('/api/v1/qqbot-apis')
        .then(function (r) { return r.json(); })
        .then(function (d) { renderQqBots(d.items || []); })
        .catch(function () { qbcMsgEl.textContent = 'QQ 机器人池加载失败——宿主未运行？'; });
}

function renderQqBots(items) {
    qbcTableBody.textContent = '';
    qbcMsgEl.textContent = items.length + ' 个机器人';
    for (var i = 0; i < items.length; i++) {
        renderQqBotRow(items[i]);
    }
}

function qqEnvLabel(env) {
    if (env === 'sandbox') { return '沙箱'; }
    if (env === 'production') { return '正式'; }
    return env || '（未设置）';
}

function renderQqBotRow(bot) {
    var tr = document.createElement('tr');
    var tdName = document.createElement('td');
    tdName.textContent = bot.displayName || '';
    tdName.style.color = 'var(--ch-identity)';
    if (bot.isDefault) { tdName.textContent = '★ ' + tdName.textContent; }
    tr.appendChild(tdName);
    var tdAppId = document.createElement('td');
    tdAppId.textContent = bot.appId || '';
    tdAppId.style.fontSize = 'var(--ch-fs-tag)';
    tdAppId.style.wordBreak = 'break-all';
    tr.appendChild(tdAppId);
    var tdEnv = document.createElement('td');
    tdEnv.textContent = qqEnvLabel(bot.apiEnv);
    tdEnv.style.color = bot.apiEnv === 'sandbox' ? 'var(--ch-warn, #d1a44a)' : 'var(--ch-fg-muted)';
    tr.appendChild(tdEnv);
    var tdSecret = document.createElement('td');
    tdSecret.textContent = bot.hasSecret ? (bot.appSecret || '已配置') : '（未配置）';
    tdSecret.style.color = bot.hasSecret ? 'var(--ch-ok-weak)' : 'var(--ch-fg-weak)';
    tdSecret.style.fontSize = 'var(--ch-fs-tag)';
    tr.appendChild(tdSecret);
    var tdOp = document.createElement('td');
    tdOp.style.whiteSpace = 'nowrap';
    if (!bot.isDefault) {
        var defBtn = document.createElement('button');
        defBtn.textContent = '设为默认';
        defBtn.className = 'btn-mini accent';
        defBtn.onclick = function () { setDefaultQqBot(bot); };
        tdOp.appendChild(defBtn);
    }
    var editBtn = document.createElement('button');
    editBtn.textContent = '编辑';
    editBtn.className = 'btn-mini';
    editBtn.onclick = function () { startQqBotEdit(bot); };
    tdOp.appendChild(editBtn);
    var delBtn = document.createElement('button');
    delBtn.textContent = '删除';
    delBtn.className = 'btn-mini danger';
    delBtn.onclick = function () {
        if (!confirm('删除机器人配置「' + bot.displayName + '」？对应的 AppSecret 一并删除。')) { return; }
        fetch('/api/v1/qqbot-apis/delete', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ qqBotId: bot.qqBotId })
        })
            .then(function (r) { return r.json(); })
            .then(function (d) {
                qbcMsgEl.textContent = d.ok ? '已删除' : '删除失败: ' + d.error;
                loadQqBots();
            })
            .catch(function (e) {
                qbcMsgEl.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）';
            });
    };
    tdOp.appendChild(delBtn);
    tr.appendChild(tdOp);
    qbcTableBody.appendChild(tr);
}

function startQqBotEdit(bot) {
    editingQbcId = bot.qqBotId;
    document.getElementById('qbcNewName').value = bot.displayName || '';
    document.getElementById('qbcNewAppId').value = bot.appId || '';
    document.getElementById('qbcNewEnv').value = bot.apiEnv || 'production';
    document.getElementById('qbcNewSecret').value = '';
    document.getElementById('qbcNewSecret').placeholder = bot.hasSecret ? 'AppSecret（空=保留原值）' : 'AppSecret';
    document.getElementById('qbcAddBtn').textContent = '保存';
    document.getElementById('qbcCancelEdit').style.display = '';
    qbcMsgEl.textContent = '编辑中: ' + bot.displayName;
}

function cancelQqBotEdit() {
    editingQbcId = '';
    document.getElementById('qbcNewName').value = '';
    document.getElementById('qbcNewAppId').value = '';
    document.getElementById('qbcNewEnv').value = 'production';
    document.getElementById('qbcNewSecret').value = '';
    document.getElementById('qbcNewSecret').placeholder = 'AppSecret';
    document.getElementById('qbcAddBtn').textContent = '新建';
    document.getElementById('qbcCancelEdit').style.display = 'none';
    qbcMsgEl.textContent = '';
}

function qqBotSubmit() {
    var payload = {
        displayName: document.getElementById('qbcNewName').value.trim(),
        appId: document.getElementById('qbcNewAppId').value.trim(),
        apiEnv: document.getElementById('qbcNewEnv').value,
        appSecret: document.getElementById('qbcNewSecret').value.trim()
    };
    var url = '/api/v1/qqbot-apis';
    if (editingQbcId.length > 0) {
        url = '/api/v1/qqbot-apis/edit';
        payload.qqBotId = editingQbcId;
    }
    fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            qbcMsgEl.textContent = d.ok ? (editingQbcId.length > 0 ? '已保存' : '已新建') : '失败: ' + d.error;
            cancelQqBotEdit();
            loadQqBots();
        })
        .catch(function (e) {
            qbcMsgEl.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）';
        });
}

// 设为默认——POST /api/v1/qqbot-apis/default（消费面按默认标记解析机器人）
function setDefaultQqBot(bot) {
    fetch('/api/v1/qqbot-apis/default', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ qqBotId: bot.qqBotId })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            qbcMsgEl.textContent = d.ok ? '默认机器人已切换: ' + bot.displayName : '失败: ' + d.error;
            loadQqBots();
        })
        .catch(function (e) {
            qbcMsgEl.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）';
        });
}

document.getElementById('qbcAddBtn').onclick = qqBotSubmit;
document.getElementById('qbcRefresh').onclick = loadQqBots;
document.getElementById('qbcCancelEdit').onclick = cancelQqBotEdit;
loadQqBots();

// [段2] QQ 群目录（A380）——机器人收到群消息即自动登记；别名供工具寻址；群名与权限走官方接口
var qgcTableBody = document.querySelector('#qgcTable tbody');
var qgcMsgEl = document.getElementById('qgcMsg');
var qgcBotSel = document.getElementById('qgcBot');

// 填机器人下拉——池内每条配置一项（qqBotId 作为端点主键）
function loadQqGroupBots(onDone) {
    fetch('/api/v1/qqbot-apis')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            var items = d.items || [];
            var keep = qgcBotSel.value;
            qgcBotSel.textContent = '';
            var i;
            for (i = 0; i < items.length; i = i + 1) {
                var op = document.createElement('option');
                op.value = items[i].qqBotId;
                op.textContent = items[i].displayName + (items[i].isDefault ? ' ★' : '') + '（' + items[i].appId + '）';
                qgcBotSel.appendChild(op);
            }
            if (keep.length > 0) { qgcBotSel.value = keep; }
            if (onDone) { onDone(); }
        })
        .catch(function (e) { qgcMsgEl.textContent = '读取机器人池失败: ' + e.message; });
}

// 拉群目录——GET /api/v1/qqbot-groups?botKey=
function loadQqGroups() {
    var key = qgcBotSel.value || '';
    fetch('/api/v1/qqbot-groups?botKey=' + encodeURIComponent(key), { cache: 'no-store' })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                qgcMsgEl.textContent = d.error || '读取失败';
                qgcTableBody.textContent = '';
                return;
            }
            renderQqGroups(d);
        })
        .catch(function (e) { qgcMsgEl.textContent = '读取失败: ' + e.message; });
}

// 收信范围转人话
function qgcRecvLabel(v) {
    if (v === 'all') { return '全部'; }
    if (v === 'only_mention') { return '仅 @'; }
    if (v === 'mention_and_context') { return '@ + 上下文'; }
    return v || '（未取到）';
}

// 渲染群目录——群名 / 别名 / OpenID / 角色 / 收信 / 主动发言 / 最近活跃
function renderQqGroups(d) {
    var groups = d.groups || [];
    qgcTableBody.textContent = '';
    qgcMsgEl.textContent = d.bot + ' · ' + groups.length + ' 个群';
    var i;
    for (i = 0; i < groups.length; i = i + 1) {
        var g = groups[i];
        var tr = document.createElement('tr');
        var tdTitle = document.createElement('td');
        tdTitle.textContent = g.title || '（未取到群名）';
        tdTitle.style.color = g.title ? 'var(--ch-identity)' : 'var(--ch-fg-weak)';
        if (g.lastRefreshFail && g.lastRefreshFail.length > 0) {
            // A384——刷新失败的群标未确认（旧权限值已被清空，可能已退群或接口无权限）
            var unk = document.createElement('span');
            unk.textContent = ' ⚠️ 未确认';
            unk.style.color = 'var(--ch-warn, #d1a44a)';
            unk.title = '刷新失败于 ' + g.lastRefreshFail + '——可能已退群或接口无权限，可用「移除」清理';
            tdTitle.appendChild(unk);
        }
        tr.appendChild(tdTitle);
        var tdAlias = document.createElement('td');
        tdAlias.textContent = g.alias || '';
        tdAlias.style.color = g.alias ? 'var(--ch-ok)' : 'var(--ch-fg-weak)';
        tr.appendChild(tdAlias);
        var tdOpen = document.createElement('td');
        tdOpen.textContent = g.openId || '';
        tdOpen.style.fontSize = 'var(--ch-fs-tag)';
        tdOpen.style.wordBreak = 'break-all';
        tr.appendChild(tdOpen);
        var tdRole = document.createElement('td');
        tdRole.textContent = g.role || '（未取到）';
        tr.appendChild(tdRole);
        var tdRecv = document.createElement('td');
        tdRecv.textContent = qgcRecvLabel(g.recvSetting);
        tr.appendChild(tdRecv);
        var tdPro = document.createElement('td');
        tdPro.textContent = g.allowProactive ? '允许' : '未知/关闭';
        tdPro.style.color = g.allowProactive ? 'var(--ch-ok)' : 'var(--ch-fg-weak)';
        tr.appendChild(tdPro);
        var tdSeen = document.createElement('td');
        tdSeen.textContent = g.lastSeen || '';
        tdSeen.style.fontSize = 'var(--ch-fs-tag)';
        tr.appendChild(tdSeen);
        var tdOp = document.createElement('td');
        tdOp.style.whiteSpace = 'nowrap';
        var aliasBtn = document.createElement('button');
        aliasBtn.textContent = '设别名';
        aliasBtn.className = 'btn-mini';
        aliasBtn.onclick = function (entry) {
            return function () {
                var next = prompt('给群「' + (entry.title || entry.openId) + '」设别名（留空=清除）：', entry.alias || '');
                if (next === null) { return; }
                qgcPost('/api/v1/qqbot-groups/alias', { botKey: qgcBotSel.value, groupRef: entry.openId, alias: next });
            };
        }(g);
        tdOp.appendChild(aliasBtn);
        var removeBtn = document.createElement('button');
        removeBtn.textContent = '移除';
        removeBtn.className = 'btn-mini danger';
        removeBtn.onclick = function (entry) {
            return function () {
                if (!confirm('从目录移除群「' + (entry.title || entry.openId) + '」？\n只清本地目录，不影响平台侧机器人归属；移除后不能再被寻址，重新收到消息或用「登记」可再入目录。')) { return; }
                qgcPost('/api/v1/qqbot-groups/remove', { botKey: qgcBotSel.value, groupRef: entry.openId });
            };
        }(g);
        tdOp.appendChild(removeBtn);
        tr.appendChild(tdOp);
        qgcTableBody.appendChild(tr);
    }
}

// 目录写操作公共段
function qgcPost(url, payload) {
    fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            qgcMsgEl.textContent = d.ok ? (d.text || '完成') : ('失败: ' + (d.error || ''));
            loadQqGroups();
        })
        .catch(function (e) { qgcMsgEl.textContent = '请求失败: ' + e.message; });
}

// 刷官方权限（逐群 bot_state + info；受平台白名单限制，取不到时降级本地观测）
function refreshQqGroupsOfficial() {
    qgcMsgEl.textContent = '刷新中…（逐群调官方接口，可能数秒）';
    qgcPost('/api/v1/qqbot-groups/refresh', { botKey: qgcBotSel.value });
}

document.getElementById('qgcRefreshList').onclick = loadQqGroups;
document.getElementById('qgcRefreshOff').onclick = refreshQqGroupsOfficial;
document.getElementById('qgcBot').onchange = loadQqGroups;
document.getElementById('qgcAddBtn').onclick = function () {
    var openId = document.getElementById('qgcAddOpenId').value.trim();
    if (openId.length === 0) { qgcMsgEl.textContent = '请填写群 OpenID'; return; }
    qgcPost('/api/v1/qqbot-groups/add', { botKey: qgcBotSel.value, groupRef: openId });
    document.getElementById('qgcAddOpenId').value = '';
};
loadQqGroupBots(loadQqGroups);
