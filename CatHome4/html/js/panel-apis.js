// CH4 外观层——panel-apis.js：LLM API 池管理（config 页签）——panel.js 拆分（2026-09-08 体量治理）
// 依赖：app.js + panel.js 先加载（全局状态已就位）；本文件承载 API 池的列表（行内编辑）+ 新建 + 删除 + 连通性测试
// 2026-10-01 行内编辑轮：行内「编辑」按钮撤除——可编辑列直接在格内改（配置 ID 只读）；未改动 / 清空 = 不改动（不发请求）
// 2026-10-01 默认列轮：★ 独立为最左「默认」列——紫星=当前默认 / 灰星=非默认；「设为默认 / 已是默认」随星同列（原在操作列）
// 2026-10-01 备用轮：同列增「设为备用 / 取消备用」——绿菱 ◈=当前备用；默认与备用互斥（默认行不出备用入口）
// A299：QQ Bot 池整段撤除（QQ 域从宿主移除）——本文件回归纯 LLM API 面；QQ 侧复用点见 QQBotB 适配文档

// [段14] M3b LLM API 池管理（config 页签——行内编辑 + 新建 + 删除；key 掩码）
var apisTableBody = document.querySelector('#apisTable tbody');
var apisMsgEl = document.getElementById('apisMsg');

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

// 行内文本输入框——撑满单元格（列宽由 thead 控制）
function cellInput(value, placeholder) {
    var inp = document.createElement('input');
    inp.className = 'input-mini';
    inp.style.width = '100%';
    inp.value = value;
    inp.placeholder = placeholder;
    return inp;
}

function renderApiRow(api) {
    var tr = document.createElement('tr');
    // 原值对照面——未改动 / 清空判据；inputs 供整行提交取值
    var cells = {
        displayName: api.displayName || '',
        apiType: api.apiType || '',
        endpoint: api.endpoint || '',
        defaultModel: api.defaultModel || '',
        apiKey: api.apiKey || ''
    };
    var inputs = {};

    // 默认 / 备用列（最左）——紫星=当前默认 / 灰星=非默认；备用位绿菱 ◈ + 「取消备用」
    // 互斥：默认行不出备用入口（默认与备用不得兼）；备用行仍可「设为默认」（后端置默认时清备用标记）
    var tdDefault = document.createElement('td');
    tdDefault.style.whiteSpace = 'nowrap';
    var star = document.createElement('span');
    star.className = api.isDefault ? 'api-star-on' : 'api-star-off';
    star.textContent = '★';
    tdDefault.appendChild(star);
    if (api.isDefault) {
        var nowTag = document.createElement('span');
        nowTag.className = 'api-def-now';
        nowTag.textContent = '已是默认';
        tdDefault.appendChild(nowTag);
    } else {
        var defBtn = document.createElement('button');
        defBtn.textContent = '设为默认';
        defBtn.className = 'btn-mini accent';
        defBtn.style.marginLeft = '4px';
        defBtn.onclick = function () { setDefaultApi(api); };
        tdDefault.appendChild(defBtn);
        if (api.isBackup) {
            var bStar = document.createElement('span');
            bStar.className = 'api-backup-on';
            bStar.textContent = '◈';
            tdDefault.appendChild(bStar);
            var bOff = document.createElement('button');
            bOff.textContent = '取消备用';
            bOff.className = 'btn-mini';
            bOff.style.marginLeft = '4px';
            bOff.onclick = function () { setBackupApi(api, true); };
            tdDefault.appendChild(bOff);
        } else {
            var bBtn = document.createElement('button');
            bBtn.textContent = '设为备用';
            bBtn.className = 'btn-mini info';
            bBtn.style.marginLeft = '4px';
            bBtn.onclick = function () { setBackupApi(api, false); };
            tdDefault.appendChild(bBtn);
        }
    }
    tr.appendChild(tdDefault);

    var tdName = document.createElement('td');
    tdName.style.color = 'var(--ch-identity)';
    inputs.displayName = cellInput(cells.displayName, '名称');
    tdName.appendChild(inputs.displayName);
    tr.appendChild(tdName);

    var tdId = document.createElement('td');
    tdId.textContent = api.apiConfigId || '';
    tdId.style.color = 'var(--ch-fg-weak)';
    tdId.style.fontSize = '10px';
    tdId.style.wordBreak = 'break-all';
    tr.appendChild(tdId);

    var tdType = document.createElement('td');
    inputs.apiType = cellInput(cells.apiType, '类型（deepseek）');
    tdType.appendChild(inputs.apiType);
    tr.appendChild(tdType);

    var tdEndpoint = document.createElement('td');
    inputs.endpoint = cellInput(cells.endpoint, '端点 URL');
    tdEndpoint.appendChild(inputs.endpoint);
    tr.appendChild(tdEndpoint);

    var tdModel = document.createElement('td');
    inputs.defaultModel = cellInput(cells.defaultModel, '模型');
    tdModel.appendChild(inputs.defaultModel);
    tr.appendChild(tdModel);

    var tdKey = document.createElement('td');
    inputs.apiKey = cellInput(cells.apiKey, api.hasKey ? '留空=不改' : '（未配置）');
    tdKey.appendChild(inputs.apiKey);
    tr.appendChild(tdKey);

    // 行内编辑接线——未改动 / 清空都不提交（清空回落原值）
    wireInlineEdit(inputs.displayName, cells.displayName, function () { submitApiEdit(api, cells, inputs, '名称'); });
    wireInlineEdit(inputs.apiType, cells.apiType, function () { submitApiEdit(api, cells, inputs, '类型'); });
    wireInlineEdit(inputs.endpoint, cells.endpoint, function () { submitApiEdit(api, cells, inputs, '端点'); });
    wireInlineEdit(inputs.defaultModel, cells.defaultModel, function () { submitApiEdit(api, cells, inputs, '模型'); });
    wireInlineEdit(inputs.apiKey, cells.apiKey, function () { submitApiEdit(api, cells, inputs, 'Key'); });

    var tdOp = document.createElement('td');
    tdOp.style.whiteSpace = 'nowrap';
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

    // 测试列（最右）——未测=「测试」；已测=「内容报告」（点击开弹窗看结果，弹窗内可重新测试）
    var tdTest = document.createElement('td');
    tdTest.style.whiteSpace = 'nowrap';
    var testBtn = document.createElement('button');
    applyProbeButtonState(api, testBtn);
    apiProbeBtns[api.apiConfigId] = testBtn;
    testBtn.onclick = function () {
        var cached = apiProbeCache[api.apiConfigId];
        if (cached) {
            openApiProbe(api, cached);
            return;
        }
        runApiProbe(api);
    };
    tdTest.appendChild(testBtn);
    tr.appendChild(tdTest);

    apisTableBody.appendChild(tr);
}

