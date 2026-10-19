// ═══════════════════════════════════════════
// lib/tool-format.js —— 工具卡内部渲染（骨架层）
//
// 定位：工具结果 → 分区结构（输入段 / 输出段 + 二级折叠）的通用渲染。
//       骨架只决定分区结构，不猜工具语义；**逐工具的「肉」归声明层**（`chat/tools/<组>/<工具名>.js`，
//       件底 `toolDecl()` 自注册）——本件在运行时取声明（inputLines / outputLines / badge / inputImages / outputImages）。
//
// 骨架面：八类通用（exec / diagnostics / listing / matches / lines / file / json / text）
//         + 两类专属（info / catinfo——逐字段摊平，实现随工具置于 `tools/` 下）
// 声明层：注册口 / 落位表 / 图标 / 中文名 / 折叠行取值口 → `chat/tools/decl.js`；公共取用小件 → `chat/tools/helpers.js`
//
// 来源：chat-tools.js（骨架层 + 阈值小件 + 结构化返回头拆分）→ A167 前端重构重建 → A200 恢复声明层（2026-10-06）
//
// 保留：
//   · 段落小件——seg / segPeekText / segKv / segLines（二级折叠 + 折叠摘要「折叠了 N 行 M 字符」）
//   · 输入段 / 输出段通用外壳（处理中 / 无输出 / 失败三态；声明层覆盖优先）
//   · 八类通用骨架形态 + 形态探测回落（未登记工具：JSON 可解析 → json 骨架，否则 text 骨架）
//   · 结构化返回头拆分（metaHead：首行 JSON 元数据 + 正文定界行）
//
// 不在本件：
//   · 逐工具声明（含折叠行文案 / 图标 / 输入意图行 / 输出自然语言化）→ `chat/tools/**`
//   · 折叠行组装 → `blocks/toolcard.js`；PS 命令解码 → `lib/cmd.js` + `fx/cmd-intent.js`
// ═══════════════════════════════════════════

// ═══ 阈值与小件 ═══

var SEG_FOLD_LINES = 5;      // 段折叠阈值——行数 ≤5 默认展开；>5 折叠为「前 2 + 摘要行 + 后 2」
var SEG_PEEK_HEAD = 2;       // 折叠摘要——保留的首行数
var SEG_PEEK_TAIL = 2;       // 折叠摘要——保留的末行数

function seg(cls, cap, fill, peek) {
    // 段落——details.seg（二级折叠：点折叠头展开全量；限高滚动由 CSS 承担）
    // 🔴 摘要必须挂在 summary 内——details 折叠时非 summary 子元素被浏览器隐藏（peek 放 body 会不可见）
    var det = el('details', 'seg ' + cls);
    var sum = el('summary', 'seg-cap');
    sum.appendChild(document.createTextNode(cap));
    var peekText = segPeekText(peek);
    if (peekText.length > 0) {
        sum.appendChild(elText('span', 'seg-peek', peekText));
    } else {
        det.open = true;   // 内容短——折叠与展开同形，直接展开
    }
    det.appendChild(sum);
    var body = el('div', 'seg-body');
    fill(body);
    det.appendChild(body);
    return det;
}

function segPeekText(text) {
    // 段折叠摘要——行数 ≤5 返回空串（不折叠）；否则「首 2 行 + 折叠提示 + 末 2 行」
    if (typeof text !== 'string' || text.length === 0) { return ''; }
    var lines = text.split('\n');
    if (lines.length <= SEG_FOLD_LINES) { return ''; }
    var head = lines.slice(0, SEG_PEEK_HEAD);
    var tail = lines.slice(lines.length - SEG_PEEK_TAIL);
    var mid = lines.slice(SEG_PEEK_HEAD, lines.length - SEG_PEEK_TAIL);
    return head.join('\n') + '\n… 折叠了 ' + mid.length + ' 行 ' + mid.join('\n').length + ' 字符 …\n' + tail.join('\n');
}

