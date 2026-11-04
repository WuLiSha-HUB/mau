// CH4 外观层——panel-group.js：配队（Group）管理页（A300）
// 落位：侧栏「配队」页签（#tab-group）——组卡片列表 + 新建 / 配置 / 打开 / 启停 / 删除
// 数据面：GET /api/v1/groups · POST /api/v1/groups · /groups/enabled · /groups/delete · /groups/{name}/messages
// 语义：成员按 cat key 落盘，显示名只是附属（可改，不作为主键）；卡片布局复用多猫卡片类

var groupRows = [];        // 最近一次组列表（配置弹层数据源）
var groupCfgEditing = '';  // 正在编辑的组名（空 = 新建）

var groupCardsEl = document.getElementById('groupCards');
var groupMsgEl = document.getElementById('groupMsg');

// [段1] 通用辅助——时间 / 类型标签 / 弹层显隐 / POST 封装

// 时刻文本——unix 毫秒；优先复用 app.js 的 fmtAgo（相对时间），缺省回落本地时间串
function groupTimeText(unixMs) {
    var ms = Number(unixMs) || 0;
    if (ms <= 0) {
        return '-';
    }
    if (typeof fmtAgo === 'function') {
        return fmtAgo(ms);
    }
    return new Date(ms).toLocaleString();
}

// 消息类型标签——delegation 派单 / receipt 回执 / note 其他
function groupTypeLabel(type) {
    if (type === 'delegation') {
        return '派单';
    }
    if (type === 'receipt') {
        return '回执';
    }
    return '其他';
}

// 弹层显隐——flex / none
function groupShowModal(id, visible) {
    var el = document.getElementById(id);
    if (!el) {
        return;
    }
    el.style.display = visible ? 'flex' : 'none';
}

// POST 封装——统一 JSON body 与失败提示
function groupPost(url, payload, cb) {
    fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(cb)
        .catch(function (err) { groupMsgEl.textContent = '请求失败: ' + err; });
}

// [段2] 组列表与卡片

// 组列表加载——页面常驻拉取（与加载包池同构）
function loadGroups() {
    fetch('/api/v1/groups', { cache: 'no-store' })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                groupMsgEl.textContent = '读取失败: ' + (d.error || '');
                return;
            }
            applyGroups(d.groups || []);
        })
        .catch(function () {
            groupMsgEl.textContent = '读取失败——宿主未运行？';
        });
}

// 组列表渲染——全量重建（组数量少，不做增量池）
function applyGroups(groups) {
    groupRows = groups;
    groupCardsEl.textContent = '';
    if (groups.length === 0) {
        groupMsgEl.textContent = '暂无配队——点「新建配队」创建';
        return;
    }
    groupMsgEl.textContent = groups.length + ' 个配队';
    for (var i = 0; i < groups.length; i = i + 1) {
        groupCardsEl.appendChild(createGroupCard(groups[i]));
    }
}

// 按钮构造——文案 + 类名 + 点击
function groupBtn(text, className, onclick) {
    var btn = document.createElement('button');
    btn.textContent = text;
    btn.className = className;
    btn.onclick = onclick;
    return btn;
}

// 成员 chip 渲染——显示名主、key 副（key 是落盘主键）；管理员 ★ / 订阅 🔔 标记
function appendMemberChips(container, members, adminKeys, subscriberKeys) {
    var list = members || [];
    var admins = adminKeys || [];
    var subs = subscriberKeys || [];
    if (list.length === 0) {
        var none = document.createElement('span');
        none.style.cssText = 'color:var(--ch-fg-weak);font-size:var(--ch-fs-tag)';
        none.textContent = '（无成员）';
        container.appendChild(none);
        return;
    }
    for (var i = 0; i < list.length; i = i + 1) {
        var chip = document.createElement('span');
        chip.style.cssText = 'display:inline-flex;align-items:baseline;gap:5px;background:var(--ch-bg-raised);border:1px solid var(--ch-line);border-radius:10px;padding:2px 8px;font-size:var(--ch-fs-tag)';
        var name = document.createElement('span');
        name.style.color = 'var(--ch-fg)';
        var text = list[i].displayName || list[i].key;
        if (admins.indexOf(list[i].key) >= 0) {
            text = '★ ' + text;
        }
        if (subs.indexOf(list[i].key) >= 0) {
            text = text + ' 🔔';
        }
        name.textContent = text;
        chip.appendChild(name);
        var keyEl = document.createElement('span');
        keyEl.style.color = 'var(--ch-fg-faint)';
        keyEl.textContent = list[i].key;
        chip.appendChild(keyEl);
        container.appendChild(chip);
    }
}

