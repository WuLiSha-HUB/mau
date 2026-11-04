// CH4 外观层——panel.js：多猫管理区（2026-08-25 拆分自 index.html；2026-09-08 体量治理：API 池 → panel-apis.js；每猫配置/受控根 → panel-catcfg.js）
// 依赖：app.js 先加载（全局状态与 SSE 已就位）
// 加载顺序：app.js → panel.js → panel-apis.js → panel-catcfg.js（index.html 引用）
// A299：QQ Bot 列与 Bot 池撤除（QQ 域从宿主移除）

// [段13] 多猫管理区（P9.3d）——GET /api/v1/cats 列表 + POST command cat.* 指令族
// 2026-10-01 主面板轮：ID 列撤除；新增 LLM API / QQ Bot（行内直改）+ 目录白名单 / 工具清单 / 前文注入（专用弹层入口）
var catsTableBody = document.querySelector('#catsTable tbody');
var catsMsgEl = document.getElementById('catsMsg');
var catNewInput = document.getElementById('catNewName');
// 行内编辑数据面——API 池 / 全局根池（首屏加载一次，渲染时查表）
var catApiOptions = [];
var catAllRoots = [];

// 池数据加载——两源就绪后渲染列表（任一失败不阻塞：降级为空池）
function loadCatPools() {
    var pending = 2;
    function step() {
        pending = pending - 1;
        if (pending === 0) { loadCats(); }
    }
    fetch('/api/v1/llm-apis')
        .then(function (r) { return r.json(); })
        .then(function (d) { catApiOptions = d.items || []; step(); })
        .catch(function () { step(); });
    fetch('/api/v1/workspace')
        .then(function (r) { return r.json(); })
        .then(function (d) { catAllRoots = d.roots || []; step(); })
        .catch(function () { step(); });
}

// 猫列表加载——HTTP 拉一次（初始 / 手动刷新 / 指令回执后）；实时跟随走 SSE state 事件（applyCatState）
function loadCats() {
    fetch('/api/v1/cats')
        .then(function (r) { return r.json(); })
        .then(function (d) { applyCatState(d.cats || []); })
        .catch(function () {
            catsMsgEl.textContent = '列表加载失败——宿主未运行？';
        });
}

// 猫列表态应用（兼并上游 A223/A224/A225 特性到表格形态）——初始拉取与 SSE state 事件共用同一渲染路径。
// 表格形态：special（Majordomo）置顶 + 按 Type 标签分区（组标题行）；行内编辑进行中跳过重绘（不打断输入）；数据未变跳过。
var lastCatSig = '';

function catsInEdit() {
    var el = document.activeElement;
    if (!el || !el.tagName) { return false; }
    var tag = el.tagName.toLowerCase();
    if (tag !== 'input' && tag !== 'select' && tag !== 'textarea') { return false; }
    return catsTableBody != null && catsTableBody.contains(el);
}

function applyCatState(cats) {
    if (catsInEdit()) { return; }
    var sig = JSON.stringify(cats);
    if (sig === lastCatSig) { return; }
    lastCatSig = sig;
    catsTableBody.textContent = '';
    if (!cats || cats.length === 0) {
        catsMsgEl.textContent = '暂无猫——输入显示名新建';
        return;
    }
    catsMsgEl.textContent = cats.length + ' 只猫';
    var groups = groupCats(cats);
    var showTitle = groups.length > 1;
    for (var g = 0; g < groups.length; g = g + 1) {
        if (showTitle) {
            catsTableBody.appendChild(renderGroupRow(catGroupLabel(groups[g].key), groups[g].cats.length));
        }
        for (var c = 0; c < groups[g].cats.length; c = c + 1) {
            renderCatRow(groups[g].cats[c]);
        }
    }
}

// 分组标题行——Type 标签分区（A223 分组语义的表格形态：单行跨列标题 + 该组猫数）
function renderGroupRow(label, count) {
    var tr = document.createElement('tr');
    tr.className = 'cat-group-row';
    var td = document.createElement('td');
    td.colSpan = 9;
    td.textContent = '▸ ' + label + '（' + count + '）';
    tr.appendChild(td);
    return tr;
}

// 分组键——special（Majordomo）固定 'majordomo' 组（置顶）；其余按会话 type 标签，缺字段 / 空串 → 默认组
function catGroupKey(cat) {
    if (cat && cat.special) { return 'majordomo'; }
    if (cat && cat.type) { return String(cat.type).trim(); }
    return '';
}

// 分组标签——空键即默认组
function catGroupLabel(key) {
    return key.length > 0 ? key : '默认';
}

