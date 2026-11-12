// A328——console 口令校验（认证层）：登录层 + 令牌注入 + 业务脚本延迟加载
// 令牌只存本文件的 CONSOLE_TOKEN 变量（不落 localStorage / sessionStorage / cookie）——刷新页面即丢失，须重新输入口令
// 认证通过后才动态加载业务脚本（app.js / panel*.js），未认证时页面只有空壳，不显示任何数据

var CONSOLE_TOKEN = '';
var CONSOLE_AUTH_BUSY = false;
var CONSOLE_SCRIPT_LIST = [
    'js/app.js',
    'js/panel.js',
    'js/panel-group.js',
    'js/panel-apis.js',
    'js/panel-appearance.js',
    'js/panel-catcfg.js',
    'js/panel-packs.js',
    'js/panel-deps.js',
    'js/panel-res.js',
    'js/panel-boot.js',
    'js/panel-db.js'
];

// [段1] 资源版本号——取自本脚本 src 的 ?v=（宿主对 index.html 注入 __V__ 后由浏览器带回）
var ASSET_VERSION = '';
(function () {
    var src = document.currentScript ? document.currentScript.src : '';
    var m = src.match(/[?&]v=([^&]+)/);
    if (m) { ASSET_VERSION = m[1]; }
})();

// [段2] fetch 包装——有令牌则附 Authorization；401 视为会话失效（回登录层）
var CONSOLE_NATIVE_FETCH = window.fetch ? window.fetch.bind(window) : null;
if (CONSOLE_NATIVE_FETCH) {
    window.fetch = function (input, init) {
        var opts = Object.assign({}, init || {});
        if (CONSOLE_TOKEN) {
            var headers = new Headers(opts.headers || {});
            headers.set('Authorization', 'Bearer ' + CONSOLE_TOKEN);
            opts.headers = headers;
        }
        return CONSOLE_NATIVE_FETCH(input, opts).then(function (res) {
            if (res.status === 401 && CONSOLE_TOKEN) {
                CONSOLE_TOKEN = '';
                showAuthOverlay('login', '会话已失效（宿主可能已重启）——请按 F5 刷新页面重新登录');
            }
            return res;
        });
    };
}

// [段3] EventSource 包装——EventSource 不能设请求头，令牌走 query（仅 /api/v1/stream）
var CONSOLE_NATIVE_ES = window.EventSource;
if (CONSOLE_NATIVE_ES) {
    window.EventSource = function (url, config) {
        var target = url;
        if (CONSOLE_TOKEN) {
            target = url + (url.indexOf('?') >= 0 ? '&' : '?') + 'token=' + encodeURIComponent(CONSOLE_TOKEN);
        }
        return new CONSOLE_NATIVE_ES(target, config);
    };
    window.EventSource.prototype = CONSOLE_NATIVE_ES.prototype;
    window.EventSource.CONNECTING = CONSOLE_NATIVE_ES.CONNECTING;
    window.EventSource.OPEN = CONSOLE_NATIVE_ES.OPEN;
    window.EventSource.CLOSED = CONSOLE_NATIVE_ES.CLOSED;
}

// [段4] 登录层——单例遮罩；mode=login（输入口令）/ setup（首次设置，两遍确认）
var AUTH_OVERLAY_ID = 'consoleAuthOverlay';

function buildAuthOverlay(mode, message) {
    var old = document.getElementById(AUTH_OVERLAY_ID);
    if (old) { old.parentNode.removeChild(old); }
    var wrap = document.createElement('div');
    wrap.id = AUTH_OVERLAY_ID;
    wrap.style.cssText = 'position:fixed;inset:0;background:#0d1110;z-index:9000;display:flex;align-items:center;justify-content:center;font-family:inherit';
    var box = document.createElement('div');
    box.style.cssText = 'width:360px;padding:18px 20px;background:#161b1a;border:1px solid #2b3330;border-radius:8px;color:#e6e1d8';
    var title = document.createElement('div');
    title.textContent = 'CatHome4 Console';
    title.style.cssText = 'font-size:15px;font-weight:bold;color:#8fd6a8;margin-bottom:4px';
    var hint = document.createElement('div');
    hint.id = 'consoleAuthHint';
    hint.textContent = message || (mode === 'setup' ? '本机尚未设置 console 口令——请设置（至少 6 位，只存哈希）' : '请输入 console 口令');
    hint.style.cssText = 'font-size:12px;color:#9aa5a0;margin-bottom:10px;line-height:1.5;white-space:pre-wrap';
    var pwd = document.createElement('input');
    pwd.id = 'consoleAuthPwd';
    pwd.type = 'password';
    pwd.placeholder = 'console 口令';
    pwd.autocomplete = 'current-password';
    pwd.style.cssText = 'width:100%;box-sizing:border-box;background:#0d1110;border:1px solid #2b3330;color:#e6e1d8;padding:8px;font-family:inherit;font-size:13px';
    box.appendChild(title);
    box.appendChild(hint);
    box.appendChild(pwd);
    if (mode === 'setup') {
        var pwd2 = document.createElement('input');
        pwd2.id = 'consoleAuthPwd2';
        pwd2.type = 'password';
        pwd2.placeholder = '再输一次';
        pwd2.autocomplete = 'new-password';
        pwd2.style.cssText = 'width:100%;box-sizing:border-box;background:#0d1110;border:1px solid #2b3330;color:#e6e1d8;padding:8px;font-family:inherit;font-size:13px;margin-top:8px';
        box.appendChild(pwd2);
    }
    var btn = document.createElement('button');
    btn.id = 'consoleAuthBtn';
    btn.textContent = mode === 'setup' ? '设置并进入' : '登录';
    btn.style.cssText = 'width:100%;margin-top:12px;background:#2f6b45;border:none;color:#f2f5f3;padding:9px;cursor:pointer;font-family:inherit;font-size:13px';
    box.appendChild(btn);
    wrap.appendChild(box);
    document.body.appendChild(wrap);
    btn.addEventListener('click', function () { submitAuth(mode); });
    pwd.addEventListener('keydown', function (ev) { if (ev.key === 'Enter') { submitAuth(mode); } });
    pwd.focus();
    return wrap;
}

