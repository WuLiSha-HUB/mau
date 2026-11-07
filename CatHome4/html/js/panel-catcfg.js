// CH4 外观层——panel-catcfg.js：每猫配置弹层 + 新猫默认模板 + 受控根编辑面（M3c/M3d/M4e）——panel.js 拆分（2026-09-08 体量治理）
// 依赖：app.js + panel.js 先加载（全局状态已就位；renderCatCard 点击时调用本文件 openCatCfg）；本文件承载 cat.cfg 配置弹层与 workspace 管理
// 段15（M3c 每猫配置）+ 段[受控根编辑面]（M3d）原样迁出，逻辑零改动

// [段15] M3c 每猫配置弹层——API 下拉/人设/工具勾选/前文 List
var catCfgModal = document.getElementById('catCfgModal');
var catCfgTarget = '';
var catCfgName = '';
var catCfgAllTools = [];
var catCfgInjectList = [];
var catCfgAllPacks = [];
// A107——工具组定义缺陷（后端 defectGroups——不可用组不进勾选面 + 弹层提示）
var catCfgDefectGroups = [];
// A275——库权限池可取性（false = 清单不可取，保存时不提交 dbLibs，避免静默清空）
var catCfgDbLibsAvailable = false;

function openCatCfg(catId, name) {
    catCfgTarget = catId;
    catCfgName = name || catId;
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
            renderAppearanceOptions(d.appearanceOptions || [], d.appearanceId || '');
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
            renderRootChecks('catCfgRoots', d.allRoots || [], d.enabledRoots || []);
            // A275 库权限——库池 = 注册表登记的库（不可取时如实告警，不当成「无库」）
            catCfgDbLibsAvailable = d.dbLibsAvailable === true;
            renderDbLibChecks(d.allLibs || [], d.dbLibs || [], catCfgDbLibsAvailable);
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
    // 特权面四态（2026-09-29；2026-10-01 增 'hide'）——'on'=默认猫面（已选 + 禁用）；'off'=其他猫面（未选 + 禁用）；'hide'=新猫模板面（整组不渲染）
    if (privilegedMode !== 'on' && privilegedMode !== 'hide') { privilegedMode = 'off'; }
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
        if (isPriv && privilegedMode === 'hide') { continue; }   // 模板面——特权工具不入组（整组皆特权即整组不出，组头一并隐藏）
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
        userInjectIdleOnly: document.getElementById('catCfgIdleOnly').checked,
        appearanceId: document.getElementById('catCfgAppearance').value,
        reasoningEffort: document.getElementById('catCfgEffort').value,
        enabledRoots: collectRootChecks('catCfgRoots')
    };
    // A275——库清单不可取时不提交 dbLibs（字段级合并：缺省保留磁盘现值，避免静默清空）
    if (catCfgDbLibsAvailable) {
        payload.dbLibs = collectDbLibChecks();
    }
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
// 前文注入入口——手动路径输入已撤（2026-10-01 易用性轮）：候选取自「新猫默认模板」的默认前文池
// 本面只保留清单展示 + 拖拽排序；增删走弹层（openCatInject——与多猫行「前文注入」按钮同一面）
document.getElementById('catCfgInjectPick').onclick = function () {
    openCatInject(catCfgTarget, catCfgName, function (list) { renderInjectList(list); });
};
// 默认猫配置入口——统一由多猫页 Majordomo 行「配置」按钮承担（openCatCfg 见 panel.js renderCatCard；F2.1 对话页签已移除，页面无独立 chatCfg 元素）