function segBlock(parent, cls, text) {
    // 段内文本块——cls 沿用锚点类（.ta / .tr / .ta.warn）
    var n = elText('div', cls, text || '');
    parent.appendChild(n);
    return n;
}

function segLines(body, lines, cls) {
    // 行列表块——逐行独立 div（等宽 + 缩进保留）
    for (var i = 0; i < lines.length; i++) {
        segBlock(body, cls, lines[i]);
    }
}

function tryJson(text) {
    // JSON 容错解析——空串 / 非对象 → null（调用方回落原文，不静默丢弃）
    var t = (text || '').replace(/^\s+|\s+$/g, '');
    if (t.length === 0 || t.charAt(0) !== '{') { return null; }
    try { return JSON.parse(t); } catch (e) { return null; }
}

function field(obj, key) {
    // 取字符串字段——类型不符返回空串
    if (!obj || typeof obj[key] !== 'string') { return ''; }
    return obj[key];
}

// ═══ 结构化返回头 ═══
// 后端约定：结果首行 JSON（含 tool 字段）+ 其后正文；未结构化结果返回 null（走原文路径）

function metaHead(text) {
    if (typeof text !== 'string' || text.charAt(0) !== '{') { return null; }
    var nl = text.indexOf('\n');
    var first = (nl < 0) ? text : text.substring(0, nl);
    var obj = tryJson(first);
    if (!obj || typeof obj.tool !== 'string') { return null; }
    return { meta: obj, body: (nl < 0) ? '' : text.substring(nl + 1) };
}

function stripLineMark(line) {
    // 行尾行号标注剥离——cs-read 正文行尾 `// L{行号}`；前端已有行号列，显示时剥离
    if (typeof line !== 'string') { return ''; }
    return line.replace(/\s*\/\/ L\d+\s*$/, '');
}

// ═══ 通用判据 ═══

function linesOf(text) {
    // 结果文本行——空串 → 空数组（保持原行内容，不 trim——缩进即语义）
    if (typeof text !== 'string' || text.length === 0) { return []; }
    return text.split('\n');
}

function isErrResult(text) {
    // 失败态——ERR / FAIL 前缀，或结构化返回头 ok:false（单一出口，各骨架不再自加后缀）；ROLLED_BACK 仅兼容历史前文（2026-10-19 起 cs-* 不再产生）
    if (typeof text !== 'string' || text.length === 0) { return false; }
    if (text.indexOf('ERR') === 0 || text.indexOf('ROLLED_BACK') === 0 || text.indexOf('FAIL') === 0) { return true; }
    var head = metaHead(text);
    return !!(head && head.meta.ok === false);
}

// ═══ 输入 / 输出段 ═══

function argObj(tool) {
    // 参数对象——arguments JSON → 对象（解析失败 / 非对象 → 空对象，不抛）
    var o = tryJson(tool ? tool.arguments : null);
    if (!o) { return {}; }
    return o;
}

function argPairs(tool) {
    // 参数键值对——[{k, v}]；值单行化（摘要行不折行的同源处理）
    var o = argObj(tool);
    var keys = Object.keys(o);
    var out = [];
    for (var i = 0; i < keys.length; i++) {
        var v = o[keys[i]];
        if (typeof v !== 'string') { v = JSON.stringify(v); }
        out.push({ k: keys[i], v: String(v).replace(/\r/g, '').replace(/\n/g, ' ') });
    }
    return out;
}

function segKv(body, pairs) {
    // 键值行块——逐行等宽（键走键色、值走值色；长值由 CSS 折行，不截断）
    for (var i = 0; i < pairs.length; i++) {
        var row = el('div', 'seg-kv');
        row.appendChild(elText('span', 'seg-kv-k', pairs[i].k + ': '));
        row.appendChild(elText('span', 'seg-kv-v', pairs[i].v));
        body.appendChild(row);
    }
}