// 分组——special 猫置顶（F2.1 固化入口），再按 type 分区（插入序 = 组序）
function groupCats(cats) {
    var ordered = [];
    var i;
    for (i = 0; i < cats.length; i = i + 1) {
        if (cats[i].special) { ordered.push(cats[i]); }
    }
    for (i = 0; i < cats.length; i = i + 1) {
        if (!cats[i].special) { ordered.push(cats[i]); }
    }
    var groups = [];
    var index = {};
    for (i = 0; i < ordered.length; i = i + 1) {
        var key = catGroupKey(ordered[i]);
        if (index[key] === undefined) {
            index[key] = groups.length;
            groups.push({ key: key, cats: [] });
        }
        groups[index[key]].cats.push(ordered[i]);
    }
    return groups;
}

// 前文行 HTML（A225b 口径）——照对话页左上角排版：前文 <num> token <num> 条 · <num> 字符
function catStateContextHtml(cat) {
    var num = function (v) {
        return '<span class="ci-num">' + fmtNumHtml(fmtCount(v)) + '</span>';
    };
    return '前文 ' + num(cat.context) + ' token '
        + '<span class="ci-num">' + (cat.contextCount || 0) + '</span> 条'
        + ' · ' + num(cat.contextChars) + ' 字符';
}

// 命中率行 HTML（A225b/A225d 口径）——（🎯xx%）Hit…；Hit = prompt − miss
function catStateRateHtml(cat) {
    var num = function (v) {
        return '<span class="ci-num">' + fmtNumHtml(fmtCount(v)) + '</span>';
    };
    var hit = (Number(cat.sessionPrompt) || 0) - (Number(cat.sessionMiss) || 0);
    if (hit < 0) { hit = 0; }
    return '（🎯<span class="ci-rate">' + fmtNumHtml((Number(cat.sessionRate || 0) * 100).toFixed(2) + '%') + '</span>）'
        + ' <span class="ci-hit">Hit</span>' + num(hit);
}

// 未命中 / 输出行 HTML（A225d 口径）——Miss… Down…
function catStateMissHtml(cat) {
    var num = function (v) {
        return '<span class="ci-num">' + fmtNumHtml(fmtCount(v)) + '</span>';
    };
    return '<span class="ci-miss">Miss</span>' + num(cat.sessionMiss)
        + '  <span class="ci-down">Down</span>' + num(cat.sessionCompletion);
}