// M4e 猫级白名单——启用根勾选（allRoots 全局池；enabledRoots 当前猫已启用；workspace 强制常驻不可取消）
// tipText：未配置提示文案（猫配置面 / 新猫模板面各自措辞；缺省=猫配置面口径）
function renderRootChecks(boxId, allRoots, enabledRoots, tipText) {
    var box = document.getElementById(boxId);
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
        tip.textContent = tipText || '⚠ 未配置根白名单——本猫仅常驻 workspace 可用（保存一次即固化为显式清单）';
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

function collectRootChecks(boxId) {
    var ids = [];
    var boxes = document.querySelectorAll('#' + boxId + ' input[type=checkbox].root-cb');
    for (var i = 0; i < boxes.length; i++) {
        if (boxes[i].checked) { ids.push(boxes[i].getAttribute('data-root')); }
    }
    return ids;
}

// A275 库权限勾选面——dbLibs（本猫可用库白名单；池 = 注册表登记的库）
// available=false：数据库服务未绑定 / 宿主版本过旧——如实告警，不把「不可取」渲染成「无库」
function renderDbLibChecks(allLibs, dbLibs, available) {
    var box = document.getElementById('catCfgDbLibs');
    box.textContent = '';
    if (!available) {
        var tip = document.createElement('div');
        tip.style.cssText = 'width:100%;font-size:var(--ch-fs-tag);color:var(--ch-err)';
        tip.textContent = '⚠ 库清单不可取——数据库服务未绑定或宿主版本过旧；保存不改动 dbLibs';
        box.appendChild(tip);
        return;
    }
    var checked = {};
    var configured = [];
    if (dbLibs && dbLibs.length > 0) {
        for (var i = 0; i < dbLibs.length; i = i + 1) {
            if (dbLibs[i] && dbLibs[i].length > 0) {
                checked[dbLibs[i]] = true;
                configured.push(dbLibs[i]);
            }
        }
    }
    var pool = allLibs || [];
    if (pool.length === 0 && configured.length === 0) {
        box.textContent = '（尚无库——控制台「数据」页签可新建）';
        return;
    }
    var inPool = {};
    for (var j = 0; j < pool.length; j = j + 1) {
        var id = pool[j].id || '';
        if (id.length === 0) { continue; }
        inPool[id] = true;
        box.appendChild(makeDbLibChip(id, pool[j].entries, pool[j].broken === true, checked[id] === true, false));
    }
    // 已配置但池内不存在——如实列出（不静默丢弃；取消勾选即清）
    for (var k = 0; k < configured.length; k = k + 1) {
        if (inPool[configured[k]] === true) { continue; }
        box.appendChild(makeDbLibChip(configured[k], -1, false, true, true));
    }
}

function makeDbLibChip(id, entries, broken, isChecked, stale) {
    var label = document.createElement('label');
    label.style.cssText = 'display:flex;align-items:center;gap:4px;background:var(--ch-bg-chip);border:1px solid var(--ch-line);border-radius:4px;padding:3px 8px;font-size:var(--ch-fs-tag);color:var(--ch-fg);cursor:pointer';
    var cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.className = 'db-lib-cb';
    cb.setAttribute('data-lib', id);
    cb.checked = isChecked;
    label.appendChild(cb);
    var txt = document.createElement('span');
    var text = id;
    if (entries >= 0) { text = text + ' · ' + entries + ' 条'; }
    if (stale) {
        text = text + '（未登记）';
        label.style.color = 'var(--ch-warn)';
        label.title = '注册表无此库——勾选保留，取消勾选即清';
    }
    if (broken) {
        text = text + ' ⚠ 坏库';
        label.style.color = 'var(--ch-err)';
    }
    txt.textContent = text;
    label.appendChild(txt);
    return label;
}

function collectDbLibChecks() {
    var ids = [];
    var boxes = document.querySelectorAll('#catCfgDbLibs input[type=checkbox].db-lib-cb');
    for (var i = 0; i < boxes.length; i = i + 1) {
        if (boxes[i].checked) { ids.push(boxes[i].getAttribute('data-lib')); }
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

// 新猫默认模板——默认前文 List（条目行内编辑：未改动 / 清空 = 不改动，改完由「保存模板」落盘）
function renderTplInject() {
    var box = document.getElementById('tplInject');
    box.textContent = '';
    for (var i = 0; i < tplInjectList.length; i++) {
        (function (idx) {
            var row = document.createElement('div');
            row.style.cssText = 'display:flex;gap:6px;align-items:center;margin-top:3px';
            row.title = '拖动 ≡ 调整顺序（保存后生效）';
            var handle = document.createElement('span');
            handle.textContent = '≡';
            handle.style.cssText = 'color:var(--ch-fg-weak);font-size:var(--ch-fs-tag);cursor:move';
            // 拖拽柄独占 draggable——行级 draggable 会吃掉输入框内的拖选
            handle.draggable = true;
            row.appendChild(handle);
            var inp = document.createElement('input');
            inp.className = 'input-mini';
            inp.style.cssText = 'flex:1;min-width:0;color:var(--ch-ok-weak)';
            inp.value = tplInjectList[idx];
            inp.placeholder = '完整路径（如 ccbp:L1/Tree.md）';
            wireInlineEdit(inp, tplInjectList[idx], function (v) {
                if (!isValidInjectPath(v)) {
                    document.getElementById('tplMsg').textContent = '路径拒绝——只接受完整路径（绝对路径或 id: 命名空间）: ' + v;
                    inp.value = tplInjectList[idx];
                    return;
                }
                tplInjectList[idx] = v;
                renderTplInject();
            });
            row.appendChild(inp);
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
            // 模板面——特权组（ConfigCat / MajordomoCat）整组隐藏：新猫模板不该配特权工具，见亦不可配（2026-10-01 体验轮）
            renderGroupedChecks('tplTools', d.allTools || d.allToolNames || [], parseToolChecked(d.defaultToolNames || ''), 'hide');
            tplInjectList = (d.defaultInjectList || []).slice();
            renderTplInject();
            renderTplPacks(d.allPacks || [], d.defaultPacks || []);
            renderRootChecks('tplRoots', d.allRoots || [], d.defaultEnabledRoots || [], '⚠ 未配置——新猫仅常驻 workspace 可用（勾选后新猫创建时继承）');
        })
        .catch(function () {
            document.getElementById('tplMsg').textContent = '读取失败——宿主未运行？';
        });
}

// 模板表单取值——保存与套用共用同一出口（单点真相源）
function tplFormValues() {
    return {
        baseRole: document.getElementById('tplBaseRole').value,
        defaultPersona: document.getElementById('tplPersona').value,
        defaultToolNames: collectChecked('tplTools').join(','),
        defaultInjectList: tplInjectList.slice(),
        defaultPacks: collectTplPacks(),
        defaultEnabledRoots: collectRootChecks('tplRoots'),
        defaultUserInjectIdleOnly: document.getElementById('tplIdleOnly').checked,
    };
}

function saveTpl() {
    var payload = tplFormValues();
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
                openTplApply();   // 顺带询问：是否把模板参数套用到已有猫
            } else {
                document.getElementById('tplMsg').textContent = '保存失败: ' + d.error;
            }
        })
        .catch(function (e) { document.getElementById('tplMsg').textContent = '请求失败: ' + e; });
}

// ── 模板套用面——保存后询问是否更新已有猫配置（与模板一致者自动勾选，其余自行勾选） ──
var tplApplyCats = [];   // [{id, name, cfg}]——比对与套用共用同一次读取

// 逗号清单归一——去空白 + 排序（工具名顺序无语义）
function splitCsvNames(s) {
    var out = [];
    var parts = String(s || '').split(',');
    for (var i = 0; i < parts.length; i++) {
        var v = parts[i].trim();
        if (v.length > 0) { out.push(v); }
    }
    out.sort();
    return out;
}

// 集合比对（入参须已排序）
function sameSet(a, b) {
    if (a.length !== b.length) { return false; }
    for (var i = 0; i < a.length; i++) {
        if (a[i] !== b[i]) { return false; }
    }
    return true;
}

// 序列比对（前文 List 注入顺序有语义）
function sameSeq(a, b) {
    if (a.length !== b.length) { return false; }
    for (var i = 0; i < a.length; i++) {
        if (a[i] !== b[i]) { return false; }
    }
    return true;
}

// 启用根归一——空/null 视同「仅常驻根」（与后端 ValidateEnabledRoots 物化语义同源）
function normalizeRoots(list) {
    var out = [];
    var src = list || [];
    for (var i = 0; i < src.length; i++) {
        if (src[i] && src[i].length > 0) { out.push(String(src[i]).toLowerCase()); }
    }
    if (out.length === 0) { out.push('workspace'); }
    out.sort();
    return out;
}

// 猫配置 vs 当前模板表单——五项全同才算「一模一样」（baseRole 非猫级字段，不参与）
function isCatMatchingTpl(cfg) {
    var tpl = tplFormValues();
    if ((cfg.persona || '') !== tpl.defaultPersona) { return false; }
    if (!sameSet(splitCsvNames(cfg.toolNames), splitCsvNames(tpl.defaultToolNames))) { return false; }
    if (!sameSet((cfg.packs || []).slice().sort(), tpl.defaultPacks.slice().sort())) { return false; }
    if (!sameSeq(cfg.injectList || [], tpl.defaultInjectList)) { return false; }
    if (!sameSet(normalizeRoots(cfg.enabledRoots), normalizeRoots(tpl.defaultEnabledRoots))) { return false; }
    return true;
}

function openTplApply() {
    document.getElementById('tplApplyList').textContent = '读取猫列表…';
    document.getElementById('tplApplyMsg').textContent = '';
    document.getElementById('tplApplyModal').style.display = 'flex';
    fetch('/api/v1/cats')
        .then(function (r) { return r.json(); })
        .then(function (d) { return loadTplApplyCats(d.cats || []); })
        .then(function () { renderTplApplyList(); })
        .catch(function (e) { document.getElementById('tplApplyList').textContent = '读取失败: ' + e; });
}

// 逐猫读配置（串行）——比对要现况；套用要保留字段（enabledRoots）
function loadTplApplyCats(cats) {
    tplApplyCats = [];
    var idx = 0;
    function next() {
        if (idx >= cats.length) { return Promise.resolve(); }
        var c = cats[idx];
        idx = idx + 1;
        return fetch('/api/v1/cat-config?cat=' + encodeURIComponent(c.id))
            .then(function (r) { return r.json(); })
            .then(function (cfg) {
                tplApplyCats.push({ id: c.id, name: c.name, cfg: (cfg && cfg.ok) ? cfg : null });
                return next();
            })
            .catch(function () {
                tplApplyCats.push({ id: c.id, name: c.name, cfg: null });
                return next();
            });
    }
    return next();
}

function renderTplApplyList() {
    var box = document.getElementById('tplApplyList');
    box.textContent = '';
    if (tplApplyCats.length === 0) {
        box.textContent = '暂无猫';
        return;
    }
    for (var i = 0; i < tplApplyCats.length; i++) {
        var item = tplApplyCats[i];
        var matched = item.cfg !== null && isCatMatchingTpl(item.cfg);
        var row = document.createElement('label');
        row.style.cssText = 'display:flex;gap:6px;align-items:center;font-size:var(--ch-fs-tag);padding:3px 0;cursor:pointer';
        var cb = document.createElement('input');
        cb.type = 'checkbox';
        cb.className = 'tpl-apply-cb';
        cb.setAttribute('data-cat', item.id);
        cb.checked = matched;
        // 配置读不到 → 无法安全套用（缺字段会丢 enabledRoots）——禁用勾选
        cb.disabled = item.cfg === null;
        row.appendChild(cb);
        var txt = document.createElement('span');
        txt.textContent = item.name + '（' + item.id + '）'
            + (item.cfg === null ? ' · 配置读取失败（不可套用）' : (matched ? ' · 与模板一致' : ''));
        txt.style.color = item.cfg === null ? 'var(--ch-err)' : (matched ? 'var(--ch-ok-weak)' : 'var(--ch-fg-muted)');
        row.appendChild(txt);
        box.appendChild(row);
    }
}

function closeTplApply() {
    document.getElementById('tplApplyModal').style.display = 'none';
}

// 一键套用——逐猫串行：模板五字段覆盖（含目录白名单）
// （后端 cat-config 2026-10-01 起为字段级合并写——缺省即保留；API 配置与目录白名单以外的字段不动）
function applyTplToCats() {
    var boxes = document.querySelectorAll('#tplApplyList input.tpl-apply-cb');
    var targets = [];
    for (var i = 0; i < boxes.length; i++) {
        if (boxes[i].checked && !boxes[i].disabled) { targets.push(boxes[i].getAttribute('data-cat')); }
    }
    var msg = document.getElementById('tplApplyMsg');
    if (targets.length === 0) {
        msg.textContent = '未勾选任何猫';
        return;
    }
    var tpl = tplFormValues();
    var idx = 0;
    var okCount = 0;
    var fail = [];
    function findItem(id) {
        for (var k = 0; k < tplApplyCats.length; k++) {
            if (tplApplyCats[k].id === id) { return tplApplyCats[k]; }
        }
        return null;
    }
    function next() {
        if (idx >= targets.length) {
            msg.textContent = '套用完成——成功 ' + okCount + ' 只' + (fail.length > 0 ? '；失败: ' + fail.join('、') : '');
            return;
        }
        var id = targets[idx];
        idx = idx + 1;
        var item = findItem(id);
        if (item === null || item.cfg === null) {
            fail.push(id);
            return next();
        }
        var payload = {
            cat: id,
            persona: tpl.defaultPersona,
            toolNames: tpl.defaultToolNames,
            packs: tpl.defaultPacks,
            injectList: tpl.defaultInjectList,
            enabledRoots: tpl.defaultEnabledRoots
        };
        msg.textContent = '套用中… ' + idx + '/' + targets.length;
        fetch('/api/v1/cat-config', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        })
            .then(function (r) { return r.json(); })
            .then(function (d) {
                if (d.ok) { okCount = okCount + 1; }
                else { fail.push(id + '(' + d.error + ')'); }
                return next();
            })
            .catch(function () {
                fail.push(id);
                return next();
            });
    }
    next();
}

