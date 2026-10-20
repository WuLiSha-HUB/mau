// CH4 外观层——panel-catcfg.js：每猫配置弹层 + 新猫默认模板 + 受控根编辑面（M3c/M3d/M4e）——panel.js 拆分（2026-09-08 体量治理）
// 依赖：app.js + panel.js 先加载（全局状态已就位；renderCatCard 点击时调用本文件 openCatCfg）；本文件承载 cat.cfg 配置弹层与 workspace 管理
// 段15（M3c 每猫配置）+ 段[受控根编辑面]（M3d）原样迁出，逻辑零改动

// [段15] M3c 每猫配置弹层——API 下拉/人设/工具勾选/前文 List
var catCfgModal = document.getElementById('catCfgModal');
var catCfgTarget = '';
var catCfgAllTools = [];
var catCfgInjectList = [];
var catCfgAllPacks = [];
// A107——工具组定义缺陷（后端 defectGroups——不可用组不进勾选面 + 弹层提示）
var catCfgDefectGroups = [];

function openCatCfg(catId, name) {
    catCfgTarget = catId;
    document.getElementById('catCfgTitle').textContent = name + ' (' + catId + ')';
    document.getElementById('catCfgMsg').textContent = '';
    fetch('/api/v1/cat-config?cat=' + encodeURIComponent(catId))
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                document.getElementById('catCfgMsg').textContent = '读取失败: ' + (d.error || '');
                catCfgModal.style.display = 'flex';
                return;
            }
            catCfgAllTools = d.allTools || d.allToolNames || [];
            catCfgDefectGroups = d.defectGroups || [];
            renderToolDefectNote(catCfgDefectGroups, d.staleToolNames || []);
            renderApiOptions(d.apiOptions || [], d.apiConfigId || '');
            renderQqBotOptions(d.qqbotOptions || [], d.qqbotId || '');
            renderAppearanceOptions(d.appearanceOptions || [], d.appearanceId || '');
            document.getElementById('catCfgQqEnable').checked = !!d.qqbotEnable;
            document.getElementById('catCfgIdleOnly').checked = !!d.userInjectIdleOnly;
            renderEffortOptions(d.reasoningEffort || '', d.defaultReasoningEffort || '');
            document.getElementById('catCfgName').value = d.displayName || '';
            document.getElementById('catCfgType').value = d.type || '';
            document.getElementById('catCfgPersona').value = d.persona || '';
            renderToolChecks(d.toolNames || '');
            renderInjectList(d.injectList || []);
            catCfgAllPacks = d.allPacks || [];
            renderPackChecks(catCfgAllPacks, d.packs || []);
            // M4e 猫级白名单——启用根勾选（workspace 强制常驻不可取消）
            renderRootChecks(d.allRoots || [], d.enabledRoots || []);
            catCfgModal.style.display = 'flex';
        })
        .catch(function () {
            document.getElementById('catCfgMsg').textContent = '读取失败——宿主未运行？';
            catCfgModal.style.display = 'flex';
        });
}

// M3c API 下拉——首项「默认（跟随全局）」= 零值语义（后端 apiConfigId 清空 → 走 ResolveDefault 实时解析）
function isDefaultApiValue(value) {
    return !value || value.length === 0 || value === '00000000-0000-0000-0000-000000000000';
}

function renderApiOptions(options, current) {
    var sel = document.getElementById('catCfgApi');
    sel.textContent = '';
    var def = document.createElement('option');
    def.value = '';
    // A50——默认项带出全局默认端点名（用户可见"跟随的是谁"；未设默认时保留抽象文案）
    var defLabel = '（默认 · 跟随全局默认端点）';
    for (var k = 0; k < options.length; k++) {
        if (options[k].isDefault) { defLabel = '（默认 · 跟随全局 → ' + options[k].displayName + '）'; }
    }
    def.textContent = defLabel;
    if (isDefaultApiValue(current)) { def.selected = true; }
    sel.appendChild(def);
    for (var i = 0; i < options.length; i++) {
        var opt = document.createElement('option');
        opt.value = options[i].apiConfigId;
        var label = options[i].displayName + ' (' + options[i].apiConfigId.substring(0, 8) + '…)';
        if (options[i].isDefault) { label = label + ' · 全局默认'; }
        opt.textContent = label;
        if (options[i].apiConfigId === current) { opt.selected = true; }
        sel.appendChild(opt);
    }
}

// A244 外观下拉——首项「默认 · 跟随全局默认外观」= 零值语义（空 → 后端 ResolveDefault 实时解析）
function renderAppearanceOptions(options, current) {
    var sel = document.getElementById('catCfgAppearance');
    sel.textContent = '';
    var def = document.createElement('option');
    def.value = '';
    var defLabel = '（默认 · 跟随全局默认外观）';
    for (var k = 0; k < options.length; k++) {
        if (options[k].isDefault) { defLabel = '（默认 · 跟随全局 → ' + options[k].displayName + '）'; }
    }
    def.textContent = defLabel;
    if (isDefaultApiValue(current)) { def.selected = true; }
    sel.appendChild(def);
    for (var i = 0; i < options.length; i++) {
        var opt = document.createElement('option');
        opt.value = options[i].appearanceId;
        var label = options[i].displayName + ' (' + options[i].appearanceId.substring(0, 8) + '…)';
        if (options[i].isDefault) { label = label + ' · 全局默认'; }
        opt.textContent = label;
        if (options[i].appearanceId === current) { opt.selected = true; }
        sel.appendChild(opt);
    }
}

