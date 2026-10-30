// CH4 外观层——panel-res.js：资源占用区（A254 · design-ch4-pytools.md §七）
// 依赖：app.js 先加载；渲染入口 renderRes(res) 由 app.js applySnapshot 调用。
// 数据源：快照 res 段——每秒一次固定刷新参（ResProbe 采集：本体进程 / py 服务 / GPU 显存）。
// 曲线为前端自缓存自绘制（后端不存历史）；保留最近 180 点（1 秒一点 = 3 分钟），F5 刷新清空重来。

var resInfoEl = document.getElementById('resInfo');
var resProcEl = document.getElementById('resProc');
var resLoopEl = document.getElementById('resLoop');
var resPyEl = document.getElementById('resPy');
var resNodeEl = document.getElementById('resNode');
var resGpuEl = document.getElementById('resGpu');
var resPauseBtn = document.getElementById('resPause');

// 主循环分段名 → 展示名（与宿主 LoopTiming 的段名一一对应）
var RES_SEG_NAMES = {
    tick: 'Flow Tick',
    http: 'HTTP 快照泵',
    hosts: 'Admin 宿主泵',
    sessions: '会话泵',
    catqueue: '猫队列泵',
    qqbot: 'QQBot',
    misc: '其它（重启 / 托盘 / 自启轮询）',
    'cs.summary': '会话·轮末总结轮询',
    'cs.pause': '会话·中止收尾',
    'cs.idle': '会话·空闲检查 / 轮首',
    'cs.llm': '会话·LLM 轮询',
    'cs.tool': '会话·工具批轮询',
    'cs.close': '会话·轮末收口',
    'cs.delays': '会话·延迟队列泵'
};

// 曲线窗口——固定 180 秒时间轴（A259；每秒一点，数据不足时自左端逐秒生长）
var RES_WINDOW_SEC = 180;
var RES_WINDOW_MS = RES_WINDOW_SEC * 1000;

// X 轴刻度分段数——180 秒 / 30 秒一格 = 6 段（7 条刻度线）
var RES_XSEG = 6;

// 采样点数上限——按时间裁剪之外的防爆阀（采样抖动时不至于无限增长）
var RES_MAXPTS = RES_WINDOW_SEC * 2;

// 历史缓存——{ t, wsMb, gpuPct, loopMs }（t = 采样时刻 Unix 毫秒；F5 重置归零）
var resHistory = [];

// 采样判重——同一时刻的采样只处理一次（applySnapshot 每帧都会被调用）
var resLastT = -1;

// 曲线暂停标记——暂停后仍更新数字块，只是不再追加曲线点
var resPaused = false;

// 图表元信息——Y 轴基准档（0 起、基准 ×2 翻倍；base<=0 = 固定满分档）/ 单位口径 / 曲线色
var RES_CHARTS = [
    { id: 'resChartMem', key: 'wsMb', base: 500, unit: 'mem', color: 'mem' },
    { id: 'resChartGpu', key: 'gpuPct', base: 0, unit: 'pct', color: 'gpu' },
    { id: 'resChartLoop', key: 'loopMs', base: 25, unit: 'ms', color: 'loop' }
];

// 绘图区内边距——左留 Y 轴刻度、下留 X 轴刻度、右留末档标签半宽
var RES_PAD_L = 56;
var RES_PAD_R = 22;
var RES_PAD_T = 10;
var RES_PAD_B = 20;

// 图表样式缓存——canvas 用不了 var()，初始化时从令牌探测一次（主题与字号档固定）
var resChartStyle = null;

/**
 * 读图表样式——令牌色 + 刻度字号 / 字体（探测元素取计算值；读取失败回落中性值）
 * @returns {object} 样式表（mem / gpu / loop / frame / grid / axis / font）
 */