document.getElementById('tplApplyGo').onclick = applyTplToCats;
document.getElementById('tplApplyCancel').onclick = closeTplApply;
document.getElementById('tplApplyClose').onclick = closeTplApply;

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
            var lockedRootId = (rootsList[idx].id || '').toLowerCase();
            if (lockedRootId === 'db' || lockedRootId === 'data') {
                // 路径锁定根（A273）——data = 应用数据根 / db = 数据根/Data/db：路径由系统计算，后端同校验拒绝
                editBtn.disabled = true;
                if (lockedRootId === 'db') {
                    editBtn.title = '数据库根固定为 数据根/Data/db——不可修改';
                } else {
                    editBtn.title = '数据根固定为应用数据目录——不可修改';
                }
            } else {
                editBtn.onclick = function () {
                    enterEditRoot(idx);
                };
            }
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
                var isLockedRow = isFixedRow && ((rootsList[rowIdx].id || '').toLowerCase() === 'db' || (rootsList[rowIdx].id || '').toLowerCase() === 'data');
                if (isLockedRow) {
                    // 路径锁定根（data / db）——只读展示，确定时忽略路径改动
                    pathIn.disabled = true;
                    pathIn.title = '路径由系统固定——不可修改';
                }
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
                    if (isLockedRow) {
                        // 路径锁定根——以原值落回（防程序性改动绕过只读）
                        npath = rootsList[rowIdx].path;
                    }
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
// ── 根池「应用给猫」——把当前根池全域写进各猫 enabledRoots（一致者标「一样」并跳过，不重复写） ──
var rootsApplyTargets = [];   // 目标根 id 全集（取磁盘态，不取表格里未保存的编辑）
var rootsApplyCats = [];      // [{id, name, cfg}]

