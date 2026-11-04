// ═══════════════════════════════════════════
// chat/apirole.js —— 端点角色 + 模型标签（侧翼件）
//
// 定位：顶栏 `[API:端点名[模型]]` 标签的**唯一实现处**——显示当前生效端点（名称 + 模型）。
// 分区点击（2026-11-04 · Rushiera 定）：标签块**大小不变**，左 80% = 主要 / 备用对调；右 20%（括号右侧小三角）= 展开模型下拉。
// 数据源：GET  /api/v1/api-role    → {"role","primary","backup","primaryName","backupName","activeName","activeModel","override"}
//         GET  /api/v1/api-models  → {"ok","endpointName","current","models":[名称]}
// 写面：  POST /api/v1/api-role/toggle（对调主备）· POST /api/v1/api-model（会话级临时模型；不回写端点配置）
// 作用域（2026-11-04 · Rushiera 定）：临时模型**页面级**——选择存前端内存（apiModelPick）；
//   新会话（session.new）但页面不关 → 沿用（发送前重新同步到后端）；页面关闭再打开 → 归位（页载时清后端覆盖 + 清内存）。
// 失败面：读取失败回落占位并出声；模型清单失败出声并给出原因（不静默给空列表）。
// ═══════════════════════════════════════════

/// 页面级内存态——当前选中的临时模型（空串=跟随端点默认）。关页即失（归位）。
var apiModelPick = '';
/// 已确认写入后端的值——与后端一致时不重复投递（发送路径零额外请求）
var apiModelSynced = '';
/// 脏标记——true 时下次发送前补投（选型后 / 新会话后）
var apiModelDirty = false;

/// 标签显示用的模型名——页面内存态优先 → 后端覆盖值 → 后端生效模型
function apiModelShown(d) {
    if (apiModelPick.length > 0) {
        return apiModelPick;
    }
    if (d && typeof d.override === 'string' && d.override.length > 0) {
        return d.override;
    }
    return (d && typeof d.activeModel === 'string') ? d.activeModel : '';
}

/// 标签文本——`API:端点名[模型]`；端点名缺失回落「未配置」；模型缺失回落「未配置」
function apiRoleText(d) {
    if (!d || (d.role !== '主要' && d.role !== '备用')) {
        return '';
    }
    var name = (typeof d.activeName === 'string' && d.activeName.length > 0) ? d.activeName : '未配置';
    var model = apiModelShown(d);
    if (!model || model.length === 0) {
        model = '未配置';
    }
    return 'API:' + name + '[' + model + ']';
}

/// 投影——文本 + 备用态类（备用 = 绿，与 API 池备用标记同色系）；元素缺失零动作
function apiRoleRender(d) {
    var node = document.getElementById('chatApiRole');
    if (!node) {
        return;
    }
    var main = node.querySelector('.apirole-main');
    if (main) {
        var text = apiRoleText(d);
        main.textContent = (text.length > 0) ? text : 'API —';
    }
    if (d && d.role === '备用') {
        node.classList.add('backup');
    } else {
        node.classList.remove('backup');
    }
}

/// 读取——端点真源投影；失败出声（回落占位）
function apiRoleRefresh() {
    fetch('/api/v1/api-role')
        .then(function (r) { return r.json(); })
        .then(function (d) { apiRoleRender(d); })
        .catch(function (e) {
            apiRoleRender(null);
            if (typeof warn === 'function') { warn('端点角色读取失败', e); }
        });
}

/// 手动对调——投递后重读（真源在后端；不做本地翻转）
function apiRoleToggle() {
    fetch('/api/v1/api-role/toggle', { method: 'POST' })
        .then(function (r) { return r.json(); })
        .then(function () {
            apiRoleRefresh();
            apiModelsClose();
        })
        .catch(function (e) {
            if (typeof warn === 'function') { warn('端点角色对调失败', e); }
        });
}

/// 模型下拉——浮层容器（DOM 已就绪则复用）
function apiModelsPanel() {
    var panel = document.getElementById('apiModelsPanel');
    if (!panel) {
        panel = document.createElement('div');
        panel.id = 'apiModelsPanel';
        panel.className = 'apirole-models';
        panel.style.display = 'none';
        document.body.appendChild(panel);
    }
    return panel;
}

/// 关闭模型下拉
function apiModelsClose() {
    var panel = document.getElementById('apiModelsPanel');
    if (panel) {
        panel.style.display = 'none';
        panel.textContent = '';
    }
}

/// 写入临时模型——页面内存态 + 会话级投递（不回写端点配置）；写后重读标签
function apiModelSelect(model) {
    var value = (typeof model === 'string') ? model : '';
    apiModelPick = value;
    apiModelDirty = true;
    return apiModelPost(value);
}

/// 投递临时模型到后端（会话级；不回写端点配置）；成功后对齐 synced 态
function apiModelPost(value) {
    return fetch('/api/v1/api-model', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: value })
    })
        .then(function (r) { return r.json(); })
        .then(function (res) {
            if (res && res.ok === false) {
                if (typeof warn === 'function') {
                    warn('模型切换失败', res.error || '');
                }
                return;
            }
            apiModelSynced = value;
            apiModelDirty = false;
            apiModelsClose();
            apiRoleRefresh();
        })
        .catch(function (e) {
            if (typeof warn === 'function') { warn('模型切换失败', e); }
        });
}