function resReadStyle() {
    if (resChartStyle) { return resChartStyle; }
    var cs = getComputedStyle(document.documentElement);
    function pick(name, fb) {
        var v = cs.getPropertyValue(name);
        return (v && v.trim()) ? v.trim() : fb;
    }
    var probe = document.createElement('span');
    probe.style.cssText = 'position:absolute;visibility:hidden;font-size:var(--ch-fs-tag);font-family:var(--ch-font-ui)';
    document.body.appendChild(probe);
    var pcs = getComputedStyle(probe);
    var font = pcs.fontSize + ' ' + pcs.fontFamily;
    document.body.removeChild(probe);
    resChartStyle = {
        mem: pick('--ch-chart-mem', '#4a9eff'),
        gpu: pick('--ch-chart-gpu', '#ffa64a'),
        loop: pick('--ch-chart-loop', '#7ec97e'),
        frame: pick('--ch-chart-frame', '#3a3a3a'),
        grid: pick('--ch-chart-grid', '#2a2a2a'),
        axis: pick('--ch-chart-axis-fg', '#6a6a6a'),
        font: font
    };
    return resChartStyle;
}

/**
 * 字节 → MB 文本（0 或不可读 → '-'）
 * @param {number} bytes 字节数
 * @returns {string} 展示文本
 */
function fmtResMb(bytes) {
    var v = Number(bytes) || 0;
    if (v <= 0) { return '-'; }
    return (v / 1048576).toFixed(1) + ' MB';
}

/**
 * 建一行——名称 + 值（两列，与依赖页签行同款观感）
 * @param {string} name 名称
 * @param {string} value 值文本
 * @returns {HTMLElement} 行元素
 */
function buildResRow(name, value) {
    var row = document.createElement('div');
    row.style.cssText = 'display:flex;align-items:center;gap:10px;padding:6px 12px;border-bottom:1px solid var(--ch-line-faint)';
    var label = document.createElement('span');
    label.textContent = name;
    label.style.cssText = 'flex:0 0 150px;color:var(--ch-fg-strong)';
    row.appendChild(label);
    var val = document.createElement('span');
    val.textContent = value;
    val.style.cssText = 'flex:1;min-width:0;color:var(--ch-fg-weak);font-size:var(--ch-fs-tag);overflow:hidden;text-overflow:ellipsis;white-space:nowrap';
    row.appendChild(val);
    return row;
}

/**
 * 渲染资源截面——数字块 + 曲线缓存追加 + 重绘
 * @param {object} res 快照 res 段（t / proc / py / gpu）
 */
function renderRes(res) {
    if (!res || !resProcEl) { return; }
    if (res.t === resLastT) { return; }   // 同一采样只处理一次
    resLastT = res.t;

    renderResProc(res.proc || {}, res.loop || {});
    renderResLoop(res.loop || {});
    renderResPy(res.py || []);
    renderResNode(res.node || []);
    renderResGpu(res.gpu || {});

    // 曲线缓存——暂停时不追加（数字块照常更新）
    if (!resPaused) {
        var proc = res.proc || {};
        var gpu = res.gpu || {};
        var loop = res.loop || {};
        var wsMb = (Number(proc.ws) || 0) / 1048576;
        var gpuPct = (Number(gpu.pct) || 0);
        var loopMs = (Number(loop.avgMs) || 0);
        resHistory.push({ t: Number(res.t) || Date.now(), wsMb: wsMb, gpuPct: gpuPct, loopMs: loopMs });
        // 窗口裁剪——按时间（180 秒）裁，另设点数上限防采样抖动爆量
        var newestT = resHistory[resHistory.length - 1].t;
        while (resHistory.length > 0 && newestT - resHistory[0].t > RES_WINDOW_MS) { resHistory.shift(); }
        while (resHistory.length > RES_MAXPTS) { resHistory.shift(); }
    }
    drawResCharts();
}

/**
 * 本体进程块——工作集 / 私有字节 / 托管堆分代 / 主循环帧耗时
 * @param {object} proc 快照 proc 段
 * @param {object} loop 快照 loop 段（主循环帧耗时窗口统计）
 */
function renderResProc(proc, loop) {
    resProcEl.textContent = '';
    resProcEl.appendChild(buildResRow('工作集（WS）', fmtResMb(proc.ws)));
    resProcEl.appendChild(buildResRow('私有字节', fmtResMb(proc.private)));
    resProcEl.appendChild(buildResRow('托管堆', fmtResMb(proc.heap)));
    resProcEl.appendChild(buildResRow('已提交（GC committed）', fmtResMb(proc.committed)));
    resProcEl.appendChild(buildResRow('分代 G0 / G1 / G2 / LOH',
        fmtResMb(proc.g0) + ' / ' + fmtResMb(proc.g1) + ' / ' + fmtResMb(proc.g2) + ' / ' + fmtResMb(proc.loh)));
    resProcEl.appendChild(buildResRow('主循环帧耗时（均值 / 峰值）',
        (Number(loop.avgMs) || 0).toFixed(2) + ' / ' + (Number(loop.peakMs) || 0).toFixed(2) + ' ms'));
}