// A234 思考强度下拉——首项「跟随全局」= 零值语义（空 → 后端回落全局 llm.reasoning_effort）
function renderEffortOptions(current, globalValue) {
    var sel = document.getElementById('catCfgEffort');
    sel.textContent = '';
    var def = document.createElement('option');
    def.value = '';
    def.textContent = '（默认 · 跟随全局 → ' + (globalValue || 'high') + '）';
    if (!current || current.length === 0) { def.selected = true; }
    sel.appendChild(def);
    var values = ['low', 'high', 'max'];
    for (var i = 0; i < values.length; i++) {
        var opt = document.createElement('option');
        opt.value = values[i];
        opt.textContent = values[i];
        if (values[i] === current) { opt.selected = true; }
        sel.appendChild(opt);
    }
}

// R2.3 QQ Bot 下拉——空选项=未绑定；多猫可绑同一 Bot
function renderQqBotOptions(options, current) {
    var sel = document.getElementById('catCfgQqBot');
    sel.textContent = '';
    var none = document.createElement('option');
    none.value = '';
    none.textContent = '（未绑定）';
    if (!current || current.length === 0) { none.selected = true; }
    sel.appendChild(none);
    for (var i = 0; i < options.length; i++) {
        var opt = document.createElement('option');
        opt.value = options[i].qqBotId;
        var label = options[i].displayName + ' (' + options[i].qqBotId.substring(0, 8) + '…)';
        // A58 1:1——一只 Bot 只能绑一只猫：已被他猫占用 → 标注并禁用（后端保存时仍显式校验）
        if (options[i].boundCat && options[i].qqBotId !== current) {
            label = label + '（已被 ' + options[i].boundCat + ' 绑定）';
            opt.disabled = true;
        }
        opt.textContent = label;
        if (options[i].qqBotId === current) { opt.selected = true; }
        sel.appendChild(opt);
    }
}

function parseToolChecked(toolNames) {
    var checked = {};
    if (toolNames && toolNames.length > 0 && toolNames !== '*') {
        var parts = toolNames.split(',');
        for (var j = 0; j < parts.length; j++) {
            var n = parts[j].trim();
            if (n.length > 0) { checked[n] = true; }
        }
    }
    return checked;
}

// A127——执行序徽标（配置面显示工具 order 层级；空值 = 不渲染，零猜测）
function toolOrderBadge(order) {
    if (!order || String(order).length === 0) { return null; }
    var el = document.createElement('span');
    el.className = 'tool-order';
    el.textContent = '⚙' + order;
    return el;
}

// 通用分组勾选渲染——优先按工具池 group 字段分组（allTools: [{name,group}]——design-ch4-tools-pool §六）；
// 兼容纯名数组（allToolNames: string[]）回退 - 前缀分组（text-*/mau-* 视为一组；无前缀归 other）；组头开关一键全组
function renderGroupedChecks(boxId, allTools, checked, privilegedMode) {
    var box = document.getElementById(boxId);
    box.textContent = '';
    // 特权面三态（2026-09-29）——'on'=默认猫面（已选 + 禁用）；'off'=其他猫面与模板面（未选 + 禁用）
    if (privilegedMode !== 'on') { privilegedMode = 'off'; }
    var groups = {};
    var order = [];
    // A107——不可用工具组直接不加载（后端池派生已不含；此处为契约显式化，防两面漂移）
    var blocked = {};
    for (var b = 0; b < catCfgDefectGroups.length; b++) {
        blocked[catCfgDefectGroups[b].group] = true;
    }
    for (var k = 0; k < allTools.length; k++) {
        var item = allTools[k];
        var toolName = typeof item === 'string' ? item : (item && item.name) || '';
        if (toolName.length === 0) { continue; }
        var isPriv = typeof item === 'object' && item.privileged === true;
        var group;
        if (typeof item === 'object' && item.group) {
            group = item.group;               // 工具池组别（TextCat/PsCat/...）——统一接口
        } else {
            var dash = toolName.indexOf('-');
            group = dash > 0 ? toolName.substring(0, dash) : 'other';  // 回退前缀
        }
        if (blocked[group] === true) { continue; }
        if (!groups[group]) { groups[group] = []; order.push(group); }
        groups[group].push({ name: toolName, privileged: isPriv, order: (typeof item === 'object' && item.order !== undefined && item.order !== null) ? String(item.order) : '' });
    }
    for (var g = 0; g < order.length; g++) {
        (function (groupName, tools) {
            var head = document.createElement('label');
            head.style.cssText = 'display:flex;align-items:center;gap:4px;width:100%;background:var(--ch-bg-elevated);border:1px solid var(--ch-line-strong);border-radius:4px;padding:3px 8px;font-size:var(--ch-fs-tag);color:var(--ch-warn);cursor:pointer;font-weight:bold';
            var hcb = document.createElement('input');
            hcb.type = 'checkbox';
            head.appendChild(hcb);
            head.appendChild(document.createTextNode(groupName + '-*（' + tools.length + '）'));
            box.appendChild(head);
            var groupBox = document.createElement('div');
            groupBox.style.cssText = 'display:flex;flex-wrap:wrap;gap:6px;margin:4px 0 8px 12px;width:100%';
            var cbs = [];
            for (var t = 0; t < tools.length; t++) {
                (function (tool) {
                    var label = document.createElement('label');
                    label.style.cssText = 'display:flex;align-items:center;gap:4px;background:var(--ch-bg-chip);border:1px solid var(--ch-line);border-radius:4px;padding:3px 8px;font-size:var(--ch-fs-tag);color:var(--ch-fg);cursor:pointer';
                    var cb = document.createElement('input');
                    cb.type = 'checkbox';
                    cb.className = 'tool-cb';
                    cb.setAttribute('data-tool', tool.name);
                    cb.checked = checked[tool.name] === true;
                    if (tool.privileged) {
                        // 特权面——可见不可配：主干会话面显示已选、其余面显示未选，一律禁用（不入名单）
                        cb.disabled = true;
                        cb.checked = (privilegedMode === 'on');
                        label.style.borderColor = 'var(--ch-warn)';
                        label.style.opacity = '0.7';
                        label.title = privilegedMode === 'on'
                            ? '特权面——主干会话永久激活，不受名单约束'
                            : '特权面——仅主干会话可用，不入名单';
                        label.appendChild(cb);
                        label.appendChild(document.createTextNode(tool.name + ' · 特权面'));
                        var ordPriv = toolOrderBadge(tool.order);
                        if (ordPriv) { label.appendChild(ordPriv); }
                        groupBox.appendChild(label);
                        return;
                    }
                    cbs.push(cb);
                    label.appendChild(cb);
                    label.appendChild(document.createTextNode(tool.name));
                    var ordEl = toolOrderBadge(tool.order);
                    if (ordEl) { label.appendChild(ordEl); }
                    groupBox.appendChild(label);
                })(tools[t]);
            }
            box.appendChild(groupBox);
            // 组开关——全选/全不选；组内变化回写半选态
            function syncHead() {
                if (cbs.length === 0) {
                    // 全特权组——组头开关无可用项可切
                    hcb.disabled = true;
                    hcb.checked = false;
                    hcb.indeterminate = false;
                    hcb.title = '该组全部为特权工具——不入名单';
                    return;
                }
                var on = 0;
                for (var i = 0; i < cbs.length; i++) { if (cbs[i].checked) { on = on + 1; } }
                hcb.checked = (on === cbs.length);
                hcb.indeterminate = (on > 0 && on < cbs.length);
            }
            hcb.addEventListener('change', function () {
                for (var i = 0; i < cbs.length; i++) { cbs[i].checked = hcb.checked; }
            });
            for (var i2 = 0; i2 < cbs.length; i2++) {
                cbs[i2].addEventListener('change', syncHead);
            }
            syncHead();
        })(order[g], groups[order[g]]);
    }
}