// 单组卡片——布局复用多猫卡片（cat-card / is-running 启用底图）
function createGroupCard(g) {
    // [段1] 头部——组名 + 状态
    var card = document.createElement('div');
    card.className = 'cat-card' + (g.running ? ' is-running' : '');
    var head = document.createElement('div');
    head.className = 'cat-head';
    var name = document.createElement('span');
    name.className = 'cat-name';
    name.textContent = g.name;
    head.appendChild(name);
    var status = document.createElement('span');
    status.className = 'cat-status';
    var state = document.createElement('span');
    state.className = g.enabled ? 'cat-runstate' : 'cat-runstate idle';
    if (g.running) {
        state.textContent = '运行中';
    } else if (g.enabled) {
        state.textContent = '未就绪';
    } else {
        state.textContent = '已关闭';
    }
    status.appendChild(state);
    var dot = document.createElement('span');
    dot.className = 'cat-dot' + (g.running ? ' on' : '');
    status.appendChild(dot);
    head.appendChild(status);
    card.appendChild(head);
    // [段2] 信息行——项目 + 创建时刻
    var meta = document.createElement('div');
    meta.className = 'cat-meta';
    var proj = document.createElement('span');
    proj.className = 'cat-id';
    proj.textContent = g.project && g.project.length > 0 ? ('项目 ' + g.project) : '未绑定项目';
    meta.appendChild(proj);
    var portEl = document.createElement('span');
    portEl.className = 'cat-port';
    if (g.running && g.port > 0) {
        portEl.textContent = '端口 ' + g.port;
    } else {
        portEl.textContent = '未启动';
    }
    meta.appendChild(portEl);
    card.appendChild(meta);
    // [段3] 成员
    var members = document.createElement('div');
    members.style.cssText = 'display:flex;flex-wrap:wrap;gap:5px;margin-top:8px';
    appendMemberChips(members, g.members, g.adminKeys, g.subscriberKeys);
    card.appendChild(members);
    // [段4] 消息统计——消息数 / 最新时刻
    var foot = document.createElement('div');
    foot.className = 'cat-foot';
    var line = document.createElement('div');
    line.className = 'cat-foot-line';
    var lastText = '无消息';
    if (g.lastSeq > 0) {
        lastText = groupTimeText(g.lastAt);
    }
    line.textContent = '消息 ' + (g.msgCount || 0) + ' 条 · 最新 ' + lastText;
    foot.appendChild(line);
    card.appendChild(foot);
    // [段5] 操作行——启动/关闭（启用态）· 打开 · 配置 · 删除
    var ops = document.createElement('div');
    ops.className = 'cat-ops';
    if (g.running) {
        ops.appendChild(groupBtn('关闭', 'btn-mini', function () { toggleGroup(g.name, false); }));
    } else {
        ops.appendChild(groupBtn('启动', 'btn-mini primary', function () { toggleGroup(g.name, true); }));
    }
    if (g.running) {
        ops.appendChild(groupBtn('打开', 'btn-mini primary', function () { openGroupChat(g.name); }));
    }
    ops.appendChild(groupBtn('配置', 'btn-mini', function () { openGroupCfg(g.name); }));
    ops.appendChild(groupBtn('删除', 'btn-mini danger', function () { deleteGroup(g); }));
    card.appendChild(ops);
    return card;
}

