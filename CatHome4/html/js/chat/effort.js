// ═══════════════════════════════════════════
// chat/effort.js —— 思考强度标签（侧翼件）
//
// 定位：顶栏 `[思考 档位]` 标签的**唯一实现处**——显示本猫 reasoningEffort 覆盖；点击循环切换（实时生效）。
// 数据源：GET  /api/v1/effort  → {"effort":"low|high|max|","global":"low|high|max"}
// 写面：  POST /api/v1/effort  → {"value":"low|high|max|"}（空串=清空覆盖，跟随全局）→ {"ok":true,"effort":"..."}
// 形态：DOM 槽位在 chat.html 顶栏（#chatEffort），样式 chat.css「思考强度标签」段；本件零新增结构
// 边界：档位真源在后端（cat.cfg reasoningEffort）——前端只渲染与投递，不本地推断；写后重读核对
// 失败面：读取失败回落占位并出声（不静默显示假值）；写入失败出声（不静默翻转）
// 循环口径（2026-11-04 · Rushiera 定）：三档循环 low → high → max（取消「跟随全局」常驻首选）；
//   「跟随全局」只在当前覆盖档与全局值恰好相同时插入循环——此时两者等效，多一档可显式回到跟随态。
// ═══════════════════════════════════════════

/// 三档循环序——覆盖档位集合（跟随全局不在其中；仅条件插入）
var EFFORT_STEPS = ['low', 'high', 'max'];

/// 标签文本——`思考 <档位>`；空档显示「跟随全局(<全局值>)」；非法 / 读取失败回落占位
function effortText(d) {
    if (!d || typeof d.effort !== 'string') {
        return '';
    }
    if (d.effort.length === 0) {
        var g = (typeof d.global === 'string' && d.global.length > 0) ? d.global : 'high';
        return '思考 跟随全局(' + g + ')';
    }
    return '思考 ' + d.effort;
}

/// 投影——文本 + 覆盖态类（本猫覆盖 = 高亮，跟随全局 = 常规）；元素缺失零动作
function effortRender(d) {
    var node = document.getElementById('chatEffort');
    if (!node) {
        return;
    }
    var text = effortText(d);
    node.textContent = (text.length > 0) ? text : '思考 —';
    if (d && typeof d.effort === 'string' && d.effort.length > 0) {
        node.classList.add('override');
    } else {
        node.classList.remove('override');
    }
}

/// 读取——档位真源投影；失败出声（回落占位）
function effortRefresh() {
    fetch('/api/v1/effort')
        .then(function (r) { return r.json(); })
        .then(function (d) { effortRender(d); })
        .catch(function (e) {
            effortRender(null);
            if (typeof warn === 'function') { warn('思考强度读取失败', e); }
        });
}

/// 循环档序——三档常驻；当前覆盖档与全局值恰好相同时，**紧接当前档之后**插入「跟随全局」（空串 = 跟随态）
function effortCycleList(d) {
    var list = EFFORT_STEPS.slice();
    var eff = (d && typeof d.effort === 'string') ? d.effort : '';
    var g = (d && typeof d.global === 'string') ? d.global : '';
    if (eff.length > 0 && g.length > 0 && eff === g) {
        var idx = list.indexOf(eff);
        list.splice(idx >= 0 ? idx + 1 : 0, 0, '');
    }
    return list;
}

/// 循环取下一档——当前档不在序中（跟随全局态 / 读取失败）按首项起步
function effortNext(d) {
    var list = effortCycleList(d);
    var eff = (d && typeof d.effort === 'string') ? d.effort : '';
    var idx = list.indexOf(eff);
    if (idx < 0) {
        return list[0];
    }
    return list[(idx + 1) % list.length];
}

/// 点击循环——先读真源定当前档，投递下一档，再重读核对（不做本地翻转）
function effortCycle() {
    fetch('/api/v1/effort')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            var next = effortNext(d);
            return fetch('/api/v1/effort', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ value: next })
            });
        })
        .then(function (r) { return r.json(); })
        .then(function (res) {
            if (res && res.ok === false && typeof warn === 'function') {
                warn('思考强度写入失败', res.error || '');
            }
            effortRefresh();
        })
        .catch(function (e) {
            if (typeof warn === 'function') { warn('思考强度切换失败', e); }
        });
}

/// 接线——点击循环 + 首读；本件加载于页面尾部，DOM 已就绪
function effortBind() {
    var node = document.getElementById('chatEffort');
    if (!node) {
        return;
    }
    node.addEventListener('click', effortCycle);
    effortRefresh();
}

effortBind();
