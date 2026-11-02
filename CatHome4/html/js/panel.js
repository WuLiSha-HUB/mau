// CH4 外观层——panel.js：多猫管理区（2026-08-25 拆分自 index.html；2026-09-08 体量治理：API 池 → panel-apis.js；每猫配置/受控根 → panel-catcfg.js）
// 依赖：app.js 先加载（全局状态与 SSE 已就位）
// 加载顺序：app.js → panel.js → panel-apis.js → panel-catcfg.js（index.html 引用）

// [段13] 多猫管理区（P9.3d；A223 List→Card 自然排布 + 分组容器）——GET /api/v1/cats 列表 + POST command cat.* 指令族
var catsGroupsEl = document.getElementById('catsGroups');
var catsMsgEl = document.getElementById('catsMsg');
var catNewInput = document.getElementById('catNewName');

// A225e 增量更新池——卡片 / 分组节点只建一次，之后只改属性（Html Job §2.1/§2.2：预创建 + 存引用 → Update 只改 DOM 属性；利于后续动效）
var catCards = {};   // 猫 id → { el, refs, data, prev }
var catGroupPool = {};   // 分组键 → { el, title, grid }
var lastCatSig = '';   // 上次结构签名（组序 + 组内 id 序）——相同则跳过节点排列（A225f）

// 猫列表加载——HTTP 拉一次（初始 / 手动刷新；实时跟随走 SSE state 事件）
function loadCats() {
    fetch('/api/v1/cats')
        .then(function (r) { return r.json(); })
        .then(function (d) { applyCatState(d.cats || []); })
        .catch(function () {
            catsMsgEl.textContent = '列表加载失败——宿主未运行？';
        });
}

// 猫卡状态应用（A225c/A225e/A225f）——HTTP 初始拉取与 SSE state 事件共用同一渲染路径；
// 增量更新：卡片节点按 id 复用、只改属性；**结构排列仅在签名变化时触碰 DOM**
// （判例 A225f：每帧无条件 appendChild 会把节点移出再插入 → 浏览器取消 :hover 与进行中的过渡 → hover 边缘闪烁）
function applyCatState(cats) {
    if (!cats || cats.length === 0) {
        clearCatCards();
        catsMsgEl.textContent = '暂无猫——输入显示名新建';
        return;
    }
    catsMsgEl.textContent = cats.length + ' 只猫';
    // F2.1 前端唯一化——special 猫（Majordomo）置顶（固化入口）；分组键 = 会话 type 标签（A224 接线）
    var groups = groupCats(cats);
    var showTitle = groups.length > 1;
    var seenCards = {};
    var seenGroups = {};
    var sigParts = [];
    for (var g = 0; g < groups.length; g = g + 1) {
        var key = groups[g].key;
        seenGroups[key] = true;
        var grp = catGroupPool[key];
        if (!grp) {
            // 首次出现的分组——建容器一次（标题 + 网格），之后复用
            var gel = document.createElement('div');
            gel.className = 'cat-group';
            var title = document.createElement('div');
            title.className = 'cat-group-title';
            gel.appendChild(title);
            var grid = document.createElement('div');
            grid.className = 'cat-grid cat-grid-manage';
            gel.appendChild(grid);
            grp = { el: gel, title: title, grid: grid };
            catGroupPool[key] = grp;
        }
        grp.title.textContent = catGroupLabel(key);
        grp.title.style.display = showTitle ? '' : 'none';
        var ids = [];
        for (var c = 0; c < groups[g].cats.length; c = c + 1) {
            var cat = groups[g].cats[c];
            seenCards[cat.id] = true;
            ids.push(cat.id);
            var entry = catCards[cat.id];
            if (!entry) {
                entry = createCatCard(cat);
                catCards[cat.id] = entry;
            }
            updateCatCard(entry, cat);
        }
        sigParts.push(key + '=' + ids.join(','));
    }
    // 结构排列——仅当「组序 / 组内序 / 组成」变化（含首帧）才动结构；稳态零 DOM 变动
    var sig = sigParts.join('|');
    if (sig !== lastCatSig) {
        lastCatSig = sig;
        for (var g2 = 0; g2 < groups.length; g2 = g2 + 1) {
            var grp2 = catGroupPool[groups[g2].key];
            var cs = groups[g2].cats;
            for (var c2 = 0; c2 < cs.length; c2 = c2 + 1) {
                grp2.grid.appendChild(catCards[cs[c2].id].el);
            }
            catsGroupsEl.appendChild(grp2.el);
        }
    }
    // 清理——消失的猫与空组（移除节点 + 出池）
    for (var id in catCards) {
        if (!seenCards[id]) {
            removeNode(catCards[id].el);
            delete catCards[id];
        }
    }
    for (var k in catGroupPool) {
        if (!seenGroups[k]) {
            removeNode(catGroupPool[k].el);
            delete catGroupPool[k];
        }
    }
}

