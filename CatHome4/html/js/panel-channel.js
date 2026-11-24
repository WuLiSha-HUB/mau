// CH4 外观层——panel-channel.js：HTTP 注入通道池管理（config 页签，A405）——panel-qqbot.js 同构
// 依赖：app.js + panel.js 先加载（全局状态已就位）；本文件只承载通道池的列表 / 新建 / 编辑 / 重置 Key / 删除
// 一条通道 = 通道名 + 绑定猫（1:1）；Key 明文只在新建 / 重置的当次回执出现（列表只回掩码）

// [段1] 通道池管理
var chnTableBody = document.querySelector('#chnTable tbody');
var chnMsgEl = document.getElementById('chnMsg');
var chnCatSel = document.getElementById('chnNewCat');
var chnBotSel = document.getElementById('chnNewBot');
var chnGroupsEl = document.getElementById('chnNewGroups');
var editingChnId = '';

function loadChannels() {
    fetch('/api/v1/channels')
        .then(function (r) { return r.json(); })
        .then(function (d) { renderChannels(d.items || []); })
        .catch(function () { chnMsgEl.textContent = '通道池加载失败——宿主未运行？'; });
}

function renderChannels(items) {
    chnTableBody.textContent = '';
    chnMsgEl.textContent = items.length + ' 个通道';
    for (var i = 0; i < items.length; i = i + 1) {
        renderChannelRow(items[i]);
    }
}

function renderChannelRow(ch) {
    var tr = document.createElement('tr');
    var tdName = document.createElement('td');
    tdName.textContent = ch.displayName || '';
    tdName.style.color = 'var(--ch-identity)';
    tr.appendChild(tdName);
    var tdCat = document.createElement('td');
    if (!ch.catKey) {
        // A406——纯播报通道：没有注入目标
        tdCat.textContent = '（不接注入）';
        tdCat.style.color = 'var(--ch-fg-weak)';
    } else {
        var catLabel = ch.catName || '（猫不存在）';
        tdCat.textContent = catLabel;
        tdCat.style.color = ch.catName ? 'var(--ch-fg)' : 'var(--ch-err, #c86a6a)';
    }
    if (ch.catKey) {
        var keyHint = document.createElement('span');
        keyHint.textContent = ' · ' + ch.catKey;
        keyHint.style.color = 'var(--ch-fg-faint)';
        keyHint.style.fontSize = 'var(--ch-fs-tag)';
        tdCat.appendChild(keyHint);
    }
    tr.appendChild(tdCat);
    // [段1.1] 出口列——A406：有出口机器人 + 白名单才算可播报（空白名单 = 禁发，如实显示）
    var tdExit = document.createElement('td');
    tdExit.style.fontSize = 'var(--ch-fs-tag)';
    if (ch.hasExit) {
        var botLabel = ch.botName || '（机器人不在池内）';
        tdExit.textContent = botLabel + ' · ' + ((ch.groups || []).length) + ' 群';
        tdExit.style.color = ch.botName ? 'var(--ch-fg)' : 'var(--ch-err, #c86a6a)';
        tdExit.title = (ch.groups || []).join(' / ') || '白名单为空 = 禁发';
    } else {
        tdExit.textContent = '仅注入';
        tdExit.style.color = 'var(--ch-fg-weak)';
    }
    tr.appendChild(tdExit);
    var tdId = document.createElement('td');
    tdId.textContent = shortChannelId(ch.channelId);
    tdId.title = ch.channelId || '';
    tdId.style.fontSize = 'var(--ch-fs-tag)';
    tdId.style.color = 'var(--ch-fg-faint)';
    tr.appendChild(tdId);
    var tdKey = document.createElement('td');
    tdKey.textContent = ch.hasKey ? (ch.key || '已配置') : '（未配置）';
    tdKey.style.color = ch.hasKey ? 'var(--ch-ok-weak)' : 'var(--ch-fg-weak)';
    tdKey.style.fontSize = 'var(--ch-fs-tag)';
    tdKey.style.wordBreak = 'break-all';
    tr.appendChild(tdKey);
    var tdOp = document.createElement('td');
    tdOp.style.whiteSpace = 'nowrap';
    var editBtn = document.createElement('button');
    editBtn.textContent = '编辑';
    editBtn.className = 'btn-mini';
    editBtn.onclick = function () { startChannelEdit(ch); };
    tdOp.appendChild(editBtn);
    var rotateBtn = document.createElement('button');
    rotateBtn.textContent = '重置 Key';
    rotateBtn.className = 'btn-mini accent';
    rotateBtn.onclick = function () { rotateChannelKey(ch); };
    tdOp.appendChild(rotateBtn);
    var delBtn = document.createElement('button');
    delBtn.textContent = '删除';
    delBtn.className = 'btn-mini danger';
    delBtn.onclick = function () {
        if (!confirm('删除通道「' + ch.displayName + '」？其 Key 立即失效，外部程序将无法再注入这只猫。')) { return; }
        fetch('/api/v1/channels/delete', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ channelId: ch.channelId })
        })
            .then(function (r) { return r.json(); })
            .then(function (d) {
                chnMsgEl.textContent = d.ok ? '已删除' : '删除失败: ' + d.error;
                loadChannels();
            })
            .catch(function (e) { chnMsgEl.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）'; });
    };
    tdOp.appendChild(delBtn);
    tr.appendChild(tdOp);
    chnTableBody.appendChild(tr);
}

