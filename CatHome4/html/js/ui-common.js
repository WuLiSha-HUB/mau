// CH4 外观层——ui-common.js：两页面共用前端工具（版本标记 + ui.* 偏好接线）——消除 chat.html/index.html 内联副本
// 加载顺序：本文件先于 app.js / chat-core.js（index.html 由 app.js 段12 复用 applyUiConfig；chat.html 用 loadUiConfig 独立接线）
// 职责：①loadUiVersion——data-version 组件填充（frontend-version.json）②applyUiConfig——ui.* 配置 → CSS 变量
//       ③loadUiConfig——拉 /api/v1/config 并接线（无配置表的页面用；index.html 由 app.js loadConfig 复用同一响应）

function loadUiVersion() {
    // 前端版本自动化——data-version 组件统一填充（js/frontend-version.json——Vitest 每次运行自动递增；两外观层同函数同文件）
    // A362——资产基址：非 console 端口页面由服务端注入 window.__ASSET_BASE__（同源时为空串，行为不变）
    var assetBase = (typeof window !== 'undefined' && window.__ASSET_BASE__) ? window.__ASSET_BASE__ : '';
    fetch(assetBase + 'js/frontend-version.json')
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



// 播报音色候选——前端两处下拉共用（全局配置区 audio.voice / 外观池 TTS 音色）
// 口径：id = 云端 TTS 的 voice_id（DeepSeek TTS 仅两档）；note = 音色特征
// 与后端契约同步：config/schema.json.example 的 audio.voice.values 与此表同集（改一处须改另一处）
var TTS_VOICES = [
    { id: 'stella', note: '海星·女声' },
    { id: 'tide', note: '暗潮·男声' }
];

/**
 * 音色显示名——id 追加音色特征（未知 id 原样返回，不隐藏）
 * @param {string} id 音色 id（如 stella）
 * @returns {string} 显示名（如 stella（海星·女声））
 */
function ttsVoiceLabel(id) {
    var key = String(id || '');
    for (var i = 0; i < TTS_VOICES.length; i++) {
        if (TTS_VOICES[i].id === key) {
            return key + '（' + TTS_VOICES[i].note + '）';
        }
    }
    return key;
}

/**
 * A320 Cat 主题色预设色板——索引 0 = 默认白，>0 = 预设色（VSCode Dark+ 语系，与界面语义色同宗）
 * 消费面：配置弹层色块（panel-catcfg.js）· 群聊气泡与成员球（group.js）——单一定义，禁各处另抄色值
 */
var CAT_THEME_COLORS = [
    { idx: 0, name: '默认白', value: '#ffffff' },
    { idx: 1, name: '蓝', value: '#569cd6' },
    { idx: 2, name: '青', value: '#4ec9b0' },
    { idx: 3, name: '紫', value: '#c586c0' },
    { idx: 4, name: '黄', value: '#dcdcaa' },
    { idx: 5, name: '橙', value: '#ce9178' },
    { idx: 6, name: '绿', value: '#6a9955' },
    { idx: 7, name: '红', value: '#f48771' },
    { idx: 8, name: '浅蓝', value: '#9cdcfe' }
];

/**
 * 主题色索引 → 色值（越界 / 非法一律回落 0 号色——渲染面不因脏值报错）
 * @param {number} idx 色板索引
 * @returns {string} 十六进制色值
 */
function catThemeColorValue(idx) {
    var n = parseInt(idx, 10);
    if (isNaN(n) || n < 0 || n >= CAT_THEME_COLORS.length) { return CAT_THEME_COLORS[0].value; }
    return CAT_THEME_COLORS[n].value;
}