// 单猫行渲染——名称/LLM API 行内直改；目录白名单 + 工具清单走专用弹层；操作按钮按状态启用（special 强制自启 + 无停止/删除）
function renderCatRow(cat) {
    var tr = document.createElement('tr');
    // 行点击入口——整行可点开会话详情（排除按钮 / 输入类控件；事件委托见文件末 tbody 绑定）
    tr.setAttribute('data-cat', cat.id);
    tr.setAttribute('data-name', cat.name || cat.id);
    // [列首] 会话状态（兼并上游猫卡四行 → 表格单元格）——上行：phase 徽标 + 七态名 + Note；
    // 行一：前文（token/条/字符）· 轮次/消息/待处理 · 活跃 · 前文变动；行二：命中率 + Miss/Down
    // 数据面 = /api/v1/cats 会话字段（BuildCatInfoEntry → ChatSession.BuildStateEntry 单一数据源）
    var tdState = document.createElement('td');
    var stateWrap = document.createElement('div');
    stateWrap.className = 'cat-state';
    var stateHead = document.createElement('div');
    stateHead.className = 'cat-state-head';
    if (cat.running) {
        if (cat.phase) {
            var phaseBadge = document.createElement('span');
            phaseBadge.className = 'session-badge ph-' + cat.phase;
            phaseBadge.textContent = cat.phase;
            stateHead.appendChild(phaseBadge);
        } else {
            var runTag = document.createElement('span');
            runTag.className = 'cat-state-run';
            runTag.textContent = '运行中';
            stateHead.appendChild(runTag);
        }
        // 七态名（吸收 A225c——当前运行态细分；idle 不显示，与卡片口径一致）
        if (cat.runState && cat.runState !== 'idle') {
            var rsBadge = document.createElement('span');
            rsBadge.className = 'cat-runstate ' + cat.runState;
            rsBadge.textContent = cat.runState;
            stateHead.appendChild(rsBadge);
        }
    } else {
        var idleTag = document.createElement('span');
        idleTag.className = 'cat-state-idle';
        idleTag.textContent = '静默';
        stateHead.appendChild(idleTag);
    }
    if (cat.noteActive) {
        var stateNote = document.createElement('span');
        stateNote.className = 'session-note';
        stateNote.textContent = '📋 Note';
        stateHead.appendChild(stateNote);
    }
    stateWrap.appendChild(stateHead);
    var stateBits = [];
    if (typeof cat.context !== 'undefined' && cat.context !== null) {
        stateBits.push(catStateContextHtml(cat));
    }
    if (typeof cat.round !== 'undefined' && cat.round !== null) {
        stateBits.push('轮次 ' + cat.round + ' · 消息 ' + cat.msgCount + ' · 待处理 ' + cat.pending);
    }
    var activeAgo = fmtAgo(cat.lastActiveAt);
    if (activeAgo) {
        stateBits.push('活跃 ' + activeAgo);
    }
    var stateAgo = fmtAgo(cat.lastContextChangeAt);
    if (stateAgo) {
        stateBits.push('前文变动 ' + stateAgo);
    }
    if (stateBits.length > 0) {
        var stateInfo = document.createElement('div');
        stateInfo.className = 'cat-state-info';
        stateInfo.innerHTML = stateBits.join(' · ');
        stateWrap.appendChild(stateInfo);
    }
    // 命中率行（吸收 A225b/A225d）——会话级 token 命中率与 Miss/Down（有会话数据才出）
    if (typeof cat.sessionPrompt !== 'undefined' && cat.sessionPrompt !== null) {
        var stateRate = document.createElement('div');
        stateRate.className = 'cat-state-rate';
        stateRate.innerHTML = catStateRateHtml(cat) + ' · ' + catStateMissHtml(cat);
        stateWrap.appendChild(stateRate);
    }
    tdState.appendChild(stateWrap);
    tr.appendChild(tdState);
    // [列1] 名称——行内编辑（未改动 / 清空 = 不提交；special 行 ★ 前缀独立于输入框）
    var tdName = document.createElement('td');
    var nameWrap = document.createElement('div');
    nameWrap.style.cssText = 'display:flex;align-items:center;gap:4px';
    if (cat.special) {
        var star = document.createElement('span');
        star.textContent = '★';
        star.style.color = 'var(--ch-warn)';
        nameWrap.appendChild(star);
    }
    var nameInput = document.createElement('input');
    nameInput.className = 'input-mini';
    nameInput.style.flex = '1';
    nameInput.style.minWidth = '0';
    nameInput.style.color = 'var(--ch-identity)';
    nameInput.value = cat.name || '';
    nameInput.placeholder = '名称';
    wireInlineEdit(nameInput, cat.name || '', function (v) { submitCatField(cat, 'displayName', v, '名称'); });
    nameWrap.appendChild(nameInput);
    // Type 标签（吸收 A224）——名称格内小标；表格另按 type 分区（组标题行，见 applyCatState）
    if (cat.type && String(cat.type).length > 0) {
        var typeTag = document.createElement('span');
        typeTag.className = 'cat-type-tag';
        typeTag.textContent = cat.type;
        nameWrap.appendChild(typeTag);
    }
    tdName.appendChild(nameWrap);
    tr.appendChild(tdName);

    // [列2] LLM API——下拉直改（首项「默认 · 跟随全局」= 零值语义）
    var tdApi = document.createElement('td');
    tdApi.setAttribute('data-label', 'LLM API');
    var apiSel = document.createElement('select');
    apiSel.className = 'input-mini';
    apiSel.style.width = '100%';
    var apiDefOpt = document.createElement('option');
    apiDefOpt.value = '';
    apiDefOpt.textContent = '（默认 · 跟随全局）';
    apiSel.appendChild(apiDefOpt);
    for (var a = 0; a < catApiOptions.length; a++) {
        var apiOpt = document.createElement('option');
        apiOpt.value = catApiOptions[a].apiConfigId;
        var apiLabel = catApiOptions[a].displayName || catApiOptions[a].apiConfigId;
        if (catApiOptions[a].isDefault) { apiLabel = apiLabel + ' · 全局默认'; }
        apiOpt.textContent = apiLabel;
        apiSel.appendChild(apiOpt);
    }
    if (isDefaultApiValue(cat.apiConfigId)) { apiSel.value = ''; } else { apiSel.value = cat.apiConfigId; }
    apiSel.addEventListener('change', function () { submitCatField(cat, 'apiConfigId', apiSel.value, 'LLM API'); });
    tdApi.appendChild(apiSel);
    tr.appendChild(tdApi);

    // [列3] 思考强度——下拉直改（首项「跟随全局」= 零值语义：空 → 后端回落全局 llm.reasoning_effort；本猫覆盖，实时生效）
    var tdEffort = document.createElement('td');
    tdEffort.setAttribute('data-label', '思考强度');
    var effortSel = document.createElement('select');
    effortSel.className = 'input-mini';
    effortSel.style.width = '100%';
    var effortValues = ['', 'low', 'high', 'max'];
    for (var e = 0; e < effortValues.length; e++) {
        var effortOpt = document.createElement('option');
        effortOpt.value = effortValues[e];
        effortOpt.textContent = (effortValues[e].length === 0) ? '（跟随全局）' : effortValues[e];
        effortSel.appendChild(effortOpt);
    }
    effortSel.value = cat.reasoningEffort || '';
    effortSel.addEventListener('change', function () { submitCatField(cat, 'reasoningEffort', effortSel.value, '思考强度'); });
    tdEffort.appendChild(effortSel);
    tr.appendChild(tdEffort);

    // [列5] 目录白名单——显示无权目录 + 设置入口（专用弹层）
    var tdRoots = document.createElement('td');
    tdRoots.setAttribute('data-label', '目录白名单');
    var denied = collectDeniedRoots(cat);
    var deniedSpan = document.createElement('span');
    deniedSpan.style.fontSize = 'var(--ch-fs-tag)';
    if (denied.length === 0) {
        deniedSpan.textContent = '（全部可访问）';
        deniedSpan.style.color = 'var(--ch-fg-weak)';
    } else {
        deniedSpan.textContent = '无权: ' + denied.join(', ');
        deniedSpan.style.color = 'var(--ch-err)';
        deniedSpan.title = '本猫不可访问的根——点「设置」调整';
    }
    tdRoots.appendChild(deniedSpan);
    var rootsBtn = document.createElement('button');
    rootsBtn.textContent = '设置';
    rootsBtn.className = 'btn-mini';
    rootsBtn.style.marginLeft = '4px';
    rootsBtn.onclick = function () { openCatRoots(cat.id, cat.name); };
    tdRoots.appendChild(rootsBtn);
    tr.appendChild(tdRoots);

    // [列6] 工具清单——单按钮入口（专用弹层：工具勾选 + 人设）
    var tdTools = document.createElement('td');
    var toolsBtn = document.createElement('button');
    toolsBtn.textContent = '工具清单';
    toolsBtn.className = 'btn-mini';
    toolsBtn.onclick = function () { openCatTools(cat.id, cat.name); };
    tdTools.appendChild(toolsBtn);
    tr.appendChild(tdTools);

    // [列7] 前文注入——单按钮入口（专用弹层：候选 = 新猫默认模板的默认前文池）
    var tdInject = document.createElement('td');
    var injectBtn = document.createElement('button');
    injectBtn.textContent = '前文注入';
    injectBtn.className = 'btn-mini';
    injectBtn.onclick = function () { openCatInject(cat.id, cat.name); };
    tdInject.appendChild(injectBtn);
    tr.appendChild(tdInject);

    var tdPort = document.createElement('td');
    tdPort.setAttribute('data-label', '端口');
    tdPort.textContent = cat.running ? (':' + cat.port) : '-';
    tr.appendChild(tdPort);
    var tdOp = document.createElement('td');
    tdOp.setAttribute('data-label', '操作');
    tdOp.style.whiteSpace = 'nowrap';
    if (cat.running) {
        var openBtn = document.createElement('button');
        openBtn.textContent = '打开对话';
        openBtn.className = 'btn-mini primary';
        openBtn.onclick = function () { window.open('http://127.0.0.1:' + cat.port + '/', '_blank'); };
        tdOp.appendChild(openBtn);
        if (!cat.special) {
            var stopBtn = document.createElement('button');
            stopBtn.textContent = '停止';
            stopBtn.className = 'btn-mini';
            stopBtn.onclick = function () { catAction('cat.stop ' + cat.id); };
            tdOp.appendChild(stopBtn);
        }
    } else {
        var startBtn = document.createElement('button');
        startBtn.textContent = '启动';
        startBtn.className = 'btn-mini primary';
        startBtn.onclick = function () { catAction('cat.start ' + cat.id); };
        tdOp.appendChild(startBtn);
    }
    // 配置入口——special（Majordomo）与多猫共用（openCatCfg 由 panel-catcfg.js 提供）
    var cfgBtn = document.createElement('button');
    cfgBtn.textContent = '配置';
    cfgBtn.className = 'btn-mini';
    cfgBtn.onclick = function () { openCatCfg(cat.id, cat.name); };
    tdOp.appendChild(cfgBtn);
    if (!cat.special) {
        // 多猫专属——删除（Majordomo 特殊会话无删除入口）
        var delBtn = document.createElement('button');
        delBtn.textContent = '删除';
        delBtn.className = 'btn-mini danger';
        delBtn.onclick = function () {
            // 删除确认——猫销毁不可逆：配置（persona/工具面/API/注入/白名单）+ 前文全部丢失
            if (!window.confirm('删除猫「' + cat.name + '」？删除后该猫的所有配置与前文都将丢失（不可恢复）。')) { return; }
            catAction('cat.delete ' + cat.id);
        };
        tdOp.appendChild(delBtn);
    }
    tr.appendChild(tdOp);
    catsTableBody.appendChild(tr);
}