// 猫现值 vs 目标全集——缺 / 多 分开报，两者皆空才算「一样」
function rootsApplyDiff(cfg) {
    var cur = normalizeRoots(cfg.enabledRoots);
    var missing = [];
    var extra = [];
    for (var i = 0; i < rootsApplyTargets.length; i++) {
        if (cur.indexOf(rootsApplyTargets[i]) < 0) { missing.push(rootsApplyTargets[i]); }
    }
    for (var j = 0; j < cur.length; j++) {
        if (rootsApplyTargets.indexOf(cur[j]) < 0) { extra.push(cur[j]); }
    }
    return { same: missing.length === 0 && extra.length === 0, missing: missing, extra: extra };
}

function openRootsApply() {
    document.getElementById('rootsApplyList').textContent = '读取猫列表…';
    document.getElementById('rootsApplyMsg').textContent = '';
    document.getElementById('rootsApplyDesc').textContent = '读取中…';
    document.getElementById('rootsApplyModal').style.display = 'flex';
    fetch('/api/v1/workspace')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) { throw new Error(d.error || '根池读取失败'); }
            var roots = d.roots || [];
            var ids = [];
            for (var i = 0; i < roots.length; i++) {
                var id = String(roots[i].id || '').toLowerCase();
                if (id.length > 0 && ids.indexOf(id) < 0) { ids.push(id); }
            }
            ids.sort();
            rootsApplyTargets = ids;
            return fetch('/api/v1/cats');
        })
        .then(function (r) { return r.json(); })
        .then(function (d) { return loadRootsApplyCats(d.cats || []); })
        .then(function () { renderRootsApplyList(); })
        .catch(function (e) { document.getElementById('rootsApplyList').textContent = '读取失败: ' + e; });
}