/**
 * 主循环分段块——逐段一行（展示名 / 均值 / 峰值）
 * @param {object} loop 快照 loop 段（含 segments 数组）
 */
function renderResLoop(loop) {
    if (!resLoopEl) { return; }
    resLoopEl.textContent = '';
    // 超帧统计（2026-10-16）——帧预算 / 实测帧率 / 累计超帧 / 上一秒超帧与占比
    var budgetMs = Number(loop.budgetMs) || 0;
    var secFrames = Number(loop.secFrames) || 0;
    var secOverruns = Number(loop.secOverruns) || 0;
    var secPct = Number(loop.secOverrunPct) || 0;
    resLoopEl.appendChild(buildResRow('帧预算 / 实测帧率', budgetMs + ' ms / ' + secFrames + ' fps'));
    resLoopEl.appendChild(buildResRow('超帧（累计 / 上一秒）',
        (Number(loop.overrunTotal) || 0) + ' / ' + secOverruns + '（占上一秒 ' + secPct.toFixed(1) + ' %）'));
    var segs = loop.segments || [];
    if (segs.length === 0) {
        resLoopEl.appendChild(buildResRow('（无）', '尚无采样'));
        return;
    }
    for (var i = 0; i < segs.length; i = i + 1) {
        var s = segs[i];
        var label = RES_SEG_NAMES[s.name] || s.name;
        resLoopEl.appendChild(buildResRow(label,
            (Number(s.avgMs) || 0).toFixed(2) + ' / ' + (Number(s.peakMs) || 0).toFixed(2) + ' ms'));
    }
}

/**
 * py 服务块——逐服务一行（名称 / 状态 / PID / 工作集）
 * @param {Array} list 快照 py 段
 */
function renderResPy(list) {
    resPyEl.textContent = '';
    if (list.length === 0) {
        resPyEl.appendChild(buildResRow('（无）', '未声明服务或 IPyService 未注入'));
        return;
    }
    for (var i = 0; i < list.length; i = i + 1) {
        var s = list[i];
        var state = s.alive ? '运行中' : '未启动';
        var detail = state + (s.pid > 0 ? (' · pid=' + s.pid) : '') + ' · ' + fmtResMb(s.ws);
        resPyEl.appendChild(buildResRow(s.name || '(未命名)', detail));
    }
}

/**
 * node 服务块——逐服务一行（名称 / 状态 / PID / 工作集）
 * @param {Array} list 快照 node 段（与 py 段并列独立——design-ch4-node-services §五）
 */
function renderResNode(list) {
    if (!resNodeEl) { return; }
    resNodeEl.textContent = '';
    if (list.length === 0) {
        resNodeEl.appendChild(buildResRow('（无）', '未声明服务或 INodeService 未注入'));
        return;
    }
    for (var i = 0; i < list.length; i = i + 1) {
        var s = list[i];
        var state = s.alive ? '运行中' : '未启动';
        var detail = state + (s.pid > 0 ? (' · pid=' + s.pid) : '') + ' · ' + fmtResMb(s.ws);
        resNodeEl.appendChild(buildResRow(s.name || '(未命名)', detail));
    }
}

/**
 * GPU 块——已用 / 总量 / 占比（WDDM 下按进程显存不可得，只报全局）
 * @param {object} gpu 快照 gpu 段
 */
function renderResGpu(gpu) {
    resGpuEl.textContent = '';
    if (!gpu.available) {
        resGpuEl.appendChild(buildResRow('（不可用）', 'nvidia-smi 未提供数据'));
        return;
    }
    resGpuEl.appendChild(buildResRow('显存已用 / 总量', gpu.usedMib + ' / ' + gpu.totalMib + ' MiB'));
    resGpuEl.appendChild(buildResRow('显存占比', gpu.pct + ' %'));
}

