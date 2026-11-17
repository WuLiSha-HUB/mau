// CH4 外观层——panel-token.js：Token 用量统计区（A390 · A388/A389 的消费面）
// 数据源：GET /api/v1/token-stats（快照；后端每分钟重算一次）· POST /api/v1/token-stats/refresh（立即重算）
// 快照字段：refreshedAt / scanMs / badLines / totals{requests,prompt,completion,cacheHit,miss} /
//           cats[{catKey,displayName,requests,prompt,completion,cacheHit,miss,days[{day,...,lastTs}]}]
// 边界：本页只读 + 一个「立即重算」动作；写入（用量流水）归会话侧 TokenUsageStore，本页不做第二套写路径。

var tokenInfoEl = document.getElementById('tokenInfo');
var tokenRefreshBtn = document.getElementById('tokenRefresh');
var tokenTotalsEl = document.getElementById('tokenTotals');
var tokenCatsEl = document.getElementById('tokenCats');

// 结构签名——同构时不重排 DOM（稳态零变动；数值变化才重建）
var lastTokenSig = '';

// 两位补零
function tokenPad2(n) {
    return n < 10 ? ('0' + n) : ('' + n);
}

// 毫秒时间戳 → 本地 yyyy-MM-dd HH:mm:ss
function tokenFmtTs(ms) {
    if (!ms) { return '—'; }
    var d = new Date(ms);
    return d.getFullYear() + '-' + tokenPad2(d.getMonth() + 1) + '-' + tokenPad2(d.getDate()) + ' ' + tokenPad2(d.getHours()) + ':' + tokenPad2(d.getMinutes()) + ':' + tokenPad2(d.getSeconds());
}

// 数字千分位（空值按 0）
function tokenFmtNum(n) {
    if (!n) { return '0'; }
    return n.toLocaleString('en-US');
}

// 五项用量读数——标签 + 数值（横向一格）
function buildTokenMetric(label, value, color) {
    var box = document.createElement('div');
    box.style.cssText = 'display:flex;flex-direction:column;gap:2px;min-width:96px';
    var cap = document.createElement('span');
    cap.textContent = label;
    cap.style.cssText = 'color:var(--ch-fg-faint);font-size:var(--ch-fs-tag)';
    box.appendChild(cap);
    var val = document.createElement('span');
    val.textContent = value;
    val.style.cssText = 'color:' + color + ';font-size:var(--ch-fs-sub)';
    box.appendChild(val);
    return box;
}

// 渲染合计条——请求数 / 输入 / 输出 / 缓存命中 / 未命中
function renderTokenTotals(totals) {
    if (!tokenTotalsEl) { return; }
    tokenTotalsEl.textContent = '';
    var t = totals || {};
    tokenTotalsEl.appendChild(buildTokenMetric('请求数', tokenFmtNum(t.requests), 'var(--ch-fg-strong)'));
    tokenTotalsEl.appendChild(buildTokenMetric('输入 token', tokenFmtNum(t.prompt), 'var(--ch-fg-muted)'));
    tokenTotalsEl.appendChild(buildTokenMetric('输出 token', tokenFmtNum(t.completion), 'var(--ch-ok)'));
    tokenTotalsEl.appendChild(buildTokenMetric('缓存命中', tokenFmtNum(t.cacheHit), 'var(--ch-fg-weak)'));
    tokenTotalsEl.appendChild(buildTokenMetric('未命中', tokenFmtNum(t.miss), 'var(--ch-fg-weak)'));
}