// 通用勾选收集——data-tool 属性直读（不依赖渲染顺序）；禁用项（特权面锁定项）不计入提交
function collectChecked(boxId) {
    var names = [];
    var boxes = document.querySelectorAll('#' + boxId + ' input[type=checkbox].tool-cb');
    for (var i = 0; i < boxes.length; i++) {
        if (boxes[i].disabled) { continue; }
        if (boxes[i].checked) { names.push(boxes[i].getAttribute('data-tool')); }
    }
    return names;
}

// A107——不可用组 / 失效工具名可见化（不可用组不渲染勾选面，此处只解释"为什么少了"）
function renderToolDefectNote(defects, stale) {
    var msg = document.getElementById('catCfgMsg');
    var parts = [];
    for (var i = 0; i < defects.length; i++) {
        parts.push(defects[i].group + '（' + defects[i].stage + '：' + (defects[i].reason || '') + '）');
    }
    var text = '';
    if (parts.length > 0) {
        text = '⚠ 工具组不可用，其工具未加载: ' + parts.join(' / ');
    }
    if (stale.length > 0) {
        if (text.length > 0) { text = text + ' ｜ '; }
        text = text + '清单含已失效工具名（保存时会被剔除）: ' + stale.join(', ');
    }
    msg.textContent = text;
}

// 每猫工具勾选——默认猫（majordomo）面：特权项已选 + 禁用；其他猫面：未选 + 禁用（后端白名单兜底剔除）
function renderToolChecks(toolNames) {
    renderGroupedChecks('catCfgTools', catCfgAllTools, parseToolChecked(toolNames), catCfgTarget === 'majordomo' ? 'on' : 'off');
}

// 拖拽排序（最简原生实现——容器级事件委托；行设 draggable=true 即可拖；onReorder(from, to) 自行搬移数组并重渲染）
function enableDragSort(boxId, onReorder) {
    var box = document.getElementById(boxId);
    var from = -1;
    function findRow(node) {
        while (node && node.parentNode !== box) { node = node.parentNode; }
        return node ? node : null;
    }
    box.addEventListener('dragstart', function (e) {
        var row = findRow(e.target);
        if (!row) { return; }
        from = Array.prototype.indexOf.call(box.children, row);
        if (e.dataTransfer) { e.dataTransfer.effectAllowed = 'move'; }
    });
    box.addEventListener('dragover', function (e) {
        if (from >= 0) { e.preventDefault(); }
    });
    box.addEventListener('drop', function (e) {
        var row = findRow(e.target);
        if (from < 0 || !row) { return; }
        e.preventDefault();
        var to = Array.prototype.indexOf.call(box.children, row);
        var moveFrom = from;
        from = -1;
        if (to >= 0 && to !== moveFrom) { onReorder(moveFrom, to); }
    });
    box.addEventListener('dragend', function () { from = -1; });
}