// ── 折线绘制（零依赖：canvas 手绘；固定 180 秒时间轴 + 自适应 Y 档位）──
/**
 * Y 轴上限——0 起、按基准数 ×2 翻倍取档，刚好罩住当前峰值（base<=0 = 固定 0..100）
 * @param {number} peak 窗口内峰值
 * @param {number} base 基准档（内存 500 MB / 主循环 25 ms）
 * @returns {number} 轴上限
 */
function resAxisMax(peak, base) {
    if (base <= 0) { return 100; }
    var m = base;
    while (m < peak) { m = m * 2; }
    return m;
}

/**
 * 数值去尾零——整数不带小数点，小数最多一位
 * @param {number} v 数值
 * @returns {string} 文本
 */
function resNum(v) {
    var r = Math.round(v * 10) / 10;
    return r === Math.round(r) ? String(Math.round(r)) : r.toFixed(1);
}

/**
 * 轴刻度数值 → 文本（内存自动进位 MB → GB；百分比带 %；毫秒带 ms）
 * @param {number} v 数值
 * @param {string} unit 单位口径（mem / pct / ms）
 * @returns {string} 展示文本
 */
function resFmtAxis(v, unit) {
    if (unit === 'pct') { return resNum(v) + '%'; }
    if (unit === 'ms') { return resNum(v) + ' ms'; }
    if (v >= 1000) { return resNum(v / 1000) + ' GB'; }
    return resNum(v) + ' MB';
}

/**
 * 画一张资源曲线——灰框 + 网格 + XY 刻度 + 折线 + 峰值 / 当前标注
 * @param {object} def 图表元信息（RES_CHARTS 项）
 */