// cat.* 指令投递——POST command 后延迟刷新（指令经 HTTP 线程入队主线程泵，异步生效）
function catAction(cmd) {
    fetch('/api/v1/command', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: cmd })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            catsMsgEl.textContent = '回执: ' + cmd + ' → ok=' + d.ok + (d.error ? ' error=' + d.error : '');
            setTimeout(loadCats, 400);
        })
        .catch(function (err) {
            catsMsgEl.textContent = '请求失败: ' + err;
        });
}

// 新建猫——cat.new [显示名]（空名 = 后端自动命名：默认小猫 / 默认小猫(1)…）
function catNew() {
    var name = catNewInput.value.trim();
    catNewInput.value = '';
    if (name.length === 0) {
        catAction('cat.new');
        return;
    }
    catAction('cat.new ' + name);
}

// 整行点击——打开会话详情弹层（openCatDetail 在 app.js；只点「内容区」才开）
// 判据：点击目标或其祖先链上出现 button / input / select / textarea / a / label → 视为操作，不触发详情
function isRowInteractive(el) {
    while (el && el.tagName) {
        var tag = el.tagName.toLowerCase();
        if (tag === 'button' || tag === 'input' || tag === 'select' || tag === 'textarea' || tag === 'a' || tag === 'label') {
            return true;
        }
        el = el.parentNode;
    }
    return false;
}

