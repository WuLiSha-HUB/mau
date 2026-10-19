// CH4 外观层——ui-common.js：两页面共用前端工具（版本标记 + ui.* 偏好接线）——消除 chat.html/index.html 内联副本
// 加载顺序：本文件先于 app.js / chat-core.js（index.html 由 app.js 段12 复用 applyUiConfig；chat.html 用 loadUiConfig 独立接线）
// 职责：①loadUiVersion——data-version 组件填充（frontend-version.json）②applyUiConfig——ui.* 配置 → CSS 变量
//       ③loadUiConfig——拉 /api/v1/config 并接线（无配置表的页面用；index.html 由 app.js loadConfig 复用同一响应）

function loadUiVersion() {
    // 前端版本自动化——data-version 组件统一填充（js/frontend-version.json——Vitest 每次运行自动递增；两外观层同函数同文件）
    fetch('js/frontend-version.json')
        .then(function (r) { return r.json(); })
        .then(function (v) {
            var txt = 'UI v' + v.version + ' | ' + (v.time || '');
            var els = document.querySelectorAll('[data-version]');
            for (var i = 0; i < els.length; i = i + 1) { els[i].textContent = txt; }
        })
        .catch(function () { /* 兜底——宿主未运行/文件缺失：保持占位 UI v? 静默降级 */ });
}

/**
 * ui.* 字号缩放配置 → CSS 变量（读配置覆盖 tokens.css 默认；读不到或越界一律按默认 100）
 * @param {Array} items 配置项列表（/api/v1/config 的 items 段；缺失按默认 100 处理）
 */
function applyUiConfig(items) {
    var scale = 100;
    for (var i = 0; i < (items || []).length; i++) {
        if (items[i].key === 'ui.font_scale') { var v = parseInt(items[i].value, 10); if (v >= 50 && v <= 300) { scale = v; } }
    }
    document.documentElement.style.setProperty('--ch-fs-scale', String(scale / 100));
}

function loadUiConfig() {
    // 无配置表页面的独立接线入口（chat.html——对话页无配置区）
    fetch('/api/v1/config')
        .then(function (r) { return r.json(); })
        .then(function (d) { applyUiConfig(d.items || []); })
        .catch(function () { /* 兜底——宿主未运行：保持 CSS 默认字号 */ });
}

/**
 * 文本安全转义（& < >）——聊天内容/Note 任务/待发送队列渲染共用（XSS 与格式双防）
 * 归属调整（2026-09-11）：原 chat-core.js 私有 → 公共（消除 chat-view/chat-note 对 core 的隐式跨文件依赖）
 * @param {string} s 原始文本
 * @returns {string} 转义后文本（可安全拼入 innerHTML 内容位）
 */
function escapeHtml(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

/**
 * 失败可见化——替换空 catch（不再静默吞异常）；console 记录 + 主面板顶部状态位提示（无提示位的页面仅 console）
 * @param {string} scope 失败动作（如 '初始快照'）
 * @param {*} err 错误对象或描述
 */
function uiWarn(scope, err) {
    var msg = '[UI] ' + scope + '失败: ' + ((err && err.message) ? err.message : String(err));
    if (typeof console !== 'undefined' && console.warn) { console.warn(msg); }
    var el = document.getElementById('meta');
    if (el) { el.textContent = msg; }
}

// 计数格式化——统计数字统一入口（A201；A225b 由 chat/lib/format.js 迁入，两页共用）
// 口径：一律两位小数；≥1000 → x.xx K；≥1000000 → x.xx M（M 为最大单位，不再进位）
// 不入此列：离散计数（前文条数 / 工具次数 / 请求次数）——按整数直出
function fmtCount(n) {
    var v = Number(n) || 0;
    if (v >= 1000000) { return (v / 1000000).toFixed(2) + 'M'; }
    if (v >= 1000) { return (v / 1000).toFixed(2) + 'K'; }
    return v.toFixed(2);
}

// 数字分片（2026-10-06 · 莎定；A225b 由 chat/lib/format.js 迁入）——整数与小数独立渲染：小数（含小数点）包 `.num-frac`
// 口径：输入 = 已格式化数字串（`fmtCount` 产物，允许 K/M 单位与 % 等尾缀）；无小数段则原样返回
function fmtNumHtml(s, tailCls) {
    var str = String(s == null ? '' : s);
    var m = /^([0-9]+)(\.[0-9]+)?(.*)$/.exec(str);
    if (m == null) {
        return str;
    }
    var tail = m[3];
    if (tailCls && tail.length > 0) {
        tail = '<span class="' + tailCls + '">' + tail + '</span>';
    }
    if (m[2] === undefined) {
        return m[1] + tail;
    }
    return m[1] + '<i class="num-frac">' + m[2] + '</i>' + tail;
}



// Kokoro 播报音色候选——前端两处下拉共用（全局配置区 audio.voice / 外观池 TTS 音色）
// 口径：id = Kokoro 音色库键（voices-v1.1-zh.bin）；note = 莎的听感标注（小 / 中 / 大 / 巨）
// 与后端契约同步：config/schema.json.example 的 audio.voice.values 与此表同集（改一处须改另一处）
var KOKORO_VOICES = [
    { id: 'zf_038', note: '小' },
    { id: 'zf_072', note: '中' },
    { id: 'zf_059', note: '大' },
    { id: 'zf_022', note: '巨' }
];

/**
 * 音色显示名——id 追加听感注释（未知 id 原样返回，不隐藏）
 * @param {string} id 音色 id（如 zf_072）
 * @returns {string} 显示名（如 zf_072（中））
 */
function kokoroVoiceLabel(id) {
    var key = String(id || '');
    for (var i = 0; i < KOKORO_VOICES.length; i++) {
        if (KOKORO_VOICES[i].id === key) {
            return key + '（' + KOKORO_VOICES[i].note + '）';
        }
    }
    return key;
}