// 节点移除——带父节点判定（已移除则跳过）
function removeNode(el) {
    if (el && el.parentNode) {
        el.parentNode.removeChild(el);
    }
}

// 清空猫卡——池与容器同步清（列表为空时）
function clearCatCards() {
    for (var id in catCards) {
        removeNode(catCards[id].el);
        delete catCards[id];
    }
    for (var k in catGroupPool) {
        removeNode(catGroupPool[k].el);
        delete catGroupPool[k];
    }
    catsGroupsEl.textContent = '';
    lastCatSig = '';
}

// 分组键——special（Majordomo）固定 'majordomo' 组（独立一排）；其余按会话 type 标签（A224 接线），缺字段 / 空串 → 默认组（空键）
function catGroupKey(cat) {
    if (cat && cat.special) {
        return 'majordomo';
    }
    if (cat && cat.type) {
        return String(cat.type).trim();
    }
    return '';
}

// 分组标签——空键即默认组
function catGroupLabel(key) {
    return key.length > 0 ? key : '默认';
}

// 分组——special 猫（Majordomo）置顶（F2.1 固化入口），再按 type 分区（插入序 = 组序）
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

// 前文行 HTML（A225b）——照 Chat 界面左上角排版：前文 <num> token <num> 条
function catStateContextHtml(cat) {
    var num = function (v) {
        return '<span class="ci-num">' + fmtNumHtml(fmtCount(v)) + '</span>';
    };
    return '前文 ' + num(cat.context) + ' token '
        + '<span class="ci-num">' + (cat.contextCount || 0) + '</span> 条';
}

// 命中率行 HTML（A225b/A225d）——照 Chat 界面左上角排版：（🎯xx%）Hit…
function catStateRateHtml(cat) {
    var num = function (v) {
        return '<span class="ci-num">' + fmtNumHtml(fmtCount(v)) + '</span>';
    };
    var hit = (Number(cat.sessionPrompt) || 0) - (Number(cat.sessionMiss) || 0);
    if (hit < 0) { hit = 0; }
    return '（🎯<span class="ci-rate">' + fmtNumHtml((Number(cat.sessionRate || 0) * 100).toFixed(2) + '%') + '</span>）'
        + ' <span class="ci-hit">Hit</span>' + num(hit);
}

// 未命中/输出行 HTML（A225d）——Miss… Down…
function catStateMissHtml(cat) {
    var num = function (v) {
        return '<span class="ci-num">' + fmtNumHtml(fmtCount(v)) + '</span>';
    };
    return '<span class="ci-miss">Miss</span>' + num(cat.sessionMiss)
        + '  <span class="ci-down">Down</span>' + num(cat.sessionCompletion);
}