function segInput(tool, cap) {
    // 输入段——声明层 inputLines（逐工具自然语言意图行）优先；未声明则通用键值表
    var ov = (typeof toolDeclOf === 'function') ? toolDeclOf(tool.name) : null;
    var lines = null;
    if (ov && typeof ov.inputLines === 'function') {
        lines = ov.inputLines(argObj(tool)) || [];
    }
    var pairs = lines ? [] : argPairs(tool);
    var peekLines = [];
    var head = cap;
    if (lines) {
        for (var i = 0; i < lines.length; i++) { peekLines.push(lines[i]); }
        if (!head || head.length === 0) { head = '输入 · ' + lines.length + ' 行'; }
    } else {
        for (var j = 0; j < pairs.length; j++) { peekLines.push(pairs[j].k + ': ' + pairs[j].v); }
        if (!head || head.length === 0) { head = '输入' + (pairs.length > 0 ? ' · ' + pairs.length + ' 项' : ''); }
    }
    return seg('seg-in', head, function (body) {
        if (lines) {
            if (lines.length === 0) { segBlock(body, 'ta', '（无参数）'); return; }
            segLines(body, lines, 'seg-line');
            return;
        }
        if (pairs.length === 0) { segBlock(body, 'ta', '（无参数）'); return; }
        segKv(body, pairs);
    }, peekLines.join('\n'));
}

function segImages(tool) {
    // 输入图片段——声明层 inputImages 声明的被处理图片先行（未声明 / 空 → null，调用方跳过）
    var ov = (typeof toolDeclOf === 'function') ? toolDeclOf(tool.name) : null;
    if (!ov || typeof ov.inputImages !== 'function') { return null; }
    if (typeof imageGroupFromPaths !== 'function') { return null; }
    var paths = ov.inputImages(argObj(tool)) || [];
    if (paths.length === 0) { return null; }
    return seg('seg-img', '输入图片 · ' + paths.length + ' 张', function (body) {
        body.appendChild(imageGroupFromPaths(paths));
    }, paths.join('\n'));
}

function segOutput(tool, cap, render, peek) {
    // 输出段通用外壳——处理中 / 无输出 / 失败三态统一；声明层 badge / outputLines 优先
    var ov = (typeof toolDeclOf === 'function') ? toolDeclOf(tool.name) : null;
    var text = tool.result;
    var head = cap || '输出';
    if (ov && typeof ov.badge === 'function' && typeof text === 'string' && text.length > 0) {
        var extra = ov.badge(text, argObj(tool));
        if (typeof extra === 'string' && extra.length > 0) { head = '输出' + extra; }
    }
    if (text === undefined) { head = head + ' · 处理中'; }
    else if (text === '') { head = head + ' · 无输出'; }
    else if (isErrResult(text)) { head = head + ' · 失败'; }
    var peekText = (typeof peek === 'string') ? peek : text;
    var draw = render;
    if (ov && typeof ov.outputLines === 'function') {
        draw = function (body, t, isErr) {
            var ls = ov.outputLines(t, argObj(tool), isErr);
            if (!ls) { render(body, t, isErr); return; }
            segLines(body, ls, isErr ? 'seg-line err' : 'seg-line');
        };
    }
    // 输出图片段——声明层 outputImages（如 browser-shot 截图）→ 缩略图组先行（失败态不出图）
    var outImgs = null;
    if (ov && typeof ov.outputImages === 'function' && typeof text === 'string' && text.length > 0 && !isErrResult(text)) {
        outImgs = ov.outputImages(text, argObj(tool));
    }
    return seg('seg-out', head, function (body) {
        if (outImgs && outImgs.length > 0 && typeof imageGroupFromPaths === 'function') {
            body.appendChild(imageGroupFromPaths(outImgs));
        }
        if (text === undefined) { return; }
        if (text === '') { segBlock(body, 'tr', '（无输出）'); return; }
        draw(body, text, isErrResult(text));
    }, peekText);
}