// 行内提交——整行字段一起送（后端按字段整体写入）；apiKey 未改动送空串（空=保留原 key）
function submitApiEdit(api, cells, inputs, label) {
    var keyVal = inputs.apiKey.value.trim();
    var payload = {
        apiConfigId: api.apiConfigId,
        displayName: inputs.displayName.value.trim(),
        apiType: inputs.apiType.value.trim(),
        endpoint: inputs.endpoint.value.trim(),
        defaultModel: inputs.defaultModel.value.trim(),
        apiKey: keyVal === cells.apiKey ? '' : keyVal
    };
    apisMsgEl.textContent = '保存中…（' + label + '）';
    fetch('/api/v1/llm-apis/edit', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            apisMsgEl.textContent = d.ok ? ('已保存（' + label + '）') : '保存失败: ' + d.error;
            loadApis();
        })
        .catch(function (e) {
            apisMsgEl.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）';
        });
}

// 新建——底部表单（行内编辑后表单只承担新建；Key 可留空）
function apiSubmit() {
    var payload = {
        displayName: document.getElementById('apiNewName').value.trim(),
        apiType: document.getElementById('apiNewType').value.trim(),
        endpoint: document.getElementById('apiNewEndpoint').value.trim(),
        defaultModel: document.getElementById('apiNewModel').value.trim(),
        apiKey: document.getElementById('apiNewKey').value.trim()
    };
    fetch('/api/v1/llm-apis', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            apisMsgEl.textContent = d.ok ? '已新建' : '失败: ' + d.error;
            if (d.ok) {
                document.getElementById('apiNewName').value = '';
                document.getElementById('apiNewType').value = '';
                document.getElementById('apiNewEndpoint').value = '';
                document.getElementById('apiNewModel').value = '';
                document.getElementById('apiNewKey').value = '';
                loadApis();
            }
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

// 设为备用 / 取消备用——POST /api/v1/llm-apis/backup（唯一备用；与默认互斥——默认端点后端拒绝）
// 备用语义：会话遇上游异常时临时切换的候选站（探测判据成立才切；180 秒后自动回默认）
function setBackupApi(api, clear) {
    var payload = { apiConfigId: clear ? '' : api.apiConfigId };
    fetch('/api/v1/llm-apis/backup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (d.ok) {
                apisMsgEl.textContent = clear ? '已取消备用端点' : ('备用端点已设为: ' + api.displayName);
            } else {
                apisMsgEl.textContent = '失败: ' + d.error;
            }
            loadApis();
        })
        .catch(function (e) {
            apisMsgEl.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）';
        });
}

