// ═══════════════════════════════════════════
// blocks/inject-report.js —— inject_report 块（persist 类 · 前文加载报告）
//
// 契约：
//   item = { type:'inject_report', ts, msgIndex, round,
//            payload:{ files:[{file, status, chars, bytes?, message?}], total, ok, missing, failed,
//                     toolGroups:[{group, tools:[{name}]}], mount? } }
//   status = ok / missing / error 三态；mount = 当前挂载的项目名（空=未挂载）
//
// 形态（2026-11-07 重做）：
//   📚 汇总头 + 两栏并排 + 连续同父收纳 + 只显示 token(k)
//   行 = 「目录(件数) 文件名…  ×k」；同父且相邻的文件收成一行，折行续行悬挂缩进
//   项目挂载：默认只到基线（reference-purge）收尾；按钮点击时才向宿主取项目清单
//            （含每项目最后改动时间）；选中 = 宿主把项目 Job/档案追加进 injectList
//            并立即重建会话（等同新会话重注入），取消 = 移除后再重建
//
// 产出：.chat-row.assistant.inject > .chat-plain.inject > .inject-report
// 来源：chat-view.js chatInjectReportHtml（第 268-300 行）
// 转义：统一走 mdEscapeHtml（lib/md.js 唯一转义入口）
// ═══════════════════════════════════════════

// 体量 → token（与宿主 PromptPlanBuilder.TokensPerByte 同口径：0.32 token/字节）
var IR_TOK_PER_BYTE = 0.32;

function irNorm(p) { return String(p || '').replace(/\\/g, '/').replace(/\/+$/, ''); }

function irDirOf(p) {
    var t = irNorm(p);
    var i = t.lastIndexOf('/');
    if (i >= 0) { return t.slice(0, i); }
    var c = t.indexOf(':');
    return c > 1 ? t.slice(0, c + 1) : '';
}

function irBaseOf(p) {
    var t = irNorm(p);
    var i = t.lastIndexOf('/');
    if (i >= 0) { return t.slice(i + 1); }
    var c = t.indexOf(':');
    return c > 1 ? t.slice(c + 1) : t;
}

// 逐文件结果 → 行（合并只发生在相邻且同父的文件之间，顺序即注入顺序）
function irRows(files) {
    var rows = [];
    for (var i = 0; i < files.length; i++) {
        var f = files[i];
        var name = f.file || '';
        var dir = irDirOf(name);
        var last = rows[rows.length - 1];
        if (last && last.dir === dir) {
            last.mem.push(irBaseOf(name));
            last.count = last.count + 1;
            last.bytes = last.bytes + (f.bytes || 0);
            last.chars = last.chars + (f.chars || 0);
            if (f.status !== 'ok') { last.bad = true; }
        } else {
            rows.push({
                dir: dir,
                mem: [irBaseOf(name)],
                count: 1,
                bytes: (f.bytes || 0),
                chars: (f.chars || 0),
                bad: f.status !== 'ok'
            });
        }
    }
    return rows;
}

// 行 token（bytes 优先；缺失时按字符数估算）
function irTok(row) {
    if (row.bytes > 0) { return row.bytes * IR_TOK_PER_BYTE; }
    return row.chars * IR_TOK_PER_BYTE;
}

function irRowHtml(r) {
    var cls = r.bad ? ' missing' : '';
    var tk = irTok(r) / 1000;
    return '<div class="ir-row' + cls + '"><span><span class="ir-dir">' + mdEscapeHtml(r.dir) + '</span>'
        + '<span class="ir-n">(' + r.count + '件)</span>'
        + '<span class="ir-mem">' + mdEscapeHtml(r.mem.join(' ')) + '</span></span>'
        + '<span class="ir-tk">' + tk.toFixed(1) + 'k</span></div>';
}

function irColsHtml(rows) {
    var half = Math.ceil(rows.length / 2);
    var left = [], right = [];
    for (var i = 0; i < rows.length; i++) {
        (i < half ? left : right).push(irRowHtml(rows[i]));
    }
    return '<div class="ir-cols"><div class="ir-col">' + left.join('') + '</div>'
        + '<div class="ir-col">' + right.join('') + '</div></div>';
}

// 挂载态：payload.mount 优先；缺失时按「存在 ccbp:Project/<名> 行」推断
function irMount(rows, d) {
    if (d && d.mount) { return d.mount; }
    for (var i = 0; i < rows.length; i++) {
        var m = rows[i].dir.match(/^ccbp:Project\/([^\/]+)/);
        if (m) { return m[1]; }
    }
    return '';
}

// 最后改动时间：24 小时内 → 「N 分钟之前」；超过 → 「MM-DD HH:mm」
function irAgo(iso) {
    var t = new Date(iso).getTime();
    if (isNaN(t)) { return ''; }
    var min = Math.floor((Date.now() - t) / 60000);
    if (min < 24 * 60) { return (min < 1 ? 0 : min) + ' 分钟之前'; }
    var d = new Date(t);
    var p = function (n) { return (n < 10 ? '0' : '') + n; };
    return p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
}

