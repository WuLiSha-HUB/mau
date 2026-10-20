// CH4 外观层——panel-db.js：数据库（Smriti）浏览面（A269 · design-ch4-db.md §六.4）
// 数据源：GET /api/v1/db/libs（库清单）· POST /api/v1/db/libs（新建库）
//         GET /api/v1/db/libs/{lib}/entries?keyword=&limit=（条目列表，与 db-query 同语义）
//         GET /api/v1/db/libs/{lib}/entries/{id}（条目全文，与 db-get 按 ID 同语义）
// 边界：本页只读 + 建库一个动作——写入（增/改/删）一律走 db-* 工具面，不做第二套写路径。

var dbLibSelectEl = document.getElementById('dbLibSelect');
var dbLibRefreshBtn = document.getElementById('dbLibRefresh');
var dbLibNewEl = document.getElementById('dbLibNew');
var dbLibNewBtn = document.getElementById('dbLibNewBtn');
var dbKeywordEl = document.getElementById('dbKeyword');
var dbLimitEl = document.getElementById('dbLimit');
var dbQueryBtn = document.getElementById('dbQueryBtn');
var dbInfoEl = document.getElementById('dbInfo');
var dbListEl = document.getElementById('dbList');
var dbPreviewEl = document.getElementById('dbPreview');

// 当前选中库名（下拉 value；空 = 无库）
function currentDbLib() {
    if (!dbLibSelectEl) { return ''; }
    return dbLibSelectEl.value || '';
}

// 建一行条目——ID / 标题（可点取全文） / 更新时间
function buildDbRow(item) {
    var row = document.createElement('div');
    row.style.cssText = 'display:flex;align-items:center;gap:10px;padding:7px 12px;border-bottom:1px solid var(--ch-line-faint)';
    var idEl = document.createElement('span');
    idEl.textContent = '#' + item.id;
    idEl.style.cssText = 'flex:0 0 64px;color:var(--ch-fg-faint);font-size:var(--ch-fs-tag)';
    row.appendChild(idEl);
    var titleEl = document.createElement('span');
    titleEl.textContent = item.title || '';
    titleEl.title = item.title || '';
    titleEl.style.cssText = 'flex:1;min-width:0;color:var(--ch-ok);cursor:pointer;overflow:hidden;text-overflow:ellipsis;white-space:nowrap';
    titleEl.addEventListener('click', function () { loadDbEntry(item.id); });
    row.appendChild(titleEl);
    var timeEl = document.createElement('span');
    timeEl.textContent = item.updatedAt || '';
    timeEl.style.cssText = 'flex:0 0 auto;color:var(--ch-fg-weak);font-size:var(--ch-fs-tag);white-space:nowrap';
    row.appendChild(timeEl);
    return row;
}

// 库下拉渲染——保留当前选中项（不存在则取首个）；库名为空给出空态
function renderDbLibs(libs) {
    if (!dbLibSelectEl) { return; }
    var keep = dbLibSelectEl.value;
    dbLibSelectEl.textContent = '';
    if (!libs || libs.length === 0) {
        var empty = document.createElement('option');
        empty.value = '';
        empty.textContent = '（无库——右侧新建）';
        dbLibSelectEl.appendChild(empty);
        return;
    }
    for (var i = 0; i < libs.length; i = i + 1) {
        var opt = document.createElement('option');
        opt.value = libs[i].id;
        opt.textContent = libs[i].id + '（' + (libs[i].entries || 0) + ' 条）' + (libs[i].broken ? ' [不可读]' : '');
        dbLibSelectEl.appendChild(opt);
    }
    var found = false;
    for (var j = 0; j < libs.length; j = j + 1) {
        if (libs[j].id === keep) { found = true; }
    }
    if (found) { dbLibSelectEl.value = keep; }
}

// 回执错误文本——机器码 + 中文原因（服务端未绑定 / 服务失败 / 网络失败统一出口）
function dbErrText(d) {
    if (!d) { return '回执为空'; }
    var code = d.code || 'ERR';
    var msg = d.message || '未知错误';
    return code + ' — ' + msg;
}

// 库清单——GET /api/v1/db/libs
function loadDbLibs() {
    fetch('/api/v1/db/libs')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d || d.ok !== true) {
                if (dbInfoEl) { dbInfoEl.textContent = '库清单读取失败：' + dbErrText(d); }
                renderDbLibs([]);
                return;
            }
            var libs = (d.data && d.data.libs) || [];
            renderDbLibs(libs);
            if (dbInfoEl) { dbInfoEl.textContent = '库 ' + libs.length + ' 个——选库后查询'; }
        })
        .catch(function () {
            if (dbInfoEl) { dbInfoEl.textContent = '库清单请求失败——宿主未运行？'; }
            renderDbLibs([]);
        });
}