// 逐猫读配置（串行）——比对要现况
function loadRootsApplyCats(cats) {
    rootsApplyCats = [];
    var idx = 0;
    function next() {
        if (idx >= cats.length) { return Promise.resolve(); }
        var c = cats[idx];
        idx = idx + 1;
        return fetch('/api/v1/cat-config?cat=' + encodeURIComponent(c.id))
            .then(function (r) { return r.json(); })
            .then(function (cfg) {
                rootsApplyCats.push({ id: c.id, name: c.name, cfg: (cfg && cfg.ok) ? cfg : null });
                return next();
            })
            .catch(function () {
                rootsApplyCats.push({ id: c.id, name: c.name, cfg: null });
                return next();
            });
    }
    return next();
}

function renderRootsApplyList() {
    var box = document.getElementById('rootsApplyList');
    box.textContent = '';
    document.getElementById('rootsApplyDesc').textContent =
        '根池全域（' + rootsApplyTargets.join(' / ') + '）将写入所勾选的猫；已一致者标「一样」并跳过，不重复写。写入后「新会话」生效。';
    if (rootsApplyCats.length === 0) { box.textContent = '暂无猫'; return; }
    var sameCount = 0;
    for (var i = 0; i < rootsApplyCats.length; i++) {
        var item = rootsApplyCats[i];
        var diff = item.cfg !== null ? rootsApplyDiff(item.cfg) : null;
        var row = document.createElement('label');
        row.style.cssText = 'display:flex;gap:6px;align-items:center;font-size:var(--ch-fs-tag);padding:3px 0;cursor:pointer';
        var cb = document.createElement('input');
        cb.type = 'checkbox';
        cb.className = 'roots-apply-cb';
        cb.setAttribute('data-cat', item.id);
        cb.checked = item.cfg !== null && !diff.same;
        // 读不到配置 → 无法安全写；已一致 → 无需写（两者都禁用勾选）
        cb.disabled = item.cfg === null || (diff !== null && diff.same);
        row.appendChild(cb);
        var label = item.name + '（' + item.id + '）';
        if (item.cfg === null) { label = label + ' · 配置读取失败（不可套用）'; }
        else if (diff.same) { sameCount = sameCount + 1; label = label + ' · 一样'; }
        else {
            var parts = [];
            if (diff.missing.length > 0) { parts.push('缺 ' + diff.missing.join('/')); }
            if (diff.extra.length > 0) { parts.push('多 ' + diff.extra.join('/')); }
            label = label + ' · ' + parts.join('，');
        }
        var txt = document.createElement('span');
        txt.textContent = label;
        txt.style.color = item.cfg === null ? 'var(--ch-err)' : (diff !== null && diff.same ? 'var(--ch-ok-weak)' : 'var(--ch-fg-muted)');
        row.appendChild(txt);
        box.appendChild(row);
    }
    if (sameCount === rootsApplyCats.length) {
        document.getElementById('rootsApplyMsg').textContent = '全部猫已与根池一致（一样）——无需写入';
    }
}