// 单猫卡片创建（A225e——只建一次）——全部子节点预建（含各状态按钮），更新阶段只改属性 / display
// 按钮 onclick 闭包引用 entry（entry.data 恒为最近一次状态），节点复用不重绑
function createCatCard(cat) {
    var entry = { el: null, refs: {}, data: cat, prev: {} };
    var card = document.createElement('div');
    card.className = 'cat-card';
    var head = document.createElement('div');
    head.className = 'cat-head';
    var name = document.createElement('span');
    name.className = 'cat-name';
    head.appendChild(name);
    // 运行态——右：当前七态名（仅运行中显示）+ 圆点（绿=运行 / 灰=停）
    var status = document.createElement('span');
    status.className = 'cat-status';
    var rs = document.createElement('span');
    rs.className = 'cat-runstate';
    status.appendChild(rs);
    var dot = document.createElement('span');
    dot.className = 'cat-dot';
    status.appendChild(dot);
    head.appendChild(status);
    card.appendChild(head);
    // 信息行——ID + 端口
    var meta = document.createElement('div');
    meta.className = 'cat-meta';
    var idEl = document.createElement('span');
    idEl.className = 'cat-id';
    meta.appendChild(idEl);
    var portEl = document.createElement('span');
    portEl.className = 'cat-port';
    meta.appendChild(portEl);
    card.appendChild(meta);
    // 活跃 / 前文 / 命中率 / 未命中（四行；前文与命中率照 Chat 界面左上角排版）
    var foot = document.createElement('div');
    foot.className = 'cat-foot';
    var agoLine = document.createElement('div');
    agoLine.className = 'cat-foot-line';
    foot.appendChild(agoLine);
    var ctxLine = document.createElement('div');
    ctxLine.className = 'cat-foot-line';
    foot.appendChild(ctxLine);
    var rateLine = document.createElement('div');
    rateLine.className = 'cat-foot-line';
    foot.appendChild(rateLine);
    var missLine = document.createElement('div');
    missLine.className = 'cat-foot-line';
    foot.appendChild(missLine);
    // A298——无会话提示行（真停后会话态参数不可得，四行收起后由本行说明原因）
    var hintLine = document.createElement('div');
    hintLine.className = 'cat-foot-hint';
    hintLine.style.display = 'none';
    foot.appendChild(hintLine);
    card.appendChild(foot);
    // 操作行——全部按钮预建，按状态 display 显隐（打开 / 停止 / 启动 / 配置 / 删除）
    var ops = document.createElement('div');
    ops.className = 'cat-ops';
    var openBtn = document.createElement('button');
    openBtn.textContent = '打开对话';
    openBtn.className = 'btn-mini primary';
    openBtn.onclick = function () { window.open('http://127.0.0.1:' + entry.data.port + '/', '_blank'); };
    ops.appendChild(openBtn);
    var stopBtn = document.createElement('button');
    stopBtn.textContent = '停止';
    stopBtn.className = 'btn-mini';
    stopBtn.onclick = function () { catAction('cat.stop ' + entry.data.id); };
    ops.appendChild(stopBtn);
    var startBtn = document.createElement('button');
    startBtn.textContent = '启动';
    startBtn.className = 'btn-mini primary';
    startBtn.onclick = function () { catAction('cat.start ' + entry.data.id); };
    ops.appendChild(startBtn);
    // 配置入口——special（Majordomo）与多猫共用（openCatCfg 由 panel-catcfg.js 提供）
    var cfgBtn = document.createElement('button');
    cfgBtn.textContent = '配置';
    cfgBtn.className = 'btn-mini';
    cfgBtn.onclick = function () { openCatCfg(entry.data.id, entry.data.name); };
    ops.appendChild(cfgBtn);
    var delBtn = document.createElement('button');
    delBtn.textContent = '删除';
    delBtn.className = 'btn-mini danger';
    delBtn.onclick = function () {
        // 删除确认——猫销毁不可逆：配置（persona/工具面/API/注入/白名单）+ 前文全部丢失
        if (!window.confirm('删除猫「' + entry.data.name + '」？删除后该猫的所有配置与前文都将丢失（不可恢复）。')) { return; }
        catAction('cat.delete ' + entry.data.id);
    };
    ops.appendChild(delBtn);
    card.appendChild(ops);
    entry.el = card;
    entry.refs = { name: name, rs: rs, dot: dot, id: idEl, port: portEl, ago: agoLine, ctx: ctxLine, rate: rateLine, miss: missLine, hint: hintLine, openBtn: openBtn, stopBtn: stopBtn, startBtn: startBtn, delBtn: delBtn };
    return entry;
}