document.getElementById('apiAddBtn').onclick = apiSubmit;
document.getElementById('apisRefresh').onclick = loadApis;
loadApis();

// [段14a] LLM API 池连通性测试——「测试」按钮 → 弹窗报告（模型清单 / 站点信息 / 定价与分组）
// 按钮两态：未测=「测试」（点击跑一次）；已测=「内容报告」（点击开弹窗看上次结果，弹窗内可重新测试）
// 结果缓存在页面内存（apiProbeCache）——刷新页面即清空（重新按钮回到「测试」态）
var apiProbeCache = {};
var apiProbeBtns = {};
var apiProbeOpen = null;
var apiProbeModal = document.getElementById('apiProbeModal');
var apiProbeBody = document.getElementById('apiProbeBody');
var apiProbeMsg = document.getElementById('apiProbeMsg');

function applyProbeButtonState(api, btn) {
    var cached = apiProbeCache[api.apiConfigId];
    if (cached) {
        btn.textContent = '内容报告';
        btn.className = cached.ok ? 'btn-mini accent' : 'btn-mini';
        btn.title = '上次测试 ' + (cached.probedAt || '') + '（点击查看，可重新测试）';
    } else {
        btn.textContent = '测试';
        btn.className = 'btn-mini';
        btn.title = '测试该端点：模型清单 / 站点信息 / 定价与分组';
    }
}

// 跑一次测试——三探由后端完成（模型清单带 Key，站点信息与定价免 Key）；行内按钮态随之刷新
function runApiProbe(api) {
    var btn = apiProbeBtns[api.apiConfigId];
    if (btn) {
        btn.disabled = true;
        btn.textContent = '测试中…';
    }
    apiProbeMsg.textContent = '测试中…（' + (api.displayName || '') + '）';
    fetch('/api/v1/llm-apis/probe', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ apiConfigId: api.apiConfigId })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (btn) { btn.disabled = false; }
            if (!d || (d.ok === false && d.error)) {
                if (btn) { applyProbeButtonState(api, btn); }
                apiProbeMsg.textContent = '测试失败: ' + ((d && d.error) || '无响应');
                return;
            }
            apiProbeCache[api.apiConfigId] = d;
            if (btn) { applyProbeButtonState(api, btn); }
            openApiProbe(api, d);
        })
        .catch(function (e) {
            if (btn) {
                btn.disabled = false;
                applyProbeButtonState(api, btn);
            }
            apiProbeMsg.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）';
        });
}

// 弹窗——渲染报告（概览 + 三段）
function openApiProbe(api, report) {
    apiProbeOpen = api;
    document.getElementById('apiProbeTitle').textContent = (api.displayName || '') + ' — ' + (api.endpoint || '');
    apiProbeMsg.textContent = '探于 ' + (report.probedAt || '') + ' · 耗时 ' + (report.elapsedMs || 0) + ' ms';
    apiProbeBody.textContent = '';
    apiProbeBody.appendChild(buildProbeOverview(api, report));
    apiProbeBody.appendChild(buildProbeModelsSection(report.models));
    apiProbeBody.appendChild(buildProbeSiteSection(report.site));
    apiProbeBody.appendChild(buildProbePricingSection(report.pricing));
    apiProbeModal.style.display = 'flex';
}

// 概览段——显示名 / 端点 / 默认模型 / Key / 结果状态
function buildProbeOverview(api, report) {
    var box = document.createElement('div');
    box.style.cssText = 'border:1px solid var(--ch-line);border-radius:6px;padding:8px;margin-bottom:8px';
    var rows = [
        ['端点', report.endpoint || api.endpoint || ''],
        ['默认模型', report.defaultModel || ''],
        ['Key', report.hasKey ? '已配置' : '未配置（模型探可能 401）'],
        ['模型清单 URL', report.modelsUrl || ''],
        ['站点根 URL', report.siteOrigin || ''],
        ['结论', report.ok ? '模型探通过（Key 有效）' : '模型探未通过——见下方分段']
    ];
    for (var i = 0; i < rows.length; i++) {
        box.appendChild(probeKVRow(rows[i][0], rows[i][1], i === 5 ? (report.ok ? 'var(--ch-ok)' : 'var(--ch-err)') : ''));
    }
    return box;
}