function renderInjectList(list) {
    catCfgInjectList = list.slice();
    var box = document.getElementById('catCfgInject');
    box.textContent = '';
    for (var i = 0; i < catCfgInjectList.length; i++) {
        (function (idx) {
            var row = document.createElement('div');
            row.style.cssText = 'display:flex;gap:6px;align-items:center;margin-top:3px';
            row.draggable = true;
            row.title = '拖动调整顺序（保存后生效）';
            var handle = document.createElement('span');
            handle.textContent = '≡';
            handle.style.cssText = 'color:var(--ch-fg-weak);font-size:var(--ch-fs-tag);cursor:move';
            row.appendChild(handle);
            var txt = document.createElement('span');
            txt.textContent = catCfgInjectList[idx];
            txt.style.cssText = 'flex:1;color:var(--ch-ok-weak);font-size:var(--ch-fs-tag);word-break:break-all';
            row.appendChild(txt);
            var rm = document.createElement('button');
            rm.textContent = '移除';
            rm.className = 'btn-mini tight danger';
            rm.onclick = function () {
                catCfgInjectList.splice(idx, 1);
                renderInjectList(catCfgInjectList);
            };
            row.appendChild(rm);
            box.appendChild(row);
        })(i);
    }
}

// 前文 List 拖拽接线（每猫配置面）——拖动即改数组顺序，保存按钮落盘
enableDragSort('catCfgInject', function (from, to) {
    var moved = catCfgInjectList.splice(from, 1)[0];
    catCfgInjectList.splice(to, 0, moved);
    renderInjectList(catCfgInjectList);
});

function saveCatCfg() {
    var toolNames = collectChecked('catCfgTools');
    var payload = {
        cat: catCfgTarget,
        displayName: document.getElementById('catCfgName').value,
        apiConfigId: document.getElementById('catCfgApi').value,
        type: document.getElementById('catCfgType').value,
        persona: document.getElementById('catCfgPersona').value,
        toolNames: toolNames.join(','),
        injectList: catCfgInjectList,
        packs: collectPackChecks(),
        qqbotId: document.getElementById('catCfgQqBot').value,
        qqbotEnable: document.getElementById('catCfgQqEnable').checked,
        userInjectIdleOnly: document.getElementById('catCfgIdleOnly').checked,
        appearanceId: document.getElementById('catCfgAppearance').value,
        reasoningEffort: document.getElementById('catCfgEffort').value,
        enabledRoots: collectRootChecks()
    };
    fetch('/api/v1/cat-config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (d.ok) {
                // A132 / A261 剔除出声——池外工具名与池外根 id 显式告知（不静默；明细同时进宿主日志）
                var warns = [];
                var removedTools = d.removedToolNames;
                if (removedTools && removedTools.length > 0) {
                    warns.push('已剔除池外工具名 ' + removedTools.length + ' 个：' + removedTools.join('、'));
                }
                var removedRoots = d.removedEnabledRoots;
                if (removedRoots && removedRoots.length > 0) {
                    warns.push('已剔除池外根 id ' + removedRoots.length + ' 个：' + removedRoots.join('、'));
                }
                if (d.rootsPoolUnavailable) {
                    warns.push('全局根池不可用——白名单已按空处理（仅常驻根可用）');
                }
                if (warns.length > 0) {
                    document.getElementById('catCfgMsg').textContent = '已保存——⚠️ ' + warns.join('；') + '（请重开配置确认名单）';
                } else {
                    document.getElementById('catCfgMsg').textContent = '已保存——API 即时生效；人设/工具/前文 List/显示名 新会话生效';
                }
            } else {
                document.getElementById('catCfgMsg').textContent = '保存失败: ' + d.error;
            }
        });
}

document.getElementById('catCfgSave').onclick = saveCatCfg;
document.getElementById('catCfgCancel').onclick = function () { catCfgModal.style.display = 'none'; };
document.getElementById('catCfgClose').onclick = function () { catCfgModal.style.display = 'none'; };
document.getElementById('catCfgInjectAddBtn').onclick = function () {
    var input = document.getElementById('catCfgInjectAdd');
    var val = input.value.trim();
    if (val.length === 0) { return; }
    if (!isValidInjectPath(val)) {
        document.getElementById('catCfgMsg').textContent = '路径拒绝——只接受完整路径（绝对路径或 id: 命名空间）: ' + val;
        return;
    }
    catCfgInjectList.push(val);
    input.value = '';
    renderInjectList(catCfgInjectList);
};
// 默认猫配置入口——统一由多猫页 Majordomo 行「配置」按钮承担（openCatCfg 见 panel.js renderCatCard；F2.1 对话页签已移除，页面无独立 chatCfg 元素）

