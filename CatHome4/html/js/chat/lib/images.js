// ═══════════════════════════════════════════
// lib/images.js —— 对话图片附件（包裹解析 + 缩略图组）
// 来源：chat-view.js 第 496-644 行（A65 对话图片附件）· 规格 design-ch4-chat-images.md §四/§六
// 契约：包裹格式（严格门：段头 + ≥1 条目 + 段尾齐备，包裹内部无杂行；不成立 = 整段按普通文本）
//   包裹识别**优先于代码围栏**——围栏内的上述三行照样命中出图，紧邻的围栏行一并吞掉（2026-11-06 莎裁：图片渲染高优先级）
//   [image-open]
//   图片<前文条数>-<批内序号>：<路径>
//   [image-end]
//   <正文>
// 取舍：保留整节（解析 + 路径→URL 四态 + 缩略图）——去掉 chat 前缀；取图端点路径为宿主契约，保留
// ═══════════════════════════════════════════

var IMG_OPEN = '[image-open]';
var IMG_END = '[image-end]';
var IMG_ITEM = /^图片(\d+)-(\d+)：(.+)$/;

function imgSplit(text) {
    // 包裹解析——包裹可位于文本**任意位置**（行粒度；2026-10-06 放宽——原「必须首行」退役）
    //   命中   → { items:[{ref,path}], before, after }（包裹前 / 后文本，各去首尾空行）
    //   不成立 → { items:[], before:原文, after:'' }（整段按普通文本）
    // 严格门保留：段头 + ≥1 条目 + 段尾**齐备**且包裹内部无杂行（空行 / 非条目行 → 此候选不成立，
    // 继续向后找下一处段头；全部候选不成立才整段按普通文本）
    var raw = (text === undefined || text === null) ? '' : String(text);
    var lines = raw.replace(/\r\n/g, '\n').replace(/\r/g, '\n').split('\n');
    // 🔴 图片渲染优先级最高（2026-11-06 莎裁）——三行齐备即命中，**不受代码围栏限制**：
    //   包裹被 ``` 包住时照样出图；围栏行只在这时吞掉（否则残留孤围栏各开空 code 块、后段正文被吞）
    //   围栏状态预扫——inCodeAt[i] = 第 i 行之前是否处于代码围栏内（只用于吞并判定，不参与命中判定）
    var inCodeAt = [];
    var inCode = false;
    for (var f = 0; f < lines.length; f = f + 1) {
        inCodeAt.push(inCode);
        if (lines[f].trim().indexOf('```') === 0) { inCode = !inCode; }
    }
    for (var s = 0; s < lines.length; s = s + 1) {
        if (lines[s].trim() !== IMG_OPEN) { continue; }
        var items = [];
        var i = s + 1;
        var broken = false;
        while (i < lines.length) {
            var t = lines[i].trim();
            if (t === IMG_END) { break; }
            if (t === '') { broken = true; break; }
            var m = IMG_ITEM.exec(t);
            if (!m) { broken = true; break; }
            items.push({ ref: m[1] + '-' + m[2], path: m[3].trim() });
            i = i + 1;
        }
        if (broken || items.length === 0 || i >= lines.length) { continue; }
        // 围栏行吞并——仅当包裹被代码围栏包住时才吞紧邻的那对围栏行；
        // 包裹在围栏外时，相邻的 ``` 属于真正的代码块，必须原样保留
        var fenced = inCodeAt[s] === true;
        var bEnd = s;
        if (fenced && bEnd > 0 && lines[bEnd - 1].trim().indexOf('```') === 0) { bEnd = bEnd - 1; }
        var aStart = i + 1;
        if (fenced && aStart < lines.length && lines[aStart].trim().indexOf('```') === 0) { aStart = aStart + 1; }
        return {
            items: items,
            before: imgTrimBlank(lines.slice(0, bEnd).join('\n')),
            after: imgTrimBlank(lines.slice(aStart).join('\n'))
        };
    }
    return { items: [], before: raw, after: '' };
}

function imgTrimBlank(text) {
    // 首尾空行剥离——包裹前后段的标准化（内部空行保留）
    return String(text).replace(/^\s*\n+/, '').replace(/\n+\s*$/, '').replace(/^\s+$/, '');
}