// ═══ 骨架实现——每类一支通用渲染（壳）═══
// 统一返回 {tag, tagCls, segs}（tag / tagCls 原为双线工具变体标签，覆盖层丢弃后恒为空串）

// ── exec：进程 / 命令执行（输入：命令原文；输出：exit 徽标 + stdout / stderr 分段 + 截断）──
function skelExec(tool) {
    var args = argObj(tool);
    var cmd = field(args, 'command');
    var cwd = field(args, 'cwd');
    var segs = [];

    // [段1] 输入——命令原文（cwd 前置一行）；无 command 键的工具走通用键值表
    if (cmd.length > 0) {
        var inParts = [];
        if (cwd.length > 0) { inParts.push('cwd: ' + cwd); }
        inParts.push(cmd);
        var inText = inParts.join('\n');
        var inCap = '输入 · 命令' + (cwd.length > 0 ? ' · 指定 cwd' : '');
        segs.push(seg('seg-in', inCap, function (body) {
            segBlock(body, 'ta', inText);
        }, inText));
    } else {
        segs.push(segInput(tool, ''));
    }

    // [段2] 输出——exit 徽标 + stdout / stderr 分段（各自规模）+ 截断 / 超时显式
    var head = metaHead(tool.result);
    var r = head ? null : tryJson(tool.result);
    var bodyText = head ? head.body : tool.result;
    var outCap = '输出';
    var warnText = '';
    var blocks = [];
    if (tool.result === undefined) {
        outCap = '输出 · 处理中';
    } else if (head && typeof head.meta.stdoutLines === 'number') {
        // 结构化回执——正文按 stdoutLines / stderrLines 切分（不依赖内容分隔符，零撞车）
        var allLines = linesOf(bodyText);
        var soCount = head.meta.stdoutLines || 0;
        var seCount = head.meta.stderrLines || 0;
        var out = (soCount > 0) ? allLines.slice(0, soCount).join('\n') : '';
        var err = (seCount > 0) ? allLines.slice(soCount, soCount + seCount).join('\n') : '';
        outCap = '输出 · exit ' + head.meta.exit;
        if (out.length > 0) { blocks.push({ cap: 'stdout', cls: 'tr', text: out }); }
        if (err.length > 0) { blocks.push({ cap: 'stderr', cls: 'tr err', text: err }); }
        if (out.length === 0 && err.length === 0) { outCap = outCap + ' · 无输出'; }
        if (head.meta.truncated === true) {
            outCap = outCap + ' · ⚠️ 已截断';
            warnText = '⚠️ 输出已达上限被截断——后续内容未回传' + (head.meta.timeout === true ? '；进程超时已终止' : '');
        }
    } else if (r && typeof r.exit !== 'undefined') {
        var out2 = field(r, 'stdout');
        var err2 = field(r, 'stderr');
        outCap = '输出 · exit ' + r.exit;
        if (out2.length > 0) { blocks.push({ cap: 'stdout', cls: 'tr', text: out2 }); }
        if (err2.length > 0) { blocks.push({ cap: 'stderr', cls: 'tr err', text: err2 }); }
        if (out2.length === 0 && err2.length === 0) { outCap = outCap + ' · 无输出'; }
        if (r.truncated === true) {
            outCap = outCap + ' · ⚠️ 已截断';
            warnText = '⚠️ 输出已达上限被截断——后续内容未回传' + (r.timeout === true ? '；进程超时已终止' : '');
        }
    } else if (tool.result === '') {
        outCap = '输出 · 无输出';
    } else {
        outCap = '输出 · 原始输出';
        blocks.push({ cap: '', cls: (isErrResult(tool.result) ? 'tr err' : 'tr'), text: bodyText });
    }
    var outPeek = bodyText;
    if (r && typeof r.exit !== 'undefined') {
        outPeek = field(r, 'stdout') + '\n' + field(r, 'stderr');
    }
    segs.push(seg('seg-out', outCap, function (body) {
        if (warnText.length > 0) { segBlock(body, 'ta warn', warnText); }
        if (blocks.length === 0) { segBlock(body, 'tr', '（无输出）'); return; }
        for (var i = 0; i < blocks.length; i = i + 1) {
            if (blocks[i].cap.length > 0) {
                var sec = el('div', 'seg-sec');
                sec.appendChild(elText('div', 'seg-sec-cap', blocks[i].cap));
                segBlock(sec, blocks[i].cls, blocks[i].text);
                body.appendChild(sec);
            } else {
                segBlock(body, blocks[i].cls, blocks[i].text);
            }
        }
    }, outPeek));

    // 工具变体标签——声明层 tag / tagCls（如 PowerShell 双线 PS 5.1 / PS 7）
    var ovTag = (typeof toolDeclOf === 'function') ? toolDeclOf(tool.name) : null;
    var tag = (ovTag && typeof ovTag.tag === 'string') ? ovTag.tag : '';
    var tagCls = (ovTag && typeof ovTag.tagCls === 'string') ? ovTag.tagCls : '';
    return { tag: tag, tagCls: tagCls, segs: segs };
}