function onCatsRowClick(e) {
    if (isRowInteractive(e.target)) { return; }
    var el = e.target;
    while (el && el !== catsTableBody) {
        if (el.tagName && el.tagName.toLowerCase() === 'tr') {
            var key = el.getAttribute('data-cat');
            if (key) { openCatDetail(key, el.getAttribute('data-name') || key); }
            return;
        }
        el = el.parentNode;
    }
}

catsTableBody.addEventListener('click', onCatsRowClick);

// 多猫区按钮绑定 + 列表初始化
document.getElementById('catNewBtn').addEventListener('click', catNew);
document.getElementById('catsRefresh').addEventListener('click', loadCats);
catNewInput.addEventListener('keydown', function (e) {
    if (e.key === 'Enter') { catNew(); }
});
loadCatPools();

// 无权目录——全局根池 ∖ 本猫启用根（workspace/runtime 系统根恒可用，不计入无权；未配置 = 空白名单）
function collectDeniedRoots(cat) {
    var denied = [];
    if (!catAllRoots || catAllRoots.length === 0) { return denied; }
    var enabled = {};
    var list = cat.enabledRoots || [];
    for (var i = 0; i < list.length; i++) {
        if (list[i]) { enabled[String(list[i]).toLowerCase()] = true; }
    }
    for (var j = 0; j < catAllRoots.length; j++) {
        var root = catAllRoots[j];
        var rid = (root.id || '').toLowerCase();
        if (rid === 'workspace' || rid === 'runtime') { continue; }
        if (enabled[rid] === true) { continue; }
        denied.push(root.id);
    }
    return denied;
}

// 主面板行内字段提交——POST /api/v1/cat-config（字段级合并写：出现即覆盖 / 缺省即保留）
// 2026-10-01 修正：原走 cat.cfg.set 文本指令（HTTP 线程入队主线程泵，回执只报"已受理"）——空值被行首 Trim 吃掉（改成默认 API / 解绑 QQ Bot 必然失败），
// 且写入未落盘就先重建列表（固定 400ms）→ 表现为「改完瞬间跳回原值」+「显示与实际不符」。
// 现改走端点：同步落盘、回执即结果、落盘后才刷新。空值语义 = 清空（apiConfigId 回默认 / displayName 拒绝空值）。
function submitCatField(cat, field, value, label) {
    catsMsgEl.textContent = '保存中…（' + label + '）';
    var payload = { cat: cat.id };
    payload[field] = value;
    fetch('/api/v1/cat-config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (d.ok) {
                catsMsgEl.textContent = '已保存（' + label + '）';
                loadCats();
            } else {
                catsMsgEl.textContent = '保存失败（' + label + '）: ' + (d.error || '');
            }
        })
        .catch(function (e) {
            catsMsgEl.textContent = '请求失败: ' + e.message;
        });
}