function probeKVRow(key, value, color) {
    var row = document.createElement('div');
    row.style.cssText = 'display:flex;gap:8px;line-height:1.8';
    var k = document.createElement('span');
    k.style.cssText = 'color:var(--ch-fg-weak);min-width:110px';
    k.textContent = key;
    var v = document.createElement('span');
    v.style.cssText = 'word-break:break-all;flex:1';
    if (color) { v.style.color = color; }
    v.textContent = value;
    row.appendChild(k);
    row.appendChild(v);
    return row;
}

// 分段外壳——标题 + 状态徽标 + 内容位
function probeSection(title, ok, status) {
    var sec = document.createElement('div');
    sec.style.cssText = 'border:1px solid var(--ch-line);border-radius:6px;padding:8px;margin-bottom:8px';
    var head = document.createElement('div');
    head.style.cssText = 'display:flex;align-items:center;gap:6px;margin-bottom:4px';
    var t = document.createElement('span');
    t.style.cssText = 'color:var(--ch-identity);font-weight:bold';
    t.textContent = title;
    var badge = document.createElement('span');
    badge.style.color = ok ? 'var(--ch-ok)' : 'var(--ch-err)';
    badge.textContent = ok ? '通过' : '未通过';
    head.appendChild(t);
    head.appendChild(badge);
    if (status) {
        var st = document.createElement('span');
        st.style.cssText = 'color:var(--ch-fg-weak)';
        st.textContent = status;
        head.appendChild(st);
    }
    sec.appendChild(head);
    return sec;
}

// 模型清单段——一个模型一行（id + 归属 + 支持协议）
function buildProbeModelsSection(sec) {
    var ok = sec && sec.ok;
    var box = probeSection('可用模型（/v1/models）', ok, sec && sec.httpStatus ? ('HTTP ' + sec.httpStatus) : '');
    if (!ok) {
        box.appendChild(probeErrLine(sec));
        return box;
    }
    var items = sec.payload || [];
    var summary = document.createElement('div');
    summary.style.color = 'var(--ch-fg-weak)';
    summary.textContent = '该 Key 可见 ' + items.length + ' 个模型';
    box.appendChild(summary);
    for (var i = 0; i < items.length; i++) {
        var it = items[i] || {};
        var line = document.createElement('div');
        line.style.cssText = 'line-height:1.8;word-break:break-all';
        var name = document.createElement('span');
        name.style.color = 'var(--ch-ok)';
        name.textContent = it.id || '(无名)';
        line.appendChild(name);
        var extra = [];
        if (it.ownedBy) { extra.push('归属 ' + it.ownedBy); }
        if (it.endpointTypes && it.endpointTypes.length) { extra.push('协议 ' + it.endpointTypes.join('/')); }
        if (extra.length) {
            var ex = document.createElement('span');
            ex.style.cssText = 'color:var(--ch-fg-weak);margin-left:8px';
            ex.textContent = extra.join(' · ');
            line.appendChild(ex);
        }
        box.appendChild(line);
    }
    return box;
}