// 组启停——P2 语义：组启用标记（停用不参与派单与巡检；宿主起停归 P3 轻宿主）
function toggleGroup(name, enabled) {
    groupPost('/api/v1/groups/enabled', { name: name, enabled: enabled }, function (d) {
        groupMsgEl.textContent = d.ok ? ('已' + (enabled ? '启动' : '关闭') + '：' + name) : ('操作失败: ' + (d.error || ''));
        if (d.ok) {
            loadGroups();
        }
    });
}

// 删组——整目录入 _trash 可恢复
function deleteGroup(g) {
    if (!window.confirm('删除配队「' + g.name + '」？组内消息流将移入回收目录（可恢复）。')) {
        return;
    }
    groupPost('/api/v1/groups/delete', { name: g.name }, function (d) {
        groupMsgEl.textContent = d.ok ? ('已删除：' + g.name) : ('删除失败: ' + (d.error || ''));
        if (d.ok) {
            loadGroups();
        }
    });
}

// [段3] 配置弹层——组名 / 项目 / 成员勾选

// 查本地缓存的组行
function findGroup(name) {
    for (var i = 0; i < groupRows.length; i = i + 1) {
        if (groupRows[i].name === name) {
            return groupRows[i];
        }
    }
    return null;
}

// 打开配置弹层——空名 = 新建
function openGroupCfg(name) {
    groupCfgEditing = name || '';
    var g = findGroup(groupCfgEditing);
    document.getElementById('groupCfgTitle').textContent = name ? (' — ' + name) : ' — 新建';
    var nameInput = document.getElementById('groupCfgName');
    nameInput.value = name || '';
    nameInput.disabled = name ? true : false;
    document.getElementById('groupCfgProject').value = g ? (g.project || '') : '';
    document.getElementById('groupCfgIdle').value = g ? String(g.subscribeIdleSec || '') : '';
    document.getElementById('groupCfgMsg').textContent = '';
    groupShowModal('groupCfgModal', true);
    fetch('/api/v1/cats')
        .then(function (r) { return r.json(); })
        .then(function (d) { renderGroupMembers(d.cats || [], g ? g.members : [], g ? g.adminKeys : [], g ? g.subscriberKeys : []); })
        .catch(function () {
            document.getElementById('groupCfgMembers').textContent = '猫列表读取失败——宿主未运行？';
        });
}

// 成员勾选渲染——cats 来自 /api/v1/cats；每只猫三列（成员 / 管理员 / 订阅自动转发）
function renderGroupMembers(cats, checkedMembers, adminKeys, subscriberKeys) {
    var box = document.getElementById('groupCfgMembers');
    box.textContent = '';
    var checked = {};
    var admins = {};
    var subs = {};
    var i;
    for (i = 0; i < (checkedMembers || []).length; i = i + 1) {
        checked[checkedMembers[i].key] = true;
    }
    for (i = 0; i < (adminKeys || []).length; i = i + 1) {
        admins[adminKeys[i]] = true;
    }
    for (i = 0; i < (subscriberKeys || []).length; i = i + 1) {
        subs[subscriberKeys[i]] = true;
    }
    if (!cats || cats.length === 0) {
        box.textContent = '（无猫——先在「多猫」页新建）';
        return;
    }
    for (i = 0; i < cats.length; i = i + 1) {
        var cat = cats[i];
        var row = document.createElement('div');
        row.style.cssText = 'display:flex;align-items:center;gap:10px;font-size:var(--ch-fs-tag);color:var(--ch-fg-muted)';
        var nameEl = document.createElement('span');
        nameEl.style.cssText = 'min-width:150px';
        nameEl.textContent = (cat.special ? '★ ' : '') + (cat.name || cat.id) + '（' + cat.id + '）';
        row.appendChild(nameEl);
        row.appendChild(groupMemberFlag('成员', 'group-member-cb', cat.id, checked[cat.id] === true));
        row.appendChild(groupMemberFlag('管理员', 'group-admin-cb', cat.id, admins[cat.id] === true));
        row.appendChild(groupMemberFlag('订阅', 'group-sub-cb', cat.id, subs[cat.id] === true));
        box.appendChild(row);
    }
}