function closeRootsApply() {
    document.getElementById('rootsApplyModal').style.display = 'none';
}

// 逐猫串行写入——只写勾选的猫；载荷只带 cat + enabledRoots（字段级合并写，其余字段保留）
function applyRootsToCats() {
    var boxes = document.querySelectorAll('#rootsApplyList input.roots-apply-cb');
    var targets = [];
    for (var i = 0; i < boxes.length; i++) {
        if (boxes[i].checked && !boxes[i].disabled) { targets.push(boxes[i].getAttribute('data-cat')); }
    }
    var msg = document.getElementById('rootsApplyMsg');
    if (targets.length === 0) {
        msg.textContent = '没有需要写入的猫（其余已一致）';
        return;
    }
    var idx = 0;
    var okCount = 0;
    var fail = [];
    function next() {
        if (idx >= targets.length) {
            msg.textContent = '应用完成——写入 ' + okCount + ' 只' + (fail.length > 0 ? '；失败: ' + fail.join('、') : '') + '（新会话生效）';
            return;
        }
        var id = targets[idx];
        idx = idx + 1;
        msg.textContent = '应用中… ' + idx + '/' + targets.length;
        fetch('/api/v1/cat-config', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ cat: id, enabledRoots: rootsApplyTargets })
        })
            .then(function (r) { return r.json(); })
            .then(function (d) {
                if (d.ok) { okCount = okCount + 1; }
                else { fail.push(id + '(' + (d.error || '') + ')'); }
                return next();
            })
            .catch(function () {
                fail.push(id);
                return next();
            });
    }
    next();
}

document.getElementById('rootsApplyCats').onclick = openRootsApply;
document.getElementById('rootsApplyGo').onclick = applyRootsToCats;
document.getElementById('rootsApplyCancel').onclick = closeRootsApply;
document.getElementById('rootsApplyClose').onclick = closeRootsApply;

// 宿主重启——配置类改动（受控根保存后）由面板直接发起：只登记请求，接力由宿主主循环在全局 Idle 后执行
function restartHost() {
    var msg = document.getElementById('rootsMsg');
    if (!window.confirm('重启宿主？会在全局空闲后执行（有猫在跑或工具在途时最多等 30 秒，超时自动撤销）。重启期间页面会短暂断开，恢复后自动重连。')) {
        return;
    }
    msg.textContent = '提交重启请求…';
    fetch('/api/v1/host-restart', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ mode: 'host', push: '管理面板：配置页请求重启' })
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            msg.textContent = d.ok
                ? '已登记重启（' + d.mode + '）——' + (d.note || '')
                : '重启请求失败: ' + (d.error || '');
        })
        .catch(function () {
            msg.textContent = '重启请求已发出——宿主可能正在停机';
        });
}

document.getElementById('rootsRestart').onclick = restartHost;

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

// [段15c] 主面板专用弹层——工具清单（含人设）/ 目录白名单（2026-10-01 主面板轮）
// 定位：主面板行内入口——打开时拉该猫配置（与完整配置弹层同源）；保存走 POST /api/v1/cat-config 部分字段写（缺省字段保留旧值）
var catToolsModalEl = document.getElementById('catToolsModal');
var catRootsModalEl = document.getElementById('catRootsModal');
var catQuickTarget = '';