// ── diagnostics：诊断列表（输出：计数徽标 + 诊断行列表）──
function diagCountsFromMeta(meta) {
    // 结构化头计数——errors / warnings 缺失 → null
    if (!meta) { return null; }
    var e = (typeof meta.errors === 'number') ? meta.errors : -1;
    var w = (typeof meta.warnings === 'number') ? meta.warnings : -1;
    if (e < 0 && w < 0) { return null; }
    return { errors: (e < 0 ? 0 : e), warnings: (w < 0 ? 0 : w) };
}

function diagCounts(text) {
    // 计数来源——JSON 结果取 errors / warnings；纯文本清单退化为行数徽标（解析不出就不标，不猜）
    var o = tryJson(text);
    if (!o) { return null; }
    return diagCountsFromMeta(o);
}

function skelDiagnostics(tool) {
    var segs = [segInput(tool, '')];
    var head = metaHead(tool.result);
    var bodyText = head ? head.body : tool.result;
    var counts = head ? diagCountsFromMeta(head.meta) : diagCounts(tool.result);
    var lines = linesOf(bodyText);
    var cap = '输出';
    if (counts) {
        cap = '输出 · ' + counts.errors + ' 错 ' + counts.warnings + ' 警';
    }
    segs.push(segOutput(tool, cap, function (body, text, isErr) {
        if (lines.length === 0) { segBlock(body, 'tr', '（无诊断输出）'); return; }
        segLines(body, lines, isErr ? 'tr err' : 'tr');
    }, bodyText));
    return { tag: '', tagCls: '', segs: segs };
}

// ── listing：列举（输出：计数 + 条目列表，缩进保留）──
// 计数不含提示行（[git] / [skip] / [截断]）——提示是元信息，不是条目
function skelListing(tool) {
    var segs = [segInput(tool, '')];
    var head = metaHead(tool.result);
    var bodyText = head ? head.body : tool.result;
    var lines = linesOf(bodyText);
    var items = [];
    for (var i = 0; i < lines.length; i++) {
        if (lines[i].replace(/\s/g, '').length === 0) { continue; }
        if (lines[i].indexOf('[git]') === 0 || lines[i].indexOf('[skip]') === 0 || lines[i].indexOf('[截断]') === 0) { continue; }
        items.push(lines[i]);
    }
    var cap = '输出';
    segs.push(segOutput(tool, cap, function (body, text, isErr) {
        if (items.length === 0) { segBlock(body, 'tr', '（无匹配条目）'); return; }
        segLines(body, items, isErr ? 'tr err' : 'seg-line');
    }, bodyText));
    return { tag: '', tagCls: '', segs: segs };
}