function showAuthOverlay(mode, message) {
    buildAuthOverlay(mode, message);
}

function authSetHint(text) {
    var hint = document.getElementById('consoleAuthHint');
    if (hint) { hint.textContent = text; }
}

// 服务端错误码 → 可读文案（未识别的码原样显示，不让失败变哑）
function authErrText(code) {
    if (!code) { return '口令错误'; }
    if (code === 'CONSOLE_AUTH_REQUIRED') { return '会话已失效——按 F5 刷新页面重新登录后再改口令'; }
    if (code === 'SURFACE_ENDPOINT_ABSENT') { return '该端口未提供此功能（端口面已收缩）'; }
    return code;
}

// [段5] 提交——login / setup；成功后加载业务脚本
function submitAuth(mode) {
    if (CONSOLE_AUTH_BUSY) { return; }
    var pwd = document.getElementById('consoleAuthPwd');
    if (!pwd) { return; }
    var password = pwd.value;
    if (!password) { authSetHint('口令不能为空'); return; }
    if (mode === 'setup') {
        var pwd2 = document.getElementById('consoleAuthPwd2');
        if (pwd2 && pwd2.value !== password) { authSetHint('两次输入不一致'); return; }
        if (password.length < 6) { authSetHint('口令至少 6 位'); return; }
    }
    CONSOLE_AUTH_BUSY = true;
    authSetHint('校验中…');
    var url = mode === 'setup' ? '/api/v1/auth/setup' : '/api/v1/auth/login';
    // 已登录时改口令必须带令牌（服务端对 setup 在已设口令状态下要求认证）
    var reqHeaders = { 'Content-Type': 'application/json' };
    if (CONSOLE_TOKEN) { reqHeaders['Authorization'] = 'Bearer ' + CONSOLE_TOKEN; }
    CONSOLE_NATIVE_FETCH(url, {
        method: 'POST',
        headers: reqHeaders,
        body: JSON.stringify({ password: password })
    }).then(function (res) { return res.json(); }).then(function (d) {
        CONSOLE_AUTH_BUSY = false;
        if (!d.ok || !d.token) {
            authSetHint(authErrText(d.error));
            return;
        }
        CONSOLE_TOKEN = d.token;
        var old = document.getElementById(AUTH_OVERLAY_ID);
        if (old && old.parentNode) { old.parentNode.removeChild(old); }
        loadConsoleScripts();
    }).catch(function (e) {
        CONSOLE_AUTH_BUSY = false;
        authSetHint('请求失败：' + (e && e.message ? e.message : e));
    });
}

// [段6] 业务脚本按序加载——全部到位后调 loadUiVersion（原本紧跟 ui-common.js 的启动调用）
var CONSOLE_SCRIPTS_LOADED = false;
function loadConsoleScripts() {
    if (CONSOLE_SCRIPTS_LOADED) { return; }
    CONSOLE_SCRIPTS_LOADED = true;
    var index = 0;
    function next() {
        if (index >= CONSOLE_SCRIPT_LIST.length) {
            if (typeof loadUiVersion === 'function') { loadUiVersion(); }
            return;
        }
        var el = document.createElement('script');
        var base = CONSOLE_SCRIPT_LIST[index];
        index = index + 1;
        el.src = ASSET_VERSION ? (base + '?v=' + ASSET_VERSION) : base;
        el.onload = next;
        el.onerror = next;
        document.body.appendChild(el);
    }
    next();
}

// [段6b] 改口令入口——配置页「🔑 改 console 口令」按钮（已登录时可改；未登录先要求登录）
(function () {
    var btn = document.getElementById('consolePwdBtn');
    if (!btn) { return; }
    btn.addEventListener('click', function () {
        if (!CONSOLE_TOKEN) { showAuthOverlay('login', '请先登录再修改口令'); return; }
        showAuthOverlay('setup', '修改 console 口令（至少 6 位，改完立即生效；下次刷新页面用新口令登录）');
    });
})();

// [段7] 启动——先问状态：未设置口令 → setup；已设置且未认证 → login；认证面未启用（旧宿主 404）→ 直接进
(function () {
    if (!CONSOLE_NATIVE_FETCH) { return; }
    CONSOLE_NATIVE_FETCH('/api/v1/auth/status', { method: 'GET' })
        .then(function (res) {
            if (!res.ok) { return null; }   // 认证面未启用（旧宿主无该端点）——与升级前行为一致，直接进
            return res.json();
        })
        .then(function (d) {
            if (!d) { loadConsoleScripts(); return; }
            if (d.authenticated) { loadConsoleScripts(); return; }
            if (d.hasPassword) { showAuthOverlay('login', ''); return; }
            showAuthOverlay('setup', '');
        })
        .catch(function (e) {
            showAuthOverlay('login', '无法连接宿主（' + (e && e.message ? e.message : e) + '）——确认宿主在运行后按 F5 重试');
        });
})();