// M4e 猫级白名单——启用根勾选（allRoots 全局池；enabledRoots 当前猫已启用；workspace 强制常驻不可取消）
function renderRootChecks(allRoots, enabledRoots) {
    var box = document.getElementById('catCfgRoots');
    box.textContent = '';
    if (!allRoots || allRoots.length === 0) {
        box.textContent = '（全局根池为空）';
        return;
    }
    // 未配置（空/null）= 空白名单——仅常驻 workspace 可用（如实渲染，不默认勾选）
    // 大小写归一——比对键一律小写（池内 id 已统一小写；历史值容错）
    var enabled = {};
    if (enabledRoots && enabledRoots.length > 0) {
        for (var i = 0; i < enabledRoots.length; i++) {
            if (enabledRoots[i] && enabledRoots[i].length > 0) { enabled[enabledRoots[i].toLowerCase()] = true; }
        }
    }
    var hasEnabled = enabledRoots && enabledRoots.length > 0;
    if (!hasEnabled) {
        var tip = document.createElement('div');
        tip.style.cssText = 'width:100%;font-size:var(--ch-fs-tag);color:var(--ch-err);margin-bottom:4px';
        tip.textContent = '⚠ 未配置根白名单——本猫仅常驻 workspace 可用（保存一次即固化为显式清单）';
        box.appendChild(tip);
    }
    for (var j = 0; j < allRoots.length; j++) {
        (function (root) {
            var rid = (root.id || '').toLowerCase();
            var isFixed = root.fixedRoot === true;
            var isWs = rid === 'workspace' || rid === 'runtime';
            var label = document.createElement('label');
            label.style.cssText = 'display:flex;align-items:center;gap:4px;background:var(--ch-bg-chip);border:1px solid var(--ch-line);border-radius:4px;padding:3px 8px;font-size:var(--ch-fs-tag);color:var(--ch-fg);cursor:pointer';
            var cb = document.createElement('input');
            cb.type = 'checkbox';
            cb.className = 'root-cb';
            cb.setAttribute('data-root', root.id);
            // workspace 强制必选且不可取消；其余按显式清单勾选（未配置 = 不勾——如实反映生效范围）
            cb.checked = isWs || (hasEnabled && enabled[rid] === true);
            if (isWs) {
                cb.checked = true;
                cb.disabled = true;
                label.style.color = 'var(--ch-warn)';
                label.title = '系统根——必选不可取消';
            } else if (isFixed) {
                // 固定命名根（mau / mauout / data）——呈现区分，不加权限
                label.style.borderColor = 'var(--ch-warn)';
                label.title = '系统根（固定命名）——' + (root.note || '');
            }
            label.appendChild(cb);
            var txt = document.createElement('span');
            var nameLabel = root.id;
            if (isWs) { nameLabel = root.id + '（系统根·必选）'; }
            else if (isFixed) { nameLabel = root.id + '（系统根）'; }
            txt.textContent = nameLabel + ' — ' + root.path + (root.writable ? '' : ' [只读]');
            txt.style.wordBreak = 'break-all';
            label.appendChild(txt);
            box.appendChild(label);
        })(allRoots[j]);
    }
}

function collectRootChecks() {
    var ids = [];
    var boxes = document.querySelectorAll('#catCfgRoots input[type=checkbox].root-cb');
    for (var i = 0; i < boxes.length; i++) {
        if (boxes[i].checked) { ids.push(boxes[i].getAttribute('data-root')); }
    }
    return ids;
}

// 新猫默认模板编辑面（M3d 体验轮——cat-default.cfg 全局配置：baseRole + 新猫三字段默认值）
var tplInjectList = [];

// 注入路径前端校验——只接受完整路径：盘符/UNC//斜杠开头 或 id: 命名空间（与后端 IsValidInjectPath 同规）
function isValidInjectPath(p) {
    if (!p || p.length === 0) { return false; }
    if (/^[a-zA-Z]:[\\/]/.test(p) || /^[\\/]{2}/.test(p) || /^\//.test(p)) { return true; }
    var m = /^([A-Za-z0-9_]+):(.+)$/.exec(p);
    return m !== null && m[2].length > 0;
}

function renderTplInject() {
    var box = document.getElementById('tplInject');
    box.textContent = '';
    for (var i = 0; i < tplInjectList.length; i++) {
        (function (idx) {
            var row = document.createElement('div');
            row.style.cssText = 'display:flex;gap:6px;align-items:center;margin-top:3px';
            row.draggable = true;
            row.title = '拖动调整顺序（保存后生效）';
            var handle = document.createElement('span');
            handle.textContent = '≡';
            handle.style.cssText = 'color:var(--ch-fg-weak);font-size:var(--ch-fs-tag);cursor:move';
            row.appendChild(handle);
            var txt = document.createElement('span');
            txt.textContent = tplInjectList[idx];
            txt.style.cssText = 'flex:1;color:var(--ch-ok-weak);font-size:var(--ch-fs-tag);word-break:break-all';
            row.appendChild(txt);
            var rm = document.createElement('button');
            rm.textContent = '移除';
            rm.className = 'btn-mini tight danger';
            rm.onclick = function () {
                tplInjectList.splice(idx, 1);
                renderTplInject();
            };
            row.appendChild(rm);
            box.appendChild(row);
        })(i);
    }
}

// 前文 List 拖拽接线（新猫默认模板面）
enableDragSort('tplInject', function (from, to) {
    var moved = tplInjectList.splice(from, 1)[0];
    tplInjectList.splice(to, 0, moved);
    renderTplInject();
});

function loadTpl() {
    fetch('/api/v1/cat-default')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                document.getElementById('tplMsg').textContent = '读取失败: ' + (d.error || '');
                return;
            }
            document.getElementById('tplBaseRole').value = d.baseRole || '';
            document.getElementById('tplPersona').value = d.defaultPersona || '';
            document.getElementById('tplIdleOnly').checked = !!d.defaultUserInjectIdleOnly;
            catCfgDefectGroups = d.defectGroups || [];
            renderGroupedChecks('tplTools', d.allTools || d.allToolNames || [], parseToolChecked(d.defaultToolNames || ''), 'off');
            tplInjectList = (d.defaultInjectList || []).slice();
            renderTplInject();
            renderTplPacks(d.allPacks || [], d.defaultPacks || []);
        })
        .catch(function () {
            document.getElementById('tplMsg').textContent = '读取失败——宿主未运行？';
        });
}