// 站点信息段——new-api /api/status 字段表（公告已由后端剔除）
function buildProbeSiteSection(sec) {
    var ok = sec && sec.ok;
    var box = probeSection('站点信息（/api/status）', ok, sec && sec.httpStatus ? ('HTTP ' + sec.httpStatus) : '');
    if (!ok) {
        box.appendChild(probeErrLine(sec));
        return box;
    }
    var fields = sec.payload || {};
    var table = document.createElement('table');
    table.className = 'box-table';
    table.style.marginTop = '4px';
    var tbody = document.createElement('tbody');
    var keys = Object.keys(fields).sort();
    for (var i = 0; i < keys.length; i++) {
        var tr = document.createElement('tr');
        var tdK = document.createElement('td');
        tdK.style.cssText = 'width:30%;color:var(--ch-fg-weak);vertical-align:top';
        tdK.textContent = keys[i];
        var tdV = document.createElement('td');
        tdV.style.cssText = 'word-break:break-all';
        tdV.textContent = probeValueText(fields[keys[i]]);
        tr.appendChild(tdK);
        tr.appendChild(tdV);
        tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    box.appendChild(table);
    return box;
}

// 定价与分组段——分组倍率 + 模型倍率 + 分组说明 + 供应商
function buildProbePricingSection(sec) {
    var ok = sec && sec.ok;
    var box = probeSection('定价与分组（/api/pricing）', ok, sec && sec.httpStatus ? ('HTTP ' + sec.httpStatus) : '');
    if (!ok) {
        box.appendChild(probeErrLine(sec));
        return box;
    }
    var p = sec.payload || {};
    // 分组倍率
    var groupRatio = p.groupRatio || {};
    var groupKeys = Object.keys(groupRatio);
    if (groupKeys.length > 0) {
        box.appendChild(probeSubTitle('分组倍率'));
        var gTable = probeTable(['分组', '倍率', '说明'], buildGroupRatioRows(groupKeys, groupRatio, p.usableGroup || {}));
        box.appendChild(gTable);
    }
    // 模型倍率
    var models = p.models || [];
    if (models.length > 0) {
        box.appendChild(probeSubTitle('模型倍率（' + models.length + ' 项）'));
        box.appendChild(probeTable(['模型', '倍率', '补全比', '缓存比', '单价', '可用分组'], buildPricingRows(models)));
    }
    // 供应商
    var vendors = p.vendors || [];
    if (vendors.length > 0) {
        var names = [];
        for (var v = 0; v < vendors.length; v++) {
            if (vendors[v] && vendors[v].name) { names.push(vendors[v].name); }
        }
        if (names.length > 0) {
            box.appendChild(probeSubTitle('供应商：' + names.join(' / ')));
        }
    }
    // 支持协议
    var ep = p.supportedEndpoint || {};
    var epKeys = Object.keys(ep);
    if (epKeys.length > 0) {
        var parts = [];
        for (var e = 0; e < epKeys.length; e++) {
            var one = ep[epKeys[e]] || {};
            parts.push(epKeys[e] + ' → ' + (one.path || '') + ' ' + (one.method || ''));
        }
        box.appendChild(probeSubTitle('支持协议：' + parts.join(' · ')));
    }
    return box;
}

function buildGroupRatioRows(keys, ratio, usable) {
    var rows = [];
    for (var i = 0; i < keys.length; i++) {
        rows.push([keys[i], probeValueText(ratio[keys[i]]), usable[keys[i]] || '']);
    }
    return rows;
}

function buildPricingRows(models) {
    var rows = [];
    for (var i = 0; i < models.length; i++) {
        var m = models[i] || {};
        var groups = m.enable_groups || [];
        rows.push([
            m.model_name || '',
            probeValueText(m.model_ratio),
            probeValueText(m.completion_ratio),
            probeValueText(m.cache_ratio),
            probeValueText(m.model_price),
            groups.join(' / ')
        ]);
    }
    return rows;
}

function probeSubTitle(text) {
    var el = document.createElement('div');
    el.style.cssText = 'margin-top:6px;color:var(--ch-warn)';
    el.textContent = text;
    return el;
}

function probeTable(headers, rows) {
    var table = document.createElement('table');
    table.className = 'box-table';
    table.style.marginTop = '4px';
    var thead = document.createElement('thead');
    var htr = document.createElement('tr');
    for (var h = 0; h < headers.length; h++) {
        var th = document.createElement('th');
        th.textContent = headers[h];
        htr.appendChild(th);
    }
    thead.appendChild(htr);
    table.appendChild(thead);
    var tbody = document.createElement('tbody');
    for (var r = 0; r < rows.length; r++) {
        var tr = document.createElement('tr');
        for (var c = 0; c < rows[r].length; c++) {
            var td = document.createElement('td');
            td.style.cssText = 'word-break:break-all;vertical-align:top';
            td.textContent = rows[r][c];
            tr.appendChild(td);
        }
        tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    return table;
}

function probeErrLine(sec) {
    var el = document.createElement('div');
    el.style.cssText = 'color:var(--ch-err);word-break:break-all';
    el.textContent = (sec && sec.error) ? sec.error : '未取到数据';
    return el;
}

// 值文本化——标量直出；数组/对象转 JSON（截断防刷屏）
function probeValueText(value) {
    if (value === null || typeof value === 'undefined') { return ''; }
    if (typeof value === 'object') {
        var text = JSON.stringify(value);
        if (text.length > 160) { return text.substring(0, 160) + '…'; }
        return text;
    }
    return String(value);
}

document.getElementById('apiProbeClose').onclick = function () { apiProbeModal.style.display = 'none'; };
document.getElementById('apiProbeRerun').onclick = function () {
    if (!apiProbeOpen) { return; }
    runApiProbe(apiProbeOpen);
};
