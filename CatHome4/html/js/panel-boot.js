// CH4 外观层——panel-boot.js：启动健康区（快照 boot 段渲染；A260 启动韧性）
// 由 app.js applySnapshot 调用。零框架零依赖；DOM API + textContent（安全铁律）。
// 数据源：快照 boot 段（后端 BuildBootSegment）——与日志 / 启动报告 / 提示窗同源。

var bootRenderKey = '';

/**
 * 渲染启动健康区——降级清单 + 报告路径 + 诊断落点。
 * @param {object|null} boot 快照 boot 段
 */
function renderBoot(boot) {
    var listEl = document.getElementById('bootList');
    var infoEl = document.getElementById('bootInfo');
    if (!listEl || !infoEl) { return; }
    if (!boot) { return; }
    var items = boot.degraded || [];
    var key = String(boot.ok) + '|' + String(boot.count) + '|' + String(boot.reportPath || '') + '|' + String(boot.sink || '');
    if (key === bootRenderKey) { return; }
    bootRenderKey = key;

    if (boot.ok) {
        infoEl.textContent = '启动正常——无降级项';
        infoEl.style.color = 'var(--ch-ok)';
    } else {
        infoEl.textContent = '启动降级 ' + boot.count + ' 项（修复后重启即可恢复）';
        infoEl.style.color = 'var(--ch-warn)';
    }

    listEl.textContent = '';
    for (var i = 0; i < items.length; i++) {
        listEl.appendChild(buildBootItem(items[i]));
    }

    var reportEl = document.getElementById('bootReport');
    if (reportEl) {
        reportEl.textContent = boot.reportPath ? ('启动报告：' + boot.reportPath) : '启动报告：未落盘';
    }
    var sinkEl = document.getElementById('bootSink');
    if (sinkEl) {
        sinkEl.textContent = boot.sink ? ('诊断日志：' + boot.sink) : '诊断日志：仅内存';
    }
}

/**
 * 构建单条降级卡片（DOM API 组装，文本一律 textContent）。
 * @param {object} d 降级条目
 * @returns {HTMLElement} 卡片元素
 */
function buildBootItem(d) {
    var card = document.createElement('div');
    card.className = 'boot-item';

    var head = document.createElement('div');
    head.className = 'boot-head';
    var codeEl = document.createElement('span');
    codeEl.className = 'boot-code';
    codeEl.textContent = d.code + (d.count > 1 ? ' ×' + d.count : '');
    var abilityEl = document.createElement('span');
    abilityEl.className = 'boot-ability';
    abilityEl.textContent = d.ability + ' · ' + d.state + ' · ' + d.stage;
    head.appendChild(codeEl);
    head.appendChild(abilityEl);

    var detailEl = document.createElement('div');
    detailEl.className = 'boot-detail';
    detailEl.textContent = d.detail;

    var fixEl = document.createElement('div');
    fixEl.className = 'boot-fix';
    fixEl.textContent = '处理：' + d.fix;

    card.appendChild(head);
    card.appendChild(detailEl);
    card.appendChild(fixEl);
    return card;
}