// 按日一行——日期 / 请求数 / 输入 / 输出 / 命中 / 未命中 / 最后请求时刻
function buildTokenDayRow(day) {
    var row = document.createElement('div');
    row.style.cssText = 'display:flex;align-items:center;gap:10px;padding:4px 0;border-bottom:1px solid var(--ch-line-faint)';
    var cells = [
        { text: day.day || '', css: 'flex:0 0 92px;color:var(--ch-fg-muted);font-size:var(--ch-fs-tag)' },
        { text: tokenFmtNum(day.requests), css: 'flex:0 0 64px;color:var(--ch-fg-strong);font-size:var(--ch-fs-tag)' },
        { text: tokenFmtNum(day.prompt), css: 'flex:1;min-width:0;color:var(--ch-fg-weak);font-size:var(--ch-fs-tag)' },
        { text: tokenFmtNum(day.completion), css: 'flex:1;min-width:0;color:var(--ch-ok);font-size:var(--ch-fs-tag)' },
        { text: tokenFmtNum(day.cacheHit), css: 'flex:1;min-width:0;color:var(--ch-fg-faint);font-size:var(--ch-fs-tag)' },
        { text: tokenFmtNum(day.miss), css: 'flex:1;min-width:0;color:var(--ch-fg-faint);font-size:var(--ch-fs-tag)' },
        { text: tokenFmtTs(day.lastTs), css: 'flex:0 0 150px;color:var(--ch-fg-faint);font-size:var(--ch-fs-tag)' }
    ];
    for (var i = 0; i < cells.length; i = i + 1) {
        var el = document.createElement('span');
        el.textContent = cells[i].text;
        el.style.cssText = cells[i].css + ';overflow:hidden;text-overflow:ellipsis;white-space:nowrap';
        row.appendChild(el);
    }
    return row;
}

// 单猫卡片——标题（显示名 + key）+ 五项读数 + 按日明细
function buildTokenCatCard(cat) {
    var card = document.createElement('div');
    card.style.cssText = 'margin:10px 12px;border:1px solid var(--ch-line);border-radius:6px;background:var(--ch-bg-panel)';
    var head = document.createElement('div');
    head.style.cssText = 'display:flex;align-items:center;gap:10px;padding:8px 12px;border-bottom:1px solid var(--ch-line-faint);flex-wrap:wrap';
    var name = document.createElement('span');
    name.textContent = cat.displayName || cat.catKey || '(未知)';
    name.style.cssText = 'color:var(--ch-fg-strong)';
    head.appendChild(name);
    var key = document.createElement('span');
    key.textContent = cat.catKey || '';
    key.style.cssText = 'color:var(--ch-fg-faint);font-size:var(--ch-fs-tag)';
    head.appendChild(key);
    var spacer = document.createElement('span');
    spacer.style.cssText = 'flex:1';
    head.appendChild(spacer);
    head.appendChild(buildTokenMetric('请求数', tokenFmtNum(cat.requests), 'var(--ch-fg-strong)'));
    head.appendChild(buildTokenMetric('输入', tokenFmtNum(cat.prompt), 'var(--ch-fg-muted)'));
    head.appendChild(buildTokenMetric('输出', tokenFmtNum(cat.completion), 'var(--ch-ok)'));
    head.appendChild(buildTokenMetric('命中', tokenFmtNum(cat.cacheHit), 'var(--ch-fg-weak)'));
    head.appendChild(buildTokenMetric('未命中', tokenFmtNum(cat.miss), 'var(--ch-fg-weak)'));
    card.appendChild(head);
    var days = cat.days || [];
    var body = document.createElement('div');
    body.style.cssText = 'padding:6px 12px 10px 12px';
    if (days.length === 0) {
        var empty = document.createElement('span');
        empty.textContent = '暂无用量记录';
        empty.style.cssText = 'color:var(--ch-fg-faint);font-size:var(--ch-fs-tag)';
        body.appendChild(empty);
    } else {
        for (var i = 0; i < days.length; i = i + 1) {
            body.appendChild(buildTokenDayRow(days[i]));
        }
    }
    card.appendChild(body);
    return card;
}