// 单列勾选件——标签文案 + 类名 + cat key + 初始勾选态
function groupMemberFlag(text, className, catKey, isChecked) {
    var label = document.createElement('label');
    label.style.cssText = 'display:flex;align-items:center;gap:4px;cursor:pointer';
    var cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.className = className;
    cb.value = catKey;
    cb.checked = isChecked;
    label.appendChild(cb);
    var span = document.createElement('span');
    span.textContent = text;
    label.appendChild(span);
    return label;
}

// 勾选集合收集——按类名取选中的 cat key 数组
function groupCheckedKeys(className) {
    var out = [];
    var boxes = document.querySelectorAll('#groupCfgMembers input.' + className);
    for (var i = 0; i < boxes.length; i = i + 1) {
        if (boxes[i].checked) {
            out.push(boxes[i].value);
        }
    }
    return out;
}

// 已勾选成员收集——cat key 数组
function collectGroupMembers() {
    return groupCheckedKeys('group-member-cb');
}

// 已勾选管理员收集——cat key 数组（保存时后端按成员交集收敛）
function collectGroupAdmins() {
    return groupCheckedKeys('group-admin-cb');
}

// 已勾选订阅者收集——cat key 数组（组内新消息自动转发目标）
function collectGroupSubscribers() {
    return groupCheckedKeys('group-sub-cb');
}

// 保存组——新建 / 更新成员与项目（组名创建后不可改名）
function saveGroupCfg() {
    var name = document.getElementById('groupCfgName').value.trim();
    var project = document.getElementById('groupCfgProject').value.trim();
    var idleText = document.getElementById('groupCfgIdle').value.trim();
    var idleSec = -1;
    if (idleText.length > 0) {
        idleSec = Number(idleText);
        if (!isFinite(idleSec) || idleSec <= 0) {
            document.getElementById('groupCfgMsg').textContent = '静默阈值须为正整数（留空 = 300）';
            return;
        }
    }
    var members = collectGroupMembers();
    var admins = collectGroupAdmins();
    var subs = collectGroupSubscribers();
    var msgEl = document.getElementById('groupCfgMsg');
    if (name.length === 0) {
        msgEl.textContent = '组名不能为空';
        return;
    }
    if (members.length === 0) {
        msgEl.textContent = '至少勾选一名成员';
        return;
    }
    groupPost('/api/v1/groups', { name: name, project: project, memberKeys: members, adminKeys: admins, subscriberKeys: subs, subscribeIdleSec: idleSec }, function (d) {
        if (!d.ok) {
            msgEl.textContent = '保存失败: ' + (d.error || '');
            return;
        }
        groupShowModal('groupCfgModal', false);
        groupMsgEl.textContent = '已保存：' + name;
        loadGroups();
    });
}

// [段4] 消息查看弹层（P2 只读；群聊页归 P4）

// 打开群聊页——实时取该组最新端口（不依赖卡片快照；每组独占端口 · A301）
function openGroupChat(name) {
    fetch('/api/v1/groups', { cache: 'no-store' })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            var list = d.groups || [];
            for (var i = 0; i < list.length; i = i + 1) {
                if (list[i].name === name) {
                    if (list[i].running && list[i].port > 0) {
                        window.open('http://127.0.0.1:' + list[i].port + '/', '_blank');
                        return;
                    }
                    break;
                }
            }
            groupMsgEl.textContent = '「' + name + '」端口未就绪——稍后重试或再点一次「启动」';
            loadGroups();
        })
        .catch(function () {
            groupMsgEl.textContent = '读取失败——宿主未运行？';
        });
}

// [段5] 事件绑定与初始化

document.getElementById('groupNewBtn').onclick = function () { openGroupCfg(''); };
document.getElementById('groupRefresh').onclick = loadGroups;
document.getElementById('groupCfgClose').onclick = function () { groupShowModal('groupCfgModal', false); };
document.getElementById('groupCfgCancel').onclick = function () { groupShowModal('groupCfgModal', false); };
document.getElementById('groupCfgSave').onclick = saveGroupCfg;
loadGroups();