function injectReportHtml(p) {
    var d = p || {};
    var files = d.files || [];
    var total = d.total || 0;
    var ok = d.ok || 0;
    var missing = d.missing || 0;
    var failed = d.failed || 0;
    var rows = irRows(files);
    var mount = irMount(rows, d);
    var tokK = 0;
    for (var i = 0; i < rows.length; i++) { tokK = tokK + irTok(rows[i]); }
    tokK = Math.round(tokK / 1000);

    var html = '<div class="inject-report">'
        + '<div class="ir-head">📚 前文加载：' + rows.length + ' 行 / ' + files.length + ' 件 · ' + ok + ' 成功'
        + (missing > 0 ? ' · 缺失 ' + missing : '')
        + (failed > 0 ? ' · 失败 ' + failed : '')
        + ' · ≈' + tokK + 'k token</div>';
    if (rows.length > 0) { html += irColsHtml(rows); }
    html += '<div class="ir-actions">'
        + '<button class="ir-btn' + (mount ? ' on' : '') + '" data-ir-mount="1">'
        + (mount ? '✕ 取消 Project 挂载' : '＋ 选择项目') + '</button>'
        + '<span class="ir-hint">' + (mount ? ('已挂载 ' + mdEscapeHtml(mount)) : '未挂载项目——只带基线') + '</span>'
        + '</div>'
        + '<div class="ir-picker" style="display:none"></div>';
    // 注入工具组——独立分区（组名 + 工具名列表；desc 不进展示）
    var groups = d.toolGroups || [];
    if (groups.length > 0) {
        var toolCount = 0;
        for (var g = 0; g < groups.length; g++) { toolCount = toolCount + (groups[g].tools || []).length; }
        html += '<div class="ir-tools"><div class="ir-tools-head">🛠️ 已启用 ' + toolCount + ' 个工具</div>';
        for (var g2 = 0; g2 < groups.length; g2++) {
            var names = [];
            var tools = groups[g2].tools || [];
            for (var t = 0; t < tools.length; t++) { names.push(tools[t].name); }
            html += '<div class="ir-tool-row"><span class="ir-tool-group">'
                + mdEscapeHtml(groups[g2].group || '内置') + '</span> —— '
                + mdEscapeHtml(names.join('、')) + '</div>';
        }
        html += '</div>';
    }
    html += '</div>';
    return html;
}

// 项目挂载交互——点击时才向宿主取清单（宿主侧执行 project-mtime.ps1）
function irBindMount(bubble, mountNow) {
    var btn = bubble.querySelector('[data-ir-mount]');
    if (!btn) { return; }
    var picker = bubble.querySelector('.ir-picker');
    var hint = bubble.querySelector('.ir-hint');

    function catId() {
        return (typeof appState !== 'undefined' && appState && appState.meta && appState.meta.catId)
            ? appState.meta.catId : '';
    }

    function post(project) {
        // 挂载语义在宿主侧：会话未开对话 → 立即重建生效；已有对话 → 只落盘，下个新会话生效（不弹确认）
        btn.disabled = true;
        fetch('/api/v1/inject/project', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ cat: catId(), project: project || '' })
        }).then(function (r) { return r.json(); }).then(function (res) {
            btn.disabled = false;
            if (!res || !res.ok) {
                if (hint) { hint.textContent = '挂载失败：' + ((res && res.reason) || '未知'); }
                return;
            }
            if (hint) { hint.textContent = res.msg || '已受理——会话重建中'; }
        }).catch(function (e) {
            btn.disabled = false;
            if (hint) { hint.textContent = '挂载失败：' + e; }
        });
    }

    function renderPicker(list) {
        var html = '';
        for (var i = 0; i < list.length; i++) {
            html += '<div class="ir-pick" data-proj="' + mdEscapeHtml(list[i].name) + '">'
                + '<span class="pn">' + mdEscapeHtml(list[i].name) + '</span>'
                + '<span class="pt">' + mdEscapeHtml(irAgo(list[i].lastWrite)) + '</span></div>';
        }
        picker.innerHTML = html;
        var items = picker.querySelectorAll('.ir-pick');
        for (var k = 0; k < items.length; k++) {
            items[k].onclick = function () {
                post(this.getAttribute('data-proj'));
                picker.style.display = 'none';
            };
        }
        picker.style.display = 'grid';
    }

    btn.onclick = function () {
        if (mountNow) { post(''); return; }
        if (picker.style.display === 'grid') { picker.style.display = 'none'; return; }
        btn.disabled = true;
        fetch('/api/v1/inject/projects')
            .then(function (r) { return r.json(); })
            .then(function (res) {
                btn.disabled = false;
                if (!res || !res.ok) {
                    if (hint) { hint.textContent = '取项目清单失败：' + ((res && res.reason) || '未知'); }
                    return;
                }
                renderPicker(res.projects || []);
            })
            .catch(function (e) {
                btn.disabled = false;
                if (hint) { hint.textContent = '取项目清单失败：' + e; }
            });
    };
}

function buildInjectReportBlock(payload) {
    // 注入报告块——前文加载明细（独立持久化字段，重建不清）
    var row = blockRow('inject_report');
    var bubble = el('div', bodyClass('inject_report'));
    bubble.innerHTML = injectReportHtml(payload);
    var d = payload || {};
    var mount = irMount(irRows(d.files || []), d);
    irBindMount(bubble, mount);
    row.appendChild(bubble);
    return row;
}