/// 发送前同步——仅脏标记为真时补投（新会话清过后端覆盖 → 发送前补投，保证「页不关即沿用」）
function apiModelSync() {
    if (!apiModelDirty) {
        return Promise.resolve(null);
    }
    return apiModelPost(apiModelPick);
}

/// 页载归位——清后端会话级覆盖 + 清页面内存态（关页再打开即回默认）
function apiModelResetOnLoad() {
    apiModelPick = '';
    apiModelSynced = '';
    apiModelDirty = false;
    return fetch('/api/v1/api-model', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: '' })
    })
        .then(function (r) { return r.json(); })
        .then(function () { apiRoleRefresh(); })
        .catch(function (e) {
            if (typeof warn === 'function') { warn('模型归位失败', e); }
        });
}

/// 展开模型下拉——清单由上游实时探得（GET /models）；单选 radio；当前生效项打勾
function apiModelsOpen() {
    var panel = apiModelsPanel();
    panel.textContent = '';
    panel.style.display = 'block';
    var head = document.createElement('div');
    head.className = 'apirole-models-head';
    head.textContent = '模型（仅本会话页有效）';
    panel.appendChild(head);
    var body = document.createElement('div');
    body.className = 'apirole-models-body';
    body.textContent = '载入中…';
    panel.appendChild(body);
    // 定位——贴着标签下沿
    var anchor = document.getElementById('chatApiRole');
    if (anchor && anchor.getBoundingClientRect) {
        var rect = anchor.getBoundingClientRect();
        panel.style.left = rect.left + 'px';
        panel.style.top = (rect.bottom + 4) + 'px';
    }
    fetch('/api/v1/api-models')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            body.textContent = '';
            if (!d || d.ok === false) {
                var err = document.createElement('div');
                err.className = 'apirole-models-err';
                err.textContent = '模型清单不可用：' + ((d && d.error) ? d.error : '未知错误');
                body.appendChild(err);
                return;
            }
            var list = d.models || [];
            var current = apiModelPick.length > 0 ? apiModelPick : ((typeof d.current === 'string') ? d.current : '');
            // 「跟随端点默认」项——空串 = 清覆盖
            var follow = document.createElement('label');
            follow.className = 'apirole-models-item';
            var followRadio = document.createElement('input');
            followRadio.type = 'radio';
            followRadio.name = 'apiModelPick';
            followRadio.value = '';
            followRadio.checked = (apiModelPick.length === 0);
            followRadio.addEventListener('change', function () { apiModelSelect(''); });
            follow.appendChild(followRadio);
            var followText = document.createElement('span');
            followText.textContent = '跟随端点默认（' + (current.length > 0 ? current : '未配置') + '）';
            follow.appendChild(followText);
            body.appendChild(follow);
            for (var i = 0; i < list.length; i++) {
                var name = list[i];
                var item = document.createElement('label');
                item.className = 'apirole-models-item';
                var radio = document.createElement('input');
                radio.type = 'radio';
                radio.name = 'apiModelPick';
                radio.value = name;
                radio.checked = (name === apiModelPick);
                radio.addEventListener('change', function () { apiModelSelect(name); });
                item.appendChild(radio);
                var text = document.createElement('span');
                text.textContent = name;
                item.appendChild(text);
                body.appendChild(item);
            }
            if (list.length === 0) {
                var empty = document.createElement('div');
                empty.className = 'apirole-models-err';
                empty.textContent = '上游未返回模型清单';
                body.appendChild(empty);
            }
        })
        .catch(function (e) {
            body.textContent = '';
            var err = document.createElement('div');
            err.className = 'apirole-models-err';
            err.textContent = '模型清单读取失败（' + e.message + '）';
            body.appendChild(err);
        });
}

/// 接线——左区对调 + 右区下拉；点击外部关闭下拉；本件加载于页面尾部，DOM 已就绪
function apiRoleBind() {
    var node = document.getElementById('chatApiRole');
    if (!node) {
        return;
    }
    var main = node.querySelector('.apirole-main');
    var caret = node.querySelector('.apirole-caret');
    if (main) {
        main.addEventListener('click', apiRoleToggle);
    }
    if (caret) {
        caret.addEventListener('click', function (e) {
            e.stopPropagation();
            var panel = document.getElementById('apiModelsPanel');
            if (panel && panel.style.display === 'block') {
                apiModelsClose();
            } else {
                apiModelsOpen();
            }
        });
    }
    document.addEventListener('click', function (e) {
        var panel = document.getElementById('apiModelsPanel');
        if (!panel || panel.style.display !== 'block') {
            return;
        }
        var el = e.target;
        while (el && el.tagName) {
            if (el.id === 'apiModelsPanel' || el.id === 'chatApiRole') {
                return;
            }
            el = el.parentNode;
        }
        apiModelsClose();
    });
    apiModelResetOnLoad();
    apiRoleRefresh();
}

apiRoleBind();