// 工具清单弹层——工具勾选（分组）+ 人设（同块，与完整配置弹层一致）
function openCatTools(catId, name) {
    catQuickTarget = catId;
    document.getElementById('catToolsTitle').textContent = name;
    document.getElementById('catToolsMsg').textContent = '';
    fetch('/api/v1/cat-config?cat=' + encodeURIComponent(catId))
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                document.getElementById('catToolsMsg').textContent = '读取失败: ' + (d.error || '');
                catToolsModalEl.style.display = 'flex';
                return;
            }
            catCfgAllTools = d.allTools || d.allToolNames || [];
            catCfgDefectGroups = d.defectGroups || [];
            renderGroupedChecks('catToolsChecks', catCfgAllTools, parseToolChecked(d.toolNames || ''), catId === 'majordomo' ? 'on' : 'off');
            document.getElementById('catToolsPersona').value = d.persona || '';
            catToolsModalEl.style.display = 'flex';
        })
        .catch(function () {
            document.getElementById('catToolsMsg').textContent = '读取失败——宿主未运行？';
            catToolsModalEl.style.display = 'flex';
        });
}

function saveCatTools() {
    var payload = {
        cat: catQuickTarget,
        toolNames: collectChecked('catToolsChecks').join(','),
        persona: document.getElementById('catToolsPersona').value
    };
    postCatConfig(payload, 'catToolsMsg');
}

// 目录白名单弹层——根勾选（workspace 系统根必选不可取消）
function openCatRoots(catId, name) {
    catQuickTarget = catId;
    document.getElementById('catRootsTitle').textContent = name;
    document.getElementById('catRootsMsg').textContent = '';
    fetch('/api/v1/cat-config?cat=' + encodeURIComponent(catId))
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                document.getElementById('catRootsMsg').textContent = '读取失败: ' + (d.error || '');
                catRootsModalEl.style.display = 'flex';
                return;
            }
            renderRootChecks('catRootsChecks', d.allRoots || [], d.enabledRoots || []);
            catRootsModalEl.style.display = 'flex';
        })
        .catch(function () {
            document.getElementById('catRootsMsg').textContent = '读取失败——宿主未运行？';
            catRootsModalEl.style.display = 'flex';
        });
}

function saveCatRoots() {
    var payload = {
        cat: catQuickTarget,
        enabledRoots: collectRootChecks('catRootsChecks')
    };
    postCatConfig(payload, 'catRootsMsg');
}

// 部分字段写入——POST /api/v1/cat-config（字段级合并写：出现即覆盖 / 缺省即保留；回执 = 落盘结果）
// onOk：可选成功回调（调用面同步自己的待保存清单——如前文注入弹层回填猫配置弹层的清单）
function postCatConfig(payload, msgId, onOk) {
    var msg = document.getElementById(msgId);
    msg.textContent = '保存中…';
    fetch('/api/v1/cat-config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(function (r) { return r.json(); })
        .then(function (d) {
            msg.textContent = d.ok ? '已保存——新会话生效' : ('保存失败: ' + (d.error || ''));
            if (d.ok && onOk) { onOk(); }
            // 端点同步落盘——回执到达即真相，直接刷新多猫列表（不再赌 400ms 延迟）
            if (d.ok && typeof loadCats === 'function') { loadCats(); }
        })
        .catch(function (e) {
            msg.textContent = '请求失败: ' + e.message + '（宿主未运行或端点不存在？）';
        });
}

document.getElementById('catToolsSave').onclick = saveCatTools;
document.getElementById('catToolsCancel').onclick = function () { catToolsModalEl.style.display = 'none'; };
document.getElementById('catToolsClose').onclick = function () { catToolsModalEl.style.display = 'none'; };
document.getElementById('catRootsSave').onclick = saveCatRoots;
document.getElementById('catRootsCancel').onclick = function () { catRootsModalEl.style.display = 'none'; };
document.getElementById('catRootsClose').onclick = function () { catRootsModalEl.style.display = 'none'; };

// [段16] 前文注入弹层（2026-10-01 易用性轮）——候选 = 新猫默认模板的默认前文池（配置页 → 新猫默认模板 → 默认前文）
// 入口两处：多猫行「前文注入」按钮 / 猫配置弹层「选择前文」按钮——共用同一面
// 语义：池内条目勾选（按池内顺序注入）+ 池外条目单列保留（可逐个移除——不静默丢）；保存走字段级合并写
var catInjectModalEl = document.getElementById('catInjectModal');
var catInjectTarget = '';
var catInjectPool = [];
var catInjectChecked = {};
var catInjectExtra = [];
var catInjectOnSaved = null;

function openCatInject(catId, name, onSaved) {
    catInjectTarget = catId;
    catInjectOnSaved = onSaved || null;
    catInjectPool = [];
    catInjectChecked = {};
    catInjectExtra = [];
    document.getElementById('catInjectTitle').textContent = name || catId;
    document.getElementById('catInjectMsg').textContent = '';
    var cur = null;
    var pending = 2;
    function step() {
        pending = pending - 1;
        if (pending > 0) { return; }
        classifyCatInject(cur);
        renderCatInject();
        catInjectModalEl.style.display = 'flex';
    }
    // 池 = 新猫默认模板的默认前文（读取失败 = 空池 + 出声，不静默降级）
    fetch('/api/v1/cat-default')
        .then(function (r) { return r.json(); })
        .then(function (d) { catInjectPool = (d && d.defaultInjectList) ? d.defaultInjectList.slice() : []; step(); })
        .catch(function () { step(); });
    // 现值 = 该猫 injectList（读取失败出声，不把失败当空清单）
    fetch('/api/v1/cat-config?cat=' + encodeURIComponent(catId))
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d.ok) {
                document.getElementById('catInjectMsg').textContent = '读取失败: ' + (d.error || '');
                step();
                return;
            }
            cur = d.injectList || [];
            step();
        })
        .catch(function () {
            document.getElementById('catInjectMsg').textContent = '读取失败——宿主未运行？';
            step();
        });
}