// 渲染统计区——首屏 / 手动刷新 / 定时拉取共用同一路径
function renderTokenStats(data) {
    if (!tokenCatsEl) { return; }
    var cats = (data && data.cats) || [];
    var totals = (data && data.totals) || {};
    var sigParts = ['t:' + totals.requests + '|' + totals.prompt + '|' + totals.completion + '|' + totals.cacheHit + '|' + totals.miss];
    for (var i = 0; i < cats.length; i++) {
        var c = cats[i];
        var lastTs = 0;
        var days = c.days || [];
        if (days.length > 0) { lastTs = days[days.length - 1].lastTs || 0; }
        sigParts.push(c.catKey + '|' + c.requests + '|' + c.prompt + '|' + c.completion + '|' + c.cacheHit + '|' + c.miss + '|' + days.length + '|' + lastTs);
    }
    var sig = sigParts.join('\u0001');
    if (sig === lastTokenSig) {
        if (tokenInfoEl) {
            tokenInfoEl.textContent = 'Token 用量——上次重算 ' + tokenFmtTs(data.refreshedAt) + '（扫描 ' + (data.scanMs || 0) + ' ms）；无新记录';
        }
        return;
    }
    lastTokenSig = sig;
    renderTokenTotals(totals);
    tokenCatsEl.textContent = '';
    for (var j = 0; j < cats.length; j = j + 1) {
        tokenCatsEl.appendChild(buildTokenCatCard(cats[j]));
    }
    if (cats.length === 0) {
        var emptyAll = document.createElement('div');
        emptyAll.textContent = '暂无任何猫的用量记录——有 LLM 请求后即出现';
        emptyAll.style.cssText = 'padding:16px 12px;color:var(--ch-fg-faint);font-size:var(--ch-fs-tag)';
        tokenCatsEl.appendChild(emptyAll);
    }
    if (tokenInfoEl) {
        var badText = (data.badLines > 0) ? ('，坏行 ' + data.badLines + ' 条（已跳过）') : '';
        tokenInfoEl.textContent = 'Token 用量——' + cats.length + ' 只猫 / 共 ' + tokenFmtNum(totals.requests) + ' 次请求；上次重算 ' + tokenFmtTs(data.refreshedAt) + '（扫描 ' + (data.scanMs || 0) + ' ms）' + badText;
    }
}

// 拉取快照——GET 读缓存快照（后端每分钟重算，本页不触发扫描）
function loadTokenStats() {
    fetch('/api/v1/token-stats')
        .then(function (r) { return r.json(); })
        .then(function (d) {
            if (!d || d.ok !== true) {
                if (tokenInfoEl) { tokenInfoEl.textContent = '读取失败：' + ((d && d.error) || '未知错误'); }
                return;
            }
            renderTokenStats(d);
        })
        .catch(function () {
            if (tokenInfoEl) { tokenInfoEl.textContent = '读取失败——宿主未运行？'; }
        });
}

// 立即重算——POST 触发后端全量扫描，返回新快照（重置签名强制重绘）
if (tokenRefreshBtn) {
    tokenRefreshBtn.addEventListener('click', function () {
        tokenRefreshBtn.disabled = true;
        if (tokenInfoEl) { tokenInfoEl.textContent = '重算中…'; }
        fetch('/api/v1/token-stats/refresh', { method: 'POST' })
            .then(function (r) { return r.json(); })
            .then(function (d) {
                if (!d || d.ok !== true) {
                    if (tokenInfoEl) { tokenInfoEl.textContent = '重算失败：' + ((d && d.error) || '未知错误'); }
                    return;
                }
                lastTokenSig = '';
                renderTokenStats(d);
            })
            .catch(function () {
                if (tokenInfoEl) { tokenInfoEl.textContent = '重算请求失败——宿主未运行？'; }
            })
            .then(function () { tokenRefreshBtn.disabled = false; });
    });
}

// 首屏拉一次 + 每分钟跟随后端重算节奏再拉（页面常驻不重复建连）
loadTokenStats();
setInterval(loadTokenStats, 60000);