function imgName(path) {
    // 路径末段——文件名（本地路径与 http(s) URL 通用；查询串剥离）
    var parts = String(path === undefined || path === null ? '' : path).split(/[\\/]/);
    var name = parts.length > 0 ? parts[parts.length - 1] : '';
    if (name.indexOf('?') >= 0) { name = name.split('?')[0]; }
    return name;
}

function imgIsAbsolutePath(path) {
    // 本地绝对路径判定——UNC `\\host\share` / 正斜杠根 `/x` / 盘符 `C:\` 或 `C:/`
    var raw = String(path === undefined || path === null ? '' : path);
    if (raw.length === 0) { return false; }
    if (raw.indexOf('\\\\') === 0) { return true; }
    if (raw.indexOf('/') === 0) { return true; }
    return /^[a-zA-Z]:[\\/]/.test(raw);
}

function imgIsRootAddress(path) {
    // 受控根寻址判定——首个冒号在首个分隔符之前（Windows 盘符 C:\ 不算）；与宿主 RootPathResolver.IsRootAddress 同判据（A257 收口锁定——JS 不能引用程序集，判据须手工同步）
    var raw = String(path === undefined || path === null ? '' : path);
    var colon = raw.indexOf(':');
    if (colon <= 0) { return false; }
    var slash = raw.indexOf('/');
    var back = raw.indexOf('\\');
    var sep = -1;
    if (slash >= 0 && back >= 0) { sep = slash < back ? slash : back; }
    else if (slash >= 0) { sep = slash; }
    else { sep = back; }
    if (sep >= 0 && sep < colon) { return false; }
    if (colon === 1 && sep === 2) { return false; }
    return true;
}

function imgRootUrl(path) {
    // 受控根 URL 段——分隔符统一为 /；首段（含根 id 与冒号）原样，其余逐段编码（保 / 不被转义）
    var raw = String(path === undefined || path === null ? '' : path).trim().replace(/\\/g, '/');
    var parts = raw.split('/');
    var out = [];
    for (var i = 0; i < parts.length; i++) {
        if (i === 0) { out.push(parts[i]); } else { out.push(encodeURIComponent(parts[i])); }
    }
    return out.join('/');
}

function imgUrl(path) {
    // 路径 → 可渲染 URL（单一出口）——四态：
    // ① http(s) 直通；② 受控根寻址 `<rootId>:<rel>` 整串进端点；③ 本地绝对路径整串进端点
    //   （宿主按受控根前缀匹配，不在根内则回落缓存目录文件名）；④ 裸文件名进缓存目录分支
    var raw = String(path === undefined || path === null ? '' : path).trim();
    if (raw.length === 0) { return ''; }
    if (raw.indexOf('http://') === 0 || raw.indexOf('https://') === 0) { return raw; }
    if (imgIsRootAddress(raw) || imgIsAbsolutePath(raw)) {
        return '/api/v1/cache-image/' + imgRootUrl(raw);
    }
    var name = imgName(raw);
    if (name === '') { return ''; }
    return '/api/v1/cache-image/' + encodeURIComponent(name);
}

function imgThumb(item) {
    // 缩略图——角标（包裹条目）/ 文件名（工具输入） + 点击新标签开原图；取图失败原位回落原始路径文本
    var label = item.ref ? ('图片' + item.ref) : imgName(item.path);
    var fig = el('figure', 'chat-img');
    var url = imgUrl(item.path);
    var a = el('a');
    a.href = url;
    a.target = '_blank';
    a.rel = 'noopener';
    var img = el('img');
    img.src = url;
    img.alt = item.ref ? ('图片' + item.ref) : '输入图片';
    img.addEventListener('error', function () {
        fig.className = 'chat-img missing';
        fig.textContent = (label ? (label + '：') : '') + item.path + '（取图失败）';
    });
    a.appendChild(img);
    fig.appendChild(a);
    if (label) {
        fig.appendChild(elText('figcaption', '', label));
    }
    return fig;
}

function imageGroup(items) {
    // 缩略图组——包裹命中时的图片块（各泡共用）
    var wrap = el('div', 'chat-imgs');
    for (var i = 0; i < items.length; i++) {
        wrap.appendChild(imgThumb(items[i]));
    }
    return wrap;
}

function imageGroupFromPaths(paths) {
    // 工具输入图片组——路径列表 → 缩略图组（无编号：非包裹条目，标题走文件名）
    var items = [];
    for (var i = 0; i < paths.length; i++) { items.push({ ref: '', path: paths[i] }); }
    return imageGroup(items);
}