function drawResChart(def) {
    var cv = document.getElementById(def.id);
    if (!cv) { return; }
    // 隐藏页签下画布尺寸为 0——直接跳过（否则按回落尺寸画出的位图会被 CSS 拉伸成错形）
    var w = cv.clientWidth;
    var h = cv.clientHeight;
    if (w <= 0 || h <= 0) { return; }
    var dpr = window.devicePixelRatio || 1;
    var pw = Math.floor(w * dpr);
    var ph = Math.floor(h * dpr);
    if (cv.width !== pw || cv.height !== ph) {
        cv.width = pw;
        cv.height = ph;
    }
    var ctx = cv.getContext('2d');
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, w, h);
    var st = resReadStyle();

    // 绘图区——外边距内缩出的矩形
    var plotX = RES_PAD_L;
    var plotY = RES_PAD_T;
    var plotW = Math.max(10, w - RES_PAD_L - RES_PAD_R);
    var plotH = Math.max(10, h - RES_PAD_T - RES_PAD_B);

    // 窗口——winStart 对应左端：数据不足 180 秒时钉在首点（线自左生长），满窗后随最新点滚动
    var pts = resHistory;
    var hasData = pts.length > 0;
    var newestT = hasData ? pts[pts.length - 1].t : 0;
    var winStart = hasData ? Math.max(pts[0].t, newestT - RES_WINDOW_MS) : 0;

    // Y 档——窗口内峰值定档（无数据取最小档，空轴照常画）
    var peak = 0;
    var i;
    for (i = 0; i < pts.length; i = i + 1) {
        var v = Number(pts[i][def.key]) || 0;
        if (v > peak) { peak = v; }
    }
    var axisMax = resAxisMax(peak, def.base);

    // 时间戳 → 画布 x；数值 → 画布 y（0 在底、axisMax 在顶）
    function mapX(t) {
        return plotX + ((t - winStart) / RES_WINDOW_MS) * plotW;
    }
    function mapY(val) {
        var r = axisMax > 0 ? val / axisMax : 0;
        if (r < 0) { r = 0; }
        if (r > 1) { r = 1; }
        return plotY + plotH - r * plotH;
    }

    // [段1] 网格——Y 四等分横线 + X 每 30 秒竖线（边框另行描边，故都只画内线）
    ctx.strokeStyle = st.grid;
    ctx.lineWidth = 1;
    ctx.beginPath();
    for (i = 1; i < 4; i = i + 1) {
        var gy = Math.round(plotY + plotH * i / 4) + 0.5;
        ctx.moveTo(plotX, gy);
        ctx.lineTo(plotX + plotW, gy);
    }
    for (i = 1; i < RES_XSEG; i = i + 1) {
        var gx = Math.round(plotX + plotW * i / RES_XSEG) + 0.5;
        ctx.moveTo(gx, plotY);
        ctx.lineTo(gx, plotY + plotH);
    }
    ctx.stroke();

    // [段2] 曲线——逐点折线（数据不足时只占左段，右侧留白）
    if (pts.length > 0) {
        ctx.strokeStyle = st[def.color];
        ctx.lineWidth = 1.5;
        ctx.beginPath();
        for (i = 0; i < pts.length; i = i + 1) {
            var px = mapX(pts[i].t);
            var py = mapY(Number(pts[i][def.key]) || 0);
            if (i === 0) { ctx.moveTo(px, py); } else { ctx.lineTo(px, py); }
        }
        ctx.stroke();
    }

    // [段3] 灰框——绘图区四边（压在网格与曲线之上，区域边界清晰）
    ctx.strokeStyle = st.frame;
    ctx.lineWidth = 1;
    ctx.strokeRect(plotX + 0.5, plotY + 0.5, plotW - 1, plotH - 1);

    // [段4] 刻度——Y 轴四档（左侧，0 刻度不标）/ X 轴 30 秒一档（下方，0 刻度不标）
    ctx.fillStyle = st.axis;
    ctx.font = st.font;
    ctx.textAlign = 'right';
    ctx.textBaseline = 'middle';
    for (i = 1; i <= 4; i = i + 1) {
        var tv = axisMax * i / 4;
        ctx.fillText(resFmtAxis(tv, def.unit), plotX - 6, mapY(tv));
    }
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';
    for (i = 1; i <= RES_XSEG; i = i + 1) {
        ctx.fillText(resNum((RES_WINDOW_SEC / RES_XSEG) * i) + 's', plotX + plotW * i / RES_XSEG, plotY + plotH + 4);
    }

    // [段5] 峰值 / 当前——绘图区内左上 / 右上
    var last = pts.length > 0 ? (Number(pts[pts.length - 1][def.key]) || 0) : 0;
    ctx.textBaseline = 'top';
    ctx.textAlign = 'left';
    ctx.fillText('峰值 ' + resFmtAxis(peak, def.unit), plotX + 4, plotY + 3);
    ctx.textAlign = 'right';
    ctx.fillText('当前 ' + resFmtAxis(last, def.unit), plotX + plotW - 4, plotY + 3);
}

/**
 * 重绘三张曲线——本体内存 / GPU 占用 / 主循环耗时（固定 180 秒时间轴）
 */
function drawResCharts() {
    for (var i = 0; i < RES_CHARTS.length; i = i + 1) {
        drawResChart(RES_CHARTS[i]);
    }
    if (resInfoEl) {
        resInfoEl.textContent = '资源占用——本体进程 / py 服务 / GPU 显存（每秒采样；曲线 ' + resHistory.length + ' 点 / ' + RES_WINDOW_SEC + ' 秒窗口）';
    }
}

// 暂停按钮——只停曲线追加，数字块照常刷新
if (resPauseBtn) {
    resPauseBtn.addEventListener('click', function () {
        resPaused = !resPaused;
        resPauseBtn.textContent = resPaused ? '继续曲线' : '暂停曲线';
    });
}

// 画布尺寸观察——页签隐藏时画布尺寸为 0（渲染会退化成回落尺寸并被 CSS 拉伸成错形）；
// 尺寸一变（切页签 / 窗口缩放 / 布局变化）立即按真实尺寸重绘——消除「切过去先错一拍」
// window.resize 作为兜底保留（不支持 ResizeObserver 的引擎）
window.addEventListener('resize', function () { drawResCharts(); });
if (window.ResizeObserver) {
    var resObserver = new ResizeObserver(function () { drawResCharts(); });
    for (var rci = 0; rci < RES_CHARTS.length; rci = rci + 1) {
        var rcv = document.getElementById(RES_CHARTS[rci].id);
        if (rcv) { resObserver.observe(rcv); }
    }
}
