// CH4 外观层——panel-apis.js：LLM API 池管理（config 页签）——panel.js 拆分（2026-09-08 体量治理）
// 依赖：app.js + panel.js 先加载（全局状态已就位）；本文件只承载 API 池的列表/新建/编辑/删除
// 段14（M3b LLM API 池）原样迁出，逻辑零改动

// [段14] M3b LLM API 池管理（config 页签——列表/新建/编辑/删除；key 掩码）
var apisTableBody = document.querySelector('#apisTable tbody');
var apisMsgEl = document.getElementById('apisMsg');
var editingApiId = '';

function loadApis() {
    fetch('/api/v1/llm-apis')
        .then(function (r) { return r.json(); })
        .then(function (d) { renderApis(d.items || []); })
        .catch(function () { apisMsgEl.textContent = 'API 池加载失败——宿主未运行？'; });
}

function renderApis(items) {
    apisTableBody.textContent = '';
    apisMsgEl.textContent = items.length + ' 组配置';
    for (var i = 0; i < items.length; i++) {
        renderApiRow(items[i]);
    }
}

function renderApiRow(api) {
    var tr = document.createElement('tr');
    var tdName = document.createElement('td');
    tdName.textContent = api.displayName || '';
    tdName.style.color = 'var(--ch-identity)';
    if (api.isDefault) { tdName.textContent = '★ ' + tdName.textContent; }
    tr.appendChild(tdName);
    var tdId = document.createElement('td');
    tdId.textContent = api.apiConfigId || '';
    tdId.style.color = 'var(--ch-fg-weak)';
    tdId.style.fontSize = '10px';
    tdId.style.wordBreak = 'break-all';
    tr.appendChild(tdId);
    var tdType = document.createElement('td');
    tdType.textContent = api.apiType || '';
    tr.appendChild(tdType);
    var tdEndpoint = document.createElement('td');
    tdEndpoint.textContent = api.endpoint || '';
    tdEndpoint.style.wordBreak = 'break-all';
    tr.appendChild(tdEndpoint);
    var tdModel = document.createElement('td');
    tdModel.textContent = api.defaultModel || '';
    tr.appendChild(tdModel);
    var tdKey = document.createElement('td');
    tdKey.textContent = api.hasKey ? api.apiKey : '（未配置）';
    tdKey.style.color = api.hasKey ? 'var(--ch-ok-weak)' : 'var(--ch-fg-weak)';
    tr.appendChild(tdKey);
    var tdOp = document.createElement('td');
    tdOp.style.whiteSpace = 'nowrap';
    if (!api.isDefault) {
        var defBtn = document.createElement('button');
        defBtn.textContent = '设为默认';
        defBtn.className = 'btn-mini accent';
        defBtn.onclick = function () { setDefaultApi(api); };
        tdOp.appendChild(defBtn);
    }
    var editBtn = document.createElement('button');
    editBtn.textContent = '编辑';
    editBtn.className = 'btn-mini';
    editBtn.onclick = function () { startApiEdit(api); };
    tdOp.appendChild(editBtn);
    var delBtn = document.createElement('button');
    delBtn.textContent = '删除';
    delBtn.className = 'btn-mini danger';
    delBtn.onclick = function () {
        if (!confirm('删除 API 配置「' + api.displayName + '」？引用它的猫将回退空配置。')) { return; }
        fetch('/api/v1/llm-apis/delete', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ apiConfigId: api.apiConfigId })
        })
            .then(function (r) { return r.json(); })
            .then(function (d) {
                apisMsgEl.textContent = d.ok ? '已删除' : '删除失败: ' + d.error;
                loadApis();
            })
            .catch(function (e) {
                apisMsgEl.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）';
            });
    };
    tdOp.appendChild(delBtn);
    tr.appendChild(tdOp);
    apisTableBody.appendChild(tr);
}

function startApiEdit(api) {
    editingApiId = api.apiConfigId;
    document.getElementById('apiNewName').value = api.displayName || '';
    document.getElementById('apiNewType').value = api.apiType || '';
    document.getElementById('apiNewEndpoint').value = api.endpoint || '';
    document.getElementById('apiNewModel').value = api.defaultModel || '';
    document.getElementById('apiNewKey').value = '';
    document.getElementById('apiAddBtn').textContent = '保存';
    document.getElementById('apiCancelEdit').style.display = '';
    apisMsgEl.textContent = '编辑中: ' + api.displayName + '（Key 留空=保留原 Key）';
}

function cancelApiEdit() {
    editingApiId = '';
    document.getElementById('apiNewName').value = '';
    document.getElementById('apiNewType').value = '';
    document.getElementById('apiNewEndpoint').value = '';
    document.getElementById('apiNewModel').value = '';
    document.getElementById('apiNewKey').value = '';
    document.getElementById('apiAddBtn').textContent = '新建';
    document.getElementById('apiCancelEdit').style.display = 'none';
    apisMsgEl.textContent = '';
}

function apiSubmit() {
    var payload = {
        displayName: document.getElementById('apiNewName').value.trim(),
        apiType: document.getElementById('apiNewType').value.trim(),
        endpoint: document.getElementById('apiNewEndpoint').value.trim(),
        defaultModel: document.getElementById('apiNewModel').value.trim(),
        apiKey: document.getElementById('apiNewKey').value.trim()
    };
    var url = '/api/v1/llm-apis';
    if (editingApiId.length > 0) {
        url = '/api/v1/llm-apis/edit';
        payload.apiConfigId = editingApiId;
    }
    fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            apisMsgEl.textContent = d.ok ? (editingApiId.length > 0 ? '已保存' : '已新建') : '失败: ' + d.error;
            cancelApiEdit();
            loadApis();
        })
        .catch(function (e) {
            apisMsgEl.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）';
        });
}

// 设为默认——POST /api/v1/llm-apis/default（默认端点：QuickCat 语料面与未显式配置的猫固定走它；切换立即生效）
function setDefaultApi(api) {
    fetch('/api/v1/llm-apis/default', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ apiConfigId: api.apiConfigId })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            apisMsgEl.textContent = d.ok ? '默认端点已切换: ' + api.displayName : '失败: ' + d.error;
            loadApis();
        })
        .catch(function (e) {
            apisMsgEl.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）';
        });
}

document.getElementById('apiAddBtn').onclick = apiSubmit;
document.getElementById('apisRefresh').onclick = loadApis;
document.getElementById('apiCancelEdit').onclick = cancelApiEdit;
loadApis();