// 现值分类——池内条目进勾选态（勾上）/ 池外条目进保留区（不静默丢）
function classifyCatInject(cur) {
    catInjectChecked = {};
    catInjectExtra = [];
    var poolSet = {};
    for (var i = 0; i < catInjectPool.length; i++) { poolSet[catInjectPool[i]] = true; }
    var list = cur || [];
    for (var j = 0; j < list.length; j++) {
        if (poolSet[list[j]] === true) { catInjectChecked[list[j]] = true; }
        else { catInjectExtra.push(list[j]); }
    }
}

function renderCatInject() {
    var box = document.getElementById('catInjectPool');
    box.textContent = '';
    if (catInjectPool.length === 0) {
        var empty = document.createElement('div');
        empty.style.cssText = 'color:var(--ch-fg-faint);font-size:var(--ch-fs-tag)';
        empty.textContent = '（默认前文池为空——先到「配置页 → 新猫默认模板 → 默认前文」添加条目）';
        box.appendChild(empty);
    }
    for (var i = 0; i < catInjectPool.length; i++) {
        (function (path) {
            var row = document.createElement('label');
            row.style.cssText = 'display:flex;align-items:center;gap:6px;margin-top:3px;font-size:var(--ch-fs-tag);color:var(--ch-ok-weak);cursor:pointer';
            var cb = document.createElement('input');
            cb.type = 'checkbox';
            cb.checked = catInjectChecked[path] === true;
            cb.onchange = function () { catInjectChecked[path] = cb.checked; };
            row.appendChild(cb);
            var txt = document.createElement('span');
            txt.textContent = path;
            txt.style.cssText = 'word-break:break-all';
            row.appendChild(txt);
            box.appendChild(row);
        })(catInjectPool[i]);
    }
    var wrap = document.getElementById('catInjectExtraWrap');
    var exBox = document.getElementById('catInjectExtra');
    exBox.textContent = '';
    wrap.style.display = catInjectExtra.length > 0 ? 'block' : 'none';
    for (var j = 0; j < catInjectExtra.length; j++) {
        (function (idx) {
            var row = document.createElement('div');
            row.style.cssText = 'display:flex;gap:6px;align-items:center;margin-top:3px';
            var txt = document.createElement('span');
            txt.textContent = catInjectExtra[idx];
            txt.style.cssText = 'flex:1;color:var(--ch-warn);font-size:var(--ch-fs-tag);word-break:break-all';
            row.appendChild(txt);
            var rm = document.createElement('button');
            rm.textContent = '移除';
            rm.className = 'btn-mini tight danger';
            rm.onclick = function () { catInjectExtra.splice(idx, 1); renderCatInject(); };
            row.appendChild(rm);
            exBox.appendChild(row);
        })(j);
    }
}

// 保存载荷——池内勾选项（池序）+ 池外保留项（原序）
function catInjectPayloadList() {
    var list = [];
    for (var i = 0; i < catInjectPool.length; i++) {
        if (catInjectChecked[catInjectPool[i]] === true) { list.push(catInjectPool[i]); }
    }
    for (var j = 0; j < catInjectExtra.length; j++) { list.push(catInjectExtra[j]); }
    return list;
}

function saveCatInject() {
    var list = catInjectPayloadList();
    postCatConfig({ cat: catInjectTarget, injectList: list }, 'catInjectMsg', function () {
        if (catInjectOnSaved) { catInjectOnSaved(list); }
    });
}

document.getElementById('catInjectSave').onclick = saveCatInject;
document.getElementById('catInjectCancel').onclick = function () { catInjectModalEl.style.display = 'none'; };
document.getElementById('catInjectClose').onclick = function () { catInjectModalEl.style.display = 'none'; };