function saveTpl() {
    var payload = {
        baseRole: document.getElementById('tplBaseRole').value,
        defaultPersona: document.getElementById('tplPersona').value,
        defaultToolNames: collectChecked('tplTools').join(','),
        defaultInjectList: tplInjectList,
        defaultPacks: collectTplPacks(),
        defaultUserInjectIdleOnly: document.getElementById('tplIdleOnly').checked,
    };
    fetch('/api/v1/cat-default', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (d.ok) {
                // A132 剔除出声——被剔池外工具名显式告知（不静默；明细同时进宿主日志）
                var removed = d.removedToolNames;
                if (removed && removed.length > 0) {
                    document.getElementById('tplMsg').textContent = '已保存——⚠️ 已剔除池外工具名 ' + removed.length + ' 个：' + removed.join('、') + '（请重开确认名单）';
                } else {
                    document.getElementById('tplMsg').textContent = '已保存——新猫创建时继承；baseRole 新会话生效';
                }
            } else {
                document.getElementById('tplMsg').textContent = '保存失败: ' + d.error;
            }
        });
}

document.getElementById('tplRefresh').onclick = loadTpl;
document.getElementById('tplSave').onclick = saveTpl;
document.getElementById('tplInjectAddBtn').onclick = function () {
    var input = document.getElementById('tplInjectAdd');
    var val = input.value.trim();
    if (val.length === 0) { return; }
    if (!isValidInjectPath(val)) {
        document.getElementById('tplMsg').textContent = '路径拒绝——只接受完整路径（绝对路径或 id: 命名空间）: ' + val;
        return;
    }
    tplInjectList.push(val);
    input.value = '';
    renderTplInject();
};
loadTpl();

// [段] 受控根编辑面（M3d 体验轮——workspace roots 管理员面；重启生效；LLM 工具面只读）
var rootsList = [];

function renderRoots() {
    var body = document.querySelector('#rootsTable tbody');
    body.textContent = '';
    for (var i = 0; i < rootsList.length; i++) {
        (function (idx) {
            var tr = document.createElement('tr');
            var tdId = document.createElement('td');
            tdId.textContent = rootsList[idx].id + (rootsList[idx].fixedRoot ? ' · 系统根' : '');
            tdId.style.color = 'var(--ch-identity)';
            tr.appendChild(tdId);
            var tdPath = document.createElement('td');
            tdPath.textContent = rootsList[idx].path;
            tdPath.style.wordBreak = 'break-all';
            tr.appendChild(tdPath);
            var tdNote = document.createElement('td');
            tdNote.textContent = rootsList[idx].note || '';
            tdNote.style.color = 'var(--ch-ok-weak)';
            tr.appendChild(tdNote);
            var tdW = document.createElement('td');
            tdW.textContent = rootsList[idx].writable ? '读写' : '只读';
            tdW.style.color = rootsList[idx].writable ? 'var(--ch-ok)' : 'var(--ch-err)';
            tr.appendChild(tdW);
            var tdOp = document.createElement('td');
            var editBtn = document.createElement('button');
            editBtn.textContent = '编辑';
            editBtn.className = 'btn-mini tight info';
            editBtn.onclick = function () {
                enterEditRoot(idx);
            };
            tdOp.appendChild(editBtn);
            var rm = document.createElement('button');
            rm.textContent = '移除';
            rm.className = 'btn-mini tight danger';
            if (rootsList[idx].fixedRoot) {
                // 系统根——三列固定且不可移除（后端同校验兜底）
                rm.disabled = true;
                rm.title = '系统根——不可移除';
            } else {
                rm.onclick = function () {
                    // 移除确认——保存并重启后生效；未保存前可刷新页面恢复
                    if (!window.confirm('移除受控根「' + rootsList[idx].id + '」？（保存并重启宿主后生效）')) { return; }
                    rootsList.splice(idx, 1);
                    renderRoots();
                };
            }
            tdOp.appendChild(rm);
            tr.appendChild(tdOp);
            body.appendChild(tr);
        })(i);
    }
}