// 单猫卡片更新（A225e）——只改属性；逐字段与上次值比对，未变不写（不触碰 DOM → 动效不被打断）
function updateCatCard(entry, cat) {
    entry.data = cat;
    var r = entry.refs;
    var p = entry.prev;
    // 名称（special 前置 ★）
    var nameText = (cat.special ? '★ ' : '') + (cat.name || '');
    if (p.name !== nameText) { r.name.textContent = nameText; p.name = nameText; }
    // 运行态切换——态名 / 圆点 / 按钮显隐（状态未变则整块跳过）
    var running = !!cat.running;
    if (p.running !== running) {
        // A298——运行中底图（从下往上绿色渐变 + 提亮描边）
        entry.el.className = 'cat-card' + (running ? ' is-running' : '');
        r.rs.style.display = running ? '' : 'none';
        r.dot.className = 'cat-dot' + (running ? ' on' : '');
        r.openBtn.style.display = running ? '' : 'none';
        r.stopBtn.style.display = (running && !cat.special) ? '' : 'none';
        r.startBtn.style.display = running ? 'none' : '';
        r.delBtn.style.display = cat.special ? 'none' : '';
        p.running = running;
        p.runState = undefined;   // 态名随运行态重置，强制下一段刷新
    }
    if (running) {
        var rsName = cat.runState || 'idle';
        if (p.runState !== rsName) {
            r.rs.textContent = rsName;
            r.rs.className = 'cat-runstate ' + rsName;
            p.runState = rsName;
        }
    }
    // ID
    if (p.id !== cat.id) { r.id.textContent = '#' + (cat.id || ''); p.id = cat.id; }
    // 端口
    var portText = '端口 ' + (running ? (':' + cat.port) : '-');
    if (p.port !== portText) { r.port.textContent = portText; p.port = portText; }
    // 活跃行
    var ago = fmtAgo(cat.lastActiveAt);
    var agoText = ago ? ('活跃 ' + ago) : '无活跃记录';
    if (p.ago !== agoText) { r.ago.textContent = agoText; p.ago = agoText; }
    // 前文行
    var ctxHtml = catStateContextHtml(cat);
    if (p.ctx !== ctxHtml) { r.ctx.innerHTML = ctxHtml; p.ctx = ctxHtml; }
    // 命中率行
    var rateHtml = catStateRateHtml(cat);
    if (p.rate !== rateHtml) { r.rate.innerHTML = rateHtml; p.rate = rateHtml; }
    // 未命中 / 输出行
    var missHtml = catStateMissHtml(cat);
    if (p.miss !== missHtml) { r.miss.innerHTML = missHtml; p.miss = missHtml; }
    // A298 真停适配——四行参数均由会话态派生（msgCount / context / session* 同属 BuildStateEntry）：
    // 会话缺失时整块收起并给一句说明，不让空白字段冒充「0」；会话回来即恢复
    var hasSession = typeof cat.msgCount === 'number';
    if (p.hasSession !== hasSession) {
        var footShow = hasSession ? '' : 'none';
        r.ago.style.display = footShow;
        r.ctx.style.display = footShow;
        r.rate.style.display = footShow;
        r.miss.style.display = footShow;
        r.hint.style.display = hasSession ? 'none' : '';
        p.hasSession = hasSession;
    }
    var hintText = hasSession ? '' : (running ? '会话未就绪' : '已停机 · 无会话 — 启动后恢复');
    if (p.hint !== hintText) { r.hint.textContent = hintText; p.hint = hintText; }
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

// 新建猫——cat.new <显示名>（空名拒绝）
function catNew() {
    var name = catNewInput.value.trim();
    if (name.length === 0) {
        catsMsgEl.textContent = '显示名不能为空';
        return;
    }
    catNewInput.value = '';
    catAction('cat.new ' + name);
}

// 多猫区按钮绑定 + 列表初始化
document.getElementById('catNewBtn').addEventListener('click', catNew);
document.getElementById('catsRefresh').addEventListener('click', loadCats);
catNewInput.addEventListener('keydown', function (e) {
    if (e.key === 'Enter') { catNew(); }
});
loadCats();