// 通道 ID 显示——取 UUID 前 8 位（完整值挂 title，点击不需要）
function shortChannelId(id) {
    var text = id || '';
    if (text.length <= 8) { return text; }
    return text.substring(0, 8);
}

// 绑定猫下拉——数据源 GET /api/v1/cats（与多猫区同源；特殊会话 majordomo 不在该列表内，如需绑定走 HTTP 端点）
function loadChannelCats(onDone) {
    fetch('/api/v1/cats')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            var cats = d.cats || [];
            var keep = chnCatSel.value;
            chnCatSel.textContent = '';
            // A406——绑定猫可空：纯播报通道不接注入
            var noneCatOp = document.createElement('option');
            noneCatOp.value = '';
            noneCatOp.textContent = '（不接注入——仅播报）';
            chnCatSel.appendChild(noneCatOp);
            for (var i = 0; i < cats.length; i = i + 1) {
                var op = document.createElement('option');
                op.value = cats[i].id || '';
                op.textContent = (cats[i].name || cats[i].id || '') + '（' + shortChannelId(cats[i].id) + '）';
                chnCatSel.appendChild(op);
            }
            if (keep.length > 0) { chnCatSel.value = keep; }
            if (onDone) { onDone(); }
        })
        .catch(function (e) { chnMsgEl.textContent = '读取猫列表失败: ' + e.message; });
}

// [段1.2] 出口机器人下拉——A406：数据源 GET /api/v1/qqbot-apis（QQ 机器人池）；首项「（不接出口）」
function loadChannelBots(onDone) {
    fetch('/api/v1/qqbot-apis')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            var bots = d.items || [];
            var keep = chnBotSel.value;
            chnBotSel.textContent = '';
            var noneOp = document.createElement('option');
            noneOp.value = '';
            noneOp.textContent = '（不接出口）';
            chnBotSel.appendChild(noneOp);
            for (var i = 0; i < bots.length; i = i + 1) {
                var op = document.createElement('option');
                op.value = bots[i].qqBotId || '';
                op.textContent = (bots[i].displayName || bots[i].appId || '') + (bots[i].isDefault ? ' ★' : '');
                chnBotSel.appendChild(op);
            }
            if (keep.length > 0) { chnBotSel.value = keep; }
            if (onDone) { onDone(); }
        })
        .catch(function (e) { chnMsgEl.textContent = '读取机器人池失败: ' + e.message; });
}

// 群白名单文本 → 数组（中英文逗号 / 分号分隔；去空白项）
function splitChannelGroups(text) {
    var raw = (text || '').replace(/，/g, ',').replace(/；/g, ',').replace(/;/g, ',');
    var parts = raw.split(',');
    var result = [];
    for (var i = 0; i < parts.length; i = i + 1) {
        var item = parts[i].trim();
        if (item.length > 0) { result.push(item); }
    }
    return result;
}