// 行内编辑——把指定行切换为输入框（id/path/writable 可改；确定/取消）
function enterEditRoot(idx) {
    var body = document.querySelector('#rootsTable tbody');
    body.textContent = '';
    for (var i = 0; i < rootsList.length; i++) {
        (function (rowIdx) {
            var tr = document.createElement('tr');
            if (rowIdx === idx) {
                var isFixedRow = rootsList[rowIdx].fixedRoot === true;
                var tdId = document.createElement('td');
                var idIn = document.createElement('input');
                idIn.value = rootsList[rowIdx].id;
                idIn.className = 'input-mini';
                idIn.style.width = '90%';
                if (isFixedRow) {
                    // 系统根——id / 注释 / 读写标志固定，只放开路径
                    idIn.disabled = true;
                    idIn.title = '系统根——id 不可修改';
                }
                tdId.appendChild(idIn);
                tr.appendChild(tdId);
                var tdPath = document.createElement('td');
                var pathIn = document.createElement('input');
                pathIn.value = rootsList[rowIdx].path;
                pathIn.className = 'input-mini';
                pathIn.style.width = '96%';
                tdPath.appendChild(pathIn);
                tr.appendChild(tdPath);
                var tdNote = document.createElement('td');
                var noteIn = document.createElement('input');
                noteIn.value = rootsList[rowIdx].note || '';
                noteIn.maxLength = 20;
                noteIn.placeholder = '≤20 字';
                noteIn.className = 'input-mini';
                noteIn.style.width = '96%';
                if (isFixedRow) {
                    noteIn.disabled = true;
                    noteIn.title = '系统根——注释固定';
                }
                tdNote.appendChild(noteIn);
                tr.appendChild(tdNote);
                var tdW = document.createElement('td');
                var wSel = document.createElement('select');
                var optW = document.createElement('option');
                optW.value = 'true';
                optW.textContent = '读写';
                var optR = document.createElement('option');
                optR.value = 'false';
                optR.textContent = '只读';
                wSel.appendChild(optW);
                wSel.appendChild(optR);
                wSel.value = rootsList[rowIdx].writable ? 'true' : 'false';
                wSel.className = 'input-mini';
                if (isFixedRow) {
                    wSel.disabled = true;
                    wSel.title = '系统根——读写标志固定';
                }
                tdW.appendChild(wSel);
                tr.appendChild(tdW);
                var tdOp = document.createElement('td');
                var okBtn = document.createElement('button');
                okBtn.textContent = '确定';
                okBtn.className = 'btn-mini primary tight';
                okBtn.onclick = function () {
                    var nid = idIn.value.trim().toLowerCase();
                    var npath = pathIn.value.trim();
                    var nw = wSel.value === 'true';
                    if (nid.length === 0 || npath.length === 0) {
                        document.getElementById('rootsMsg').textContent = 'id 和路径不能为空';
                        return;
                    }
                    if (!/^[a-z0-9]+$/.test(nid)) {
                        document.getElementById('rootsMsg').textContent = 'id 非法——仅字母/数字（自动转小写）';
                        return;
                    }
                    for (var k = 0; k < rootsList.length; k++) {
                        if (k !== rowIdx && (rootsList[k].id || '').toLowerCase() === nid) {
                            document.getElementById('rootsMsg').textContent = 'id 重复: ' + nid;
                            return;
                        }
                    }
                    var nnote = noteIn.value.trim();
                    if (nnote.length > 20) {
                        document.getElementById('rootsMsg').textContent = '注释超长——最多 20 字';
                        return;
                    }
                    if (isFixedRow) {
                        // 系统根——只放开路径（id/注释/读写由系统固定，后端同校验兜底）
                        rootsList[rowIdx].path = npath;
                    } else {
                        rootsList[rowIdx] = { id: nid, path: npath, writable: nw, note: nnote, fixedRoot: false };
                    }
                    renderRoots();
                };
                tdOp.appendChild(okBtn);
                var cancelBtn = document.createElement('button');
                cancelBtn.textContent = '取消';
                cancelBtn.className = 'btn-mini tight';
                cancelBtn.onclick = function () {
                    renderRoots();
                };
                tdOp.appendChild(cancelBtn);
                tr.appendChild(tdOp);
            } else {
                var tdId2 = document.createElement('td');
                tdId2.textContent = rootsList[rowIdx].id;
                tdId2.style.color = 'var(--ch-identity)';
                tr.appendChild(tdId2);
                var tdPath2 = document.createElement('td');
                tdPath2.textContent = rootsList[rowIdx].path;
                tdPath2.style.wordBreak = 'break-all';
                tr.appendChild(tdPath2);
                var tdNote2 = document.createElement('td');
                tdNote2.textContent = rootsList[rowIdx].note || '';
                tdNote2.style.color = 'var(--ch-ok-weak)';
                tr.appendChild(tdNote2);
                var tdW2 = document.createElement('td');
                tdW2.textContent = rootsList[rowIdx].writable ? '读写' : '只读';
                tdW2.style.color = rootsList[rowIdx].writable ? 'var(--ch-ok)' : 'var(--ch-err)';
                tr.appendChild(tdW2);
                var tdOp2 = document.createElement('td');
                tdOp2.textContent = '';
                tr.appendChild(tdOp2);
            }
            body.appendChild(tr);
        })(i);
    }
}

function loadRoots() {
    fetch('/api/v1/workspace')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                document.getElementById('rootsMsg').textContent = '读取失败: ' + (d.error || '');
                return;
            }
            rootsList = (d.roots || []).slice();
            renderRoots();
        })
        .catch(function () {
            document.getElementById('rootsMsg').textContent = '读取失败——宿主未运行？';
        });
}

function saveRoots() {
    var payload = { roots: rootsList };
    fetch('/api/v1/workspace', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            document.getElementById('rootsMsg').textContent = d.ok ? '已保存——重启宿主生效' : '保存失败: ' + (d.error || '');
        });
}