// ── matches：检索命中（输出：路径 / 行号 / 上下文 三列）──
// 行形态 `[跨程序集] 路径:行:列: 上下文`（列号可缺；切不出行号的整行作路径，不丢行）
function parseHits(lines) {
    var hits = [];
    for (var i = 0; i < lines.length; i++) {
        var ln = lines[i];
        if (ln.replace(/\s/g, '').length === 0) { continue; }
        var cross = false;
        if (ln.indexOf('[跨程序集]') === 0) {
            cross = true;
            ln = ln.substring(10).replace(/^\s+/, '');
        }
        var m = /^(.+?):(\d+):(\d+):(.*)$/.exec(ln);
        if (m) {
            hits.push({ path: m[1], line: m[2], col: m[3], ctx: m[4], cross: cross });
            continue;
        }
        var m2 = /^(.+?):(\d+):(.*)$/.exec(ln);
        if (m2) {
            hits.push({ path: m2[1], line: m2[2], col: '', ctx: m2[3], cross: cross });
            continue;
        }
        hits.push({ path: ln, line: '', col: '', ctx: '', cross: cross });
    }
    return hits;
}

function skelMatches(tool) {
    var segs = [segInput(tool, '')];
    var head = metaHead(tool.result);
    var bodyText = head ? head.body : tool.result;
    var hits = parseHits(linesOf(bodyText));
    var cap = '输出';
    segs.push(segOutput(tool, cap, function (body, text, isErr) {
        if (hits.length === 0) { segBlock(body, 'tr', '（无命中）'); return; }
        for (var i = 0; i < hits.length; i++) {
            var row = el('div', 'seg-hit' + (isErr ? ' err' : ''));
            if (hits[i].cross) { row.appendChild(elText('span', 'seg-hit-cross', '[跨程序集] ')); }
            row.appendChild(elText('span', 'seg-hit-path', hits[i].path));
            if (hits[i].line.length > 0) {
                row.appendChild(elText('span', 'seg-hit-line',
                    (hits[i].col.length > 0) ? (':' + hits[i].line + ':' + hits[i].col) : (':' + hits[i].line)));
            }
            if (hits[i].ctx.length > 0) {
                row.appendChild(elText('span', 'seg-hit-ctx', ':' + hits[i].ctx));
            }
            body.appendChild(row);
        }
    }, bodyText));
    return { tag: '', tagCls: '', segs: segs };
}

// ── lines：带行号文本（输出：行号列 + 内容）──
// 行形态 `行号: 内容`（统一文件坐标系）；无行号前缀的行整行渲染
function skelLines(tool) {
    var segs = [segInput(tool, '')];
    var head = metaHead(tool.result);
    var bodyText = head ? head.body : tool.result;
    var lines = linesOf(bodyText);
    var startNo = (head && typeof head.meta.start === 'number') ? head.meta.start : -1;
    var cap = '输出';
    segs.push(segOutput(tool, cap, function (body, text, isErr) {
        if (lines.length === 0) { segBlock(body, 'tr', '（无输出）'); return; }
        for (var i = 0; i < lines.length; i++) {
            var row = el('div', 'seg-line' + (isErr ? ' err' : ''));
            if (startNo > 0) {
                // 结构化行号——start + 行序（后端已给准确区间，无需解析行尾 / 行首标注）
                row.appendChild(elText('span', 'seg-line-no', String(startNo + i)));
                row.appendChild(elText('span', 'seg-line-tx', stripLineMark(lines[i])));
            } else {
                var m = /^(\d+): ?(.*)$/.exec(lines[i]);
                if (m) {
                    row.appendChild(elText('span', 'seg-line-no', m[1]));
                    row.appendChild(elText('span', 'seg-line-tx', m[2]));
                } else {
                    row.textContent = lines[i];
                }
            }
            body.appendChild(row);
        }
    }, bodyText));
    return { tag: '', tagCls: '', segs: segs };
}