function startChannelEdit(ch) {
    editingChnId = ch.channelId;
    document.getElementById('chnNewName').value = ch.displayName || '';
    chnCatSel.value = ch.catKey || '';
    chnBotSel.value = ch.botKey || '';
    chnGroupsEl.value = (ch.groups || []).join(', ');
    document.getElementById('chnAddBtn').textContent = '保存';
    document.getElementById('chnCancelEdit').style.display = '';
    chnMsgEl.textContent = '编辑中: ' + ch.displayName + '（Key 不变）';
}

function cancelChannelEdit() {
    editingChnId = '';
    document.getElementById('chnNewName').value = '';
    chnBotSel.value = '';
    chnGroupsEl.value = '';
    document.getElementById('chnAddBtn').textContent = '新建';
    document.getElementById('chnCancelEdit').style.display = 'none';
    chnMsgEl.textContent = '';
}

function channelSubmit() {
    var payload = {
        displayName: document.getElementById('chnNewName').value.trim(),
        catKey: chnCatSel.value || '',
        botKey: chnBotSel.value || '',
        groups: splitChannelGroups(chnGroupsEl.value)
    };
    if (payload.displayName.length === 0) { chnMsgEl.textContent = '请填通道名'; return; }
    if (payload.catKey.length === 0 && payload.botKey.length === 0) { chnMsgEl.textContent = '至少选一项能力：绑定猫（注入）或出口机器人（播报）'; return; }
    var url = '/api/v1/channels';
    var creating = editingChnId.length === 0;
    if (!creating) {
        url = '/api/v1/channels/edit';
        payload.channelId = editingChnId;
    }
    fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                chnMsgEl.textContent = '失败: ' + d.error;
                return;
            }
            chnMsgEl.textContent = creating ? '已新建' : '已保存';
            cancelChannelEdit();
            loadChannels();
            // 新建回执带明文 Key——只在这一次显示
            if (creating && d.key) { showChannelKey('新建', payload.displayName, d.key); }
        })
        .catch(function (e) { chnMsgEl.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）'; });
}

// 重置 Key——旧 Key 立即失效；新 Key 明文仅本次显示
function rotateChannelKey(ch) {
    if (!confirm('重置通道「' + ch.displayName + '」的 Key？旧 Key 立即失效，外部程序需换新 Key。')) { return; }
    fetch('/api/v1/channels/rotate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ channelId: ch.channelId })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                chnMsgEl.textContent = '重置失败: ' + d.error;
                return;
            }
            chnMsgEl.textContent = 'Key 已重置: ' + ch.displayName;
            loadChannels();
            showChannelKey('重置', ch.displayName, d.key);
        })
        .catch(function (e) { chnMsgEl.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）'; });
}

// [段2] Key 一次性展示弹层——关闭即消失（列表只回掩码，重拿走「重置 Key」）
function showChannelKey(action, name, key) {
    document.getElementById('chnKeyTitle').textContent = name + ' · ' + action;
    document.getElementById('chnKeyValue').value = key || '';
    document.getElementById('chnKeyModal').style.display = 'flex';
    chnMsgEl.textContent = 'Key 已显示——关闭弹层后只能看到掩码';
}

function closeChannelKey() {
    document.getElementById('chnKeyValue').value = '';
    document.getElementById('chnKeyModal').style.display = 'none';
}

function copyChannelKey() {
    var input = document.getElementById('chnKeyValue');
    var text = input.value || '';
    if (text.length === 0) { return; }
    if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text)
            .then(function () { chnMsgEl.textContent = 'Key 已复制到剪贴板'; })
            .catch(function () { chnMsgEl.textContent = '复制失败——请手动选中后复制'; });
        return;
    }
    input.select();
    chnMsgEl.textContent = '已选中——请按 Ctrl+C 复制';
}

document.getElementById('chnAddBtn').onclick = channelSubmit;
document.getElementById('chnRefresh').onclick = loadChannels;
document.getElementById('chnCancelEdit').onclick = cancelChannelEdit;
document.getElementById('chnKeyClose').onclick = closeChannelKey;
document.getElementById('chnKeyDone').onclick = closeChannelKey;
document.getElementById('chnKeyCopy').onclick = copyChannelKey;
loadChannels();
loadChannelCats();
loadChannelBots();