document.getElementById('rootAddBtn').onclick = function () {
    var id = document.getElementById('rootNewId').value.trim().toLowerCase();
    var path = document.getElementById('rootNewPath').value.trim();
    var writable = document.getElementById('rootNewWritable').value === 'true';
    if (id.length === 0 || path.length === 0) {
        document.getElementById('rootsMsg').textContent = 'id 和路径不能为空';
        return;
    }
    if (!/^[a-z0-9]+$/.test(id)) {
        document.getElementById('rootsMsg').textContent = 'id 非法——仅字母/数字（自动转小写）';
        return;
    }
    // PS 保留驱动器名镜像——权威在后端 AdminService.IsReservedPsDriveName（此处只为即时反馈）
    var PS_RESERVED = ['alias', 'cert', 'env', 'function', 'hkcu', 'hklm', 'variable', 'wsman', 'temp'];
    if (/^[a-z]$/.test(id) || PS_RESERVED.indexOf(id) >= 0) {
        document.getElementById('rootsMsg').textContent = 'id 与 PowerShell 内置驱动器同名（ps 根寻址会失效）: ' + id;
        return;
    }
    for (var i = 0; i < rootsList.length; i++) {
        if ((rootsList[i].id || '').toLowerCase() === id) {
            document.getElementById('rootsMsg').textContent = 'id 重复: ' + id;
            return;
        }
    }
    var note = document.getElementById('rootNewNote').value.trim();
    if (note.length > 20) {
        document.getElementById('rootsMsg').textContent = '注释超长——最多 20 字';
        return;
    }
    rootsList.push({ id: id, path: path, writable: writable, note: note, fixedRoot: false });
    document.getElementById('rootNewId').value = '';
    document.getElementById('rootNewPath').value = '';
    document.getElementById('rootNewNote').value = '';
    renderRoots();
};
document.getElementById('rootsSave').onclick = saveRoots;
loadRoots();

// [R4 加载包] 猫级挂载勾选——池全量 allPacks；packs 当前已挂载；pack 工具按此授权
function renderPackChecks(allPacks, packs) {
    var box = document.getElementById('catCfgPacks');
    box.textContent = '';
    if (!allPacks || allPacks.length === 0) {
        var empty = document.createElement('span');
        empty.style.cssText = 'font-size:var(--ch-fs-tag);color:var(--ch-fg-weak)';
        empty.textContent = '（池为空——在配置页「加载包池」新建）';
        box.appendChild(empty);
        return;
    }
    var mounted = {};
    if (packs && packs.length > 0) {
        for (var i = 0; i < packs.length; i++) { mounted[packs[i]] = true; }
    }
    for (var j = 0; j < allPacks.length; j++) {
        (function (pack) {
            var label = document.createElement('label');
            label.style.cssText = 'display:flex;align-items:center;gap:4px;background:var(--ch-bg-chip);border:1px solid var(--ch-line);border-radius:4px;padding:3px 8px;font-size:var(--ch-fs-tag);color:var(--ch-fg);cursor:pointer';
            var cb = document.createElement('input');
            cb.type = 'checkbox';
            cb.className = 'pack-cb';
            cb.setAttribute('data-pack', pack.key);
            cb.checked = mounted[pack.key] === true;
            label.appendChild(cb);
            var txt = document.createElement('span');
            var desc = pack.desc && pack.desc.length > 0 ? '（' + pack.desc + '）' : '';
            txt.textContent = pack.key + desc;
            if (pack.pathCount) { txt.title = pack.pathCount + ' 个路径项'; }
            label.appendChild(txt);
            box.appendChild(label);
        })(allPacks[j]);
    }
}

function collectPackChecks() {
    var keys = [];
    var boxes = document.querySelectorAll('#catCfgPacks input[type=checkbox].pack-cb');
    for (var i = 0; i < boxes.length; i++) {
        if (boxes[i].checked) { keys.push(boxes[i].getAttribute('data-pack')); }
    }
    return keys;
}

// 包池管理入口在配置页「加载包池」区（本弹层只做猫级挂载勾选——池 CRUD 见 panel-packs.js）

// [R4 加载包] 新猫默认模板——默认挂载包勾选（池全量 allPacks；defaultPacks 当前默认值；新猫创建时继承）
function renderTplPacks(allPacks, defaultPacks) {
    var box = document.getElementById('tplPacks');
    box.textContent = '';
    if (!allPacks || allPacks.length === 0) {
        var empty = document.createElement('span');
        empty.style.cssText = 'font-size:var(--ch-fs-tag);color:var(--ch-fg-weak)';
        empty.textContent = '（池为空——在「加载包池」区新建）';
        box.appendChild(empty);
        return;
    }
    var mounted = {};
    if (defaultPacks && defaultPacks.length > 0) {
        for (var i = 0; i < defaultPacks.length; i++) { mounted[defaultPacks[i]] = true; }
    }
    for (var j = 0; j < allPacks.length; j++) {
        (function (pack) {
            var label = document.createElement('label');
            label.style.cssText = 'display:flex;align-items:center;gap:4px;background:var(--ch-bg-chip);border:1px solid var(--ch-line);border-radius:4px;padding:3px 8px;font-size:var(--ch-fs-tag);color:var(--ch-fg);cursor:pointer';
            var cb = document.createElement('input');
            cb.type = 'checkbox';
            cb.className = 'tpl-pack-cb';
            cb.setAttribute('data-pack', pack.key);
            cb.checked = mounted[pack.key] === true;
            label.appendChild(cb);
            var txt = document.createElement('span');
            var desc = pack.desc && pack.desc.length > 0 ? '（' + pack.desc + '）' : '';
            txt.textContent = pack.key + desc;
            label.appendChild(txt);
            box.appendChild(label);
        })(allPacks[j]);
    }
}

function collectTplPacks() {
    var keys = [];
    var boxes = document.querySelectorAll('#tplPacks input[type=checkbox].tpl-pack-cb');
    for (var i = 0; i < boxes.length; i++) {
        if (boxes[i].checked) { keys.push(boxes[i].getAttribute('data-pack')); }
    }
    return keys;
}