// ── file / text：正文渲染（共用一支）──
function skelPlain(tool) {
    var segs = [];
    // 被处理的图片先行（声明层 inputImages——如 image-analyze / image-inject）
    var imgSeg = segImages(tool);
    if (imgSeg) { segs.push(imgSeg); }
    segs.push(segInput(tool, ''));
    var head = metaHead(tool.result);
    var bodyText = head ? head.body : tool.result;
    segs.push(segOutput(tool, '输出', function (body, text, isErr) {
        segBlock(body, isErr ? 'tr err' : 'tr', bodyText);
    }, bodyText));
    return { tag: '', tagCls: '', segs: segs };
}

// ── json：通用结构（输出：顶层键值 / 原文回落）──
// 解析失败不静默——原样可见；结构化返回时元数据头作键值表、正文另起文本块
function skelJson(tool) {
    var segs = [segInput(tool, '')];
    var head = metaHead(tool.result);
    var parsed = head ? head.meta : tryJson(tool.result);
    var bodyText = head ? head.body : '';
    var pairs = [];
    if (parsed) {
        var keys = Object.keys(parsed);
        for (var i = 0; i < keys.length; i++) {
            var v = parsed[keys[i]];
            if (typeof v !== 'string') { v = JSON.stringify(v); }
            pairs.push({ k: keys[i], v: String(v) });
        }
    }
    var hasBody = (head !== null) && (bodyText.length > 0);
    var cap = '输出';
    if (!parsed && tool.result !== undefined && tool.result !== '') { cap = '输出 · 原始输出'; }
    segs.push(segOutput(tool, cap, function (body, text, isErr) {
        if (parsed) {
            segKv(body, pairs);
            if (hasBody) { segBlock(body, 'tr', bodyText); }
            return;
        }
        segBlock(body, isErr ? 'tr err' : 'tr', text);
    }, hasBody ? bodyText : tool.result));
    return { tag: '', tagCls: '', segs: segs };
}

// ═══ 分派 ═══

// 骨架 → 渲染函数（八类通用 + info / catinfo 两支专属骨架——专属件随工具置于 tools/ 下）
// 骨架落位表（工具名 → 骨架 id）· 骨架图标 · 中文名 · skeletonOf / iconOf → `chat/tools/decl.js`（声明层单点）
var SKEL_RENDERERS = {
    'exec': skelExec,
    'diagnostics': skelDiagnostics,
    'listing': skelListing,
    'matches': skelMatches,
    'lines': skelLines,
    'file': function (tool) { return skelPlain(tool); },
    'json': skelJson,
    'info': function (tool) { return skelInfo(tool); },
    'catinfo': function (tool) { return skelCatInfo(tool); },
    'text': function (tool) { return skelPlain(tool); }
};

// 骨架图标 / 中文名 / 落位表 / skeletonOf / iconOf —— 已迁至声明层 `chat/tools/decl.js`（单点真相源）。

function probeSkeleton(tool) {
    // 形态探测回落——未登记的工具：可解析 → json 骨架；否则 text 骨架（禁止空白、禁止静默）
    if (tryJson(tool.result)) { return skelJson(tool); }
    return skelPlain(tool);
}

function toolBody(tool) {
    // 工具块内部渲染——三级分派：落位表（壳）→ 骨架通用渲染 → 形态探测回落
    // 返回 {tag, tagCls, segs}——永不返回 null（未登记工具走探测骨架，禁止空白、禁止静默）
    if (!tool || typeof tool.name !== 'string' || tool.name.length === 0) { return null; }
    var skel = (typeof skeletonOf === 'function') ? skeletonOf(tool.name) : '';
    if (skel.length > 0) {
        var fn = SKEL_RENDERERS[skel];
        if (typeof fn === 'function') { return fn(tool); }
    }
    return probeSkeleton(tool);
}