// 新建库——POST /api/v1/db/libs（body: {"id":"<libId>"}）
function createDbLib() {
    if (!dbLibNewEl) { return; }
    var libId = (dbLibNewEl.value || '').replace(/^\s+|\s+$/g, '').toLowerCase();
    if (!/^[a-z0-9-]{2,32}$/.test(libId)) {
        if (dbInfoEl) { dbInfoEl.textContent = '库名不合法——小写字母 / 数字 / 短横线，2–32 位'; }
        return;
    }
    dbLibNewBtn.disabled = true;
    fetch('/api/v1/db/libs', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: libId })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d || d.ok !== true) {
                if (dbInfoEl) { dbInfoEl.textContent = '新建失败：' + dbErrText(d); }
                return;
            }
            if (dbInfoEl) { dbInfoEl.textContent = '库已建立：' + libId; }
            dbLibNewEl.value = '';
            loadDbLibs();
        })
        .catch(function () {
            if (dbInfoEl) { dbInfoEl.textContent = '新建请求失败——宿主未运行？'; }
        })
        .then(function () { dbLibNewBtn.disabled = false; });
}

// 条目列表——GET /api/v1/db/libs/{lib}/entries?keyword=&limit=
function queryDbEntries() {
    var lib = currentDbLib();
    if (!lib) {
        if (dbInfoEl) { dbInfoEl.textContent = '未选库——先新建或选择库'; }
        return;
    }
    var keyword = dbKeywordEl ? dbKeywordEl.value : '';
    var limit = dbLimitEl ? dbLimitEl.value : '';
    var url = '/api/v1/db/libs/' + encodeURIComponent(lib) + '/entries?keyword=' + encodeURIComponent(keyword) + '&limit=' + encodeURIComponent(limit);
    if (dbListEl) { dbListEl.textContent = ''; }
    if (dbPreviewEl) { dbPreviewEl.textContent = ''; }
    fetch(url)
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d || d.ok !== true) {
                if (dbInfoEl) { dbInfoEl.textContent = '查询失败：' + dbErrText(d); }
                return;
            }
            var data = d.data || {};
            var items = data.items || [];
            if (dbListEl) {
                for (var i = 0; i < items.length; i = i + 1) {
                    dbListEl.appendChild(buildDbRow(items[i]));
                }
            }
            var info = '命中 ' + (data.matched || 0) + ' / 活跃 ' + (data.total || 0) + '，显示 ' + (data.shown || 0) + ' 条';
            if (d.badLines > 0) { info = info + '（坏行 ' + d.badLines + ' 已跳过）'; }
            if (dbInfoEl) { dbInfoEl.textContent = info; }
        })
        .catch(function () {
            if (dbInfoEl) { dbInfoEl.textContent = '查询请求失败——宿主未运行？'; }
        });
}

// 条目全文——GET /api/v1/db/libs/{lib}/entries/{id}
function loadDbEntry(entryId) {
    var lib = currentDbLib();
    if (!lib) { return; }
    var url = '/api/v1/db/libs/' + encodeURIComponent(lib) + '/entries/' + encodeURIComponent(entryId);
    fetch(url)
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!dbPreviewEl) { return; }
            dbPreviewEl.textContent = '';
            if (!d || d.ok !== true) {
                var fail = document.createElement('div');
                fail.textContent = '取全文失败：' + dbErrText(d);
                fail.style.cssText = 'color:var(--ch-err);font-size:var(--ch-fs-tag)';
                dbPreviewEl.appendChild(fail);
                return;
            }
            var e = d.data || {};
            var head = document.createElement('h2');
            head.textContent = '#' + e.id + ' ' + (e.title || '');
            dbPreviewEl.appendChild(head);
            var meta = document.createElement('div');
            meta.textContent = 'v' + e.v + ' · 创建 ' + (e.createdAt || '') + ' · 更新 ' + (e.updatedAt || '');
            meta.style.cssText = 'color:var(--ch-fg-faint);font-size:var(--ch-fs-tag);margin-bottom:8px';
            dbPreviewEl.appendChild(meta);
            var bodyEl = document.createElement('div');
            bodyEl.textContent = e.body || '';
            bodyEl.style.cssText = 'color:var(--ch-fg);white-space:pre-wrap;word-break:break-word';
            dbPreviewEl.appendChild(bodyEl);
        })
        .catch(function () {
            if (dbInfoEl) { dbInfoEl.textContent = '取全文请求失败——宿主未运行？'; }
        });
}

// 事件接线——刷新 / 新建 / 查询；下拉切换即清空结果（防串库）
if (dbLibRefreshBtn) {
    dbLibRefreshBtn.addEventListener('click', function () { loadDbLibs(); });
}
if (dbLibNewBtn) {
    dbLibNewBtn.addEventListener('click', function () { createDbLib(); });
}
if (dbQueryBtn) {
    dbQueryBtn.addEventListener('click', function () { queryDbEntries(); });
}
if (dbLibSelectEl) {
    dbLibSelectEl.addEventListener('change', function () {
        if (dbListEl) { dbListEl.textContent = ''; }
        if (dbPreviewEl) { dbPreviewEl.textContent = ''; }
        if (dbInfoEl) { dbInfoEl.textContent = '已选库 ' + currentDbLib(); }
        queryDbEntries();
    });
}
if (dbKeywordEl) {
    dbKeywordEl.addEventListener('keydown', function (ev) {
        if (ev.key === 'Enter') { queryDbEntries(); }
    });
}

// 首屏——载入库清单
loadDbLibs();
