// CH4 外观层——chat/lib/cmd.js：PowerShell 命令解读器（纯函数，无依赖 · A174 恢复）
// 定位：powershell / powershell7 工具 command 原文硬解码为自然语言意图——工具卡折叠行 + 展开区首块
// 消费：fx/cmd-intent.js（独立功能件 · A176 归位）——工具卡经该件取折叠行简报与展开区 `.cmd-intent`
// 原则：表驱动确定性解析——零 LLM / 零网络 / 零状态；未识别段原样标注（不编造、不静默）
// 加载顺序：lib 段（chat.html 脚本清单）→ fx/cmd-intent.js 运行期调用
// 🔴 cmdUnescapeJson 为 C# TextUtil.JsonUnescape 的前端镜像（同规则，双实现须同步——L1/TOOL-REF §三-B）

// ═══════════════════════════════════════════
// 入口——工具卡消费面
// ═══════════════════════════════════════════

/**
 * 工具参数原文 → 命令解读。
 * @param {string} argsText 工具 arguments（JSON 字符串；实时 payload 可能被宿主截断）
 * @returns {object|null} {brief, detail, truncated}；无法解析返回 null（调用方回退覆盖表 headline / 骨架兜底）
 */
function cmdDecodeTool(argsText) {
    var got = cmdExtractCommand(argsText);
    if (got === null) {
        return null;
    }
    if (got.command.replace(/\s/g, '').length === 0) {
        return null;
    }
    var segs = cmdSplitSegments(got.command);
    if (segs.length === 0) {
        return null;
    }
    var intents = [];
    for (var i = 0; i < segs.length; i = i + 1) {
        intents.push(cmdSegmentIntent(segs[i].text, segs[i].pipe));
    }
    return {
        brief: cmdBrief(intents, got.truncated),
        detail: cmdDetail(intents, got.truncated),
        truncated: got.truncated
    };
}

// ═══════════════════════════════════════════
// 参数面——argsJSON → command 原文
// ═══════════════════════════════════════════

/**
 * 提取 command 字段——完整 JSON 优先，失败回退正则提取（宿主截断 200 字时 JSON 未闭合）。
 * @param {string} argsText 工具参数原文
 * @returns {object|null} {command, truncated}
 */
function cmdExtractCommand(argsText) {
    if (!argsText || argsText.length === 0) {
        return null;
    }
    if (argsText.charAt(0) === '{') {
        try {
            var obj = JSON.parse(argsText);
            if (obj && typeof obj.command === 'string') {
                return { command: obj.command, truncated: false };
            }
            return null;
        } catch (e) {
            // 截断/损坏——走正则兜底
        }
    }
    var m = /"command"\s*:\s*"((?:[^"\\]|\\.)*)/.exec(argsText);
    if (!m) {
        return null;
    }
    return { command: cmdUnescapeJson(m[1]), truncated: true };
}

/**
 * JSON 字符串转义还原（正则兜底路径用）——规则与 C# 侧 Mau.Runtime.TextUtil.JsonUnescape 同源（双实现须同步）：
 * 标准八种转义 + \uXXXX；未识别序列原样保留（不吞反斜杠）。
 * @param {string} s 原始转义片段
 * @returns {string} 还原文本
 */
function cmdUnescapeJson(s) {
    var out = '';
    var i = 0;
    while (i < s.length) {
        var c = s.charAt(i);
        if (c !== '\\' || i + 1 >= s.length) {
            out = out + c;
            i = i + 1;
            continue;
        }
        var n = s.charAt(i + 1);
        if (n === 'n') { out = out + '\n'; i = i + 2; }
        else if (n === 'r') { out = out + '\r'; i = i + 2; }
        else if (n === 't') { out = out + '\t'; i = i + 2; }
        else if (n === 'b') { out = out + '\b'; i = i + 2; }
        else if (n === 'f') { out = out + '\f'; i = i + 2; }
        else if (n === '"') { out = out + '"'; i = i + 2; }
        else if (n === '\\') { out = out + '\\'; i = i + 2; }
        else if (n === '/') { out = out + '/'; i = i + 2; }
        else if (n === 'u' && i + 5 < s.length) {
            var code = parseInt(s.substring(i + 2, i + 6), 16);
            if (isNaN(code)) { out = out + c; i = i + 1; }
            else { out = out + String.fromCharCode(code); i = i + 6; }
        } else {
            out = out + c;
            i = i + 1;
        }
    }
    return out;
}

// ═══════════════════════════════════════════
// 切分——引号感知断句
// ═══════════════════════════════════════════

/**
 * 命令切分——分隔符 ; && || | 换行（引号内不切）。
 * @param {string} cmd 命令全文
 * @returns {array} [{text, pipe}]——pipe=本段由 | 接续而来
 */
function cmdSplitSegments(cmd) {
    var segs = [];
    var buf = '';
    var inSingle = false;
    var inDouble = false;
    var curPipe = false;
    var i = 0;
    while (i < cmd.length) {
        var c = cmd.charAt(i);
        if (inSingle) {
            buf = buf + c;
            if (c === '\'') {
                if (i + 1 < cmd.length && cmd.charAt(i + 1) === '\'') {
                    buf = buf + '\'';
                    i = i + 2;
                    continue;
                }
                inSingle = false;
            }
            i = i + 1;
            continue;
        }
        if (inDouble) {
            buf = buf + c;
            if (c === '`' && i + 1 < cmd.length) {
                buf = buf + cmd.charAt(i + 1);
                i = i + 2;
                continue;
            }
            if (c === '"') {
                inDouble = false;
            }
            i = i + 1;
            continue;
        }
        if (c === '\'') { inSingle = true; buf = buf + c; i = i + 1; continue; }
        if (c === '"') { inDouble = true; buf = buf + c; i = i + 1; continue; }
        var sep = null;
        var step = 1;
        if (c === ';') { sep = ';'; }
        else if (c === '\n' || c === '\r') { sep = 'nl'; }
        else if (c === '&' && i + 1 < cmd.length && cmd.charAt(i + 1) === '&') { sep = 'and'; step = 2; }
        else if (c === '|') {
            if (i + 1 < cmd.length && cmd.charAt(i + 1) === '|') { sep = 'or'; step = 2; }
            else { sep = 'pipe'; }
        }
        if (sep !== null) {
            var t = cmdTrim(buf);
            if (t.length > 0) {
                segs.push({ text: t, pipe: curPipe });
            }
            curPipe = (sep === 'pipe');
            buf = '';
            i = i + step;
            continue;
        }
        buf = buf + c;
        i = i + 1;
    }
    var last = cmdTrim(buf);
    if (last.length > 0) {
        segs.push({ text: last, pipe: curPipe });
    }
    return segs;
}

// ═══════════════════════════════════════════
// 单段解读
// ═══════════════════════════════════════════

/**
 * 单段命令 → 意图。
 * @param {string} segText 段原文
 * @param {boolean} piped 是否由管道接续
 * @returns {object} {known, text, piped}
 */
function cmdSegmentIntent(segText, piped) {
    var suffix = '';
    var body = segText;
    var redir = /\s(>>?)\s*([^>|]*)$/.exec(body);
    if (redir) {
        body = cmdTrim(body.substring(0, redir.index));
        suffix = '（结果写入文件 ' + cmdQuote(cmdTrim(redir[2])) + '）';
    }
    var clean = cmdStripCall(body);
    for (var i = 0; i < CMD_RULES.length; i = i + 1) {
        var m = CMD_RULES[i].re.exec(clean);
        if (m) {
            return {
                known: true,
                tag: cmdRuleTag(CMD_RULES[i], clean, m),
                text: CMD_RULES[i].fmt(clean, m) + suffix,
                piped: piped === true
            };
        }
    }
    return { known: false, tag: cmdTagWord(clean), text: clean + suffix, piped: piped === true };
}

/** 去前导调用运算符 & / 表达式括号 ( 与空白（`(Get-Item x).VersionInfo…` 形态） */
function cmdStripCall(text) {
    var t = cmdTrim(text);
    while (t.length > 0 && (t.charAt(0) === '(' || (t.charAt(0) === '&' && t.charAt(1) !== '&'))) {
        t = cmdTrim(t.substring(1));
    }
    return t;
}

// ═══════════════════════════════════════════
// 折叠行 / 展开区文本
// ═══════════════════════════════════════════

/**
 * 折叠行单行总述。
 * @param {array} intents 段意图数组
 * @param {boolean} truncated 参数是否被截断
 * @returns {string} 单行文本
 */
function cmdBrief(intents, truncated) {
    var unknown = 0;
    for (var i = 0; i < intents.length; i = i + 1) {
        if (!intents[i].known) {
            unknown = unknown + 1;
        }
    }
    var head = intents[0].known ? (intents[0].tag + ' · ' + intents[0].text) : '未识别命令';
    var body = (intents.length > 1) ? (head + ' 等 ' + intents.length + ' 段') : head;
    if (unknown > 0 && intents.length > 1) {
        body = body + '（' + unknown + ' 段未识别）';
    }
    if (truncated) {
        body = body + '（截断）';
    }
    return body;
}

/**
 * 展开区逐段对照文本（纯文本——调用方 textContent 落 DOM）。
 * @param {array} intents 段意图数组
 * @param {boolean} truncated 参数是否被截断
 * @returns {string} 多行文本
 */
function cmdDetail(intents, truncated) {
    var lines = [];
    lines.push('🔎 命令意图（' + intents.length + ' 段）');
    for (var i = 0; i < intents.length; i = i + 1) {
        var it = intents[i];
        var mark = it.piped ? ' ⤷' : '';
        if (it.known) {
            lines.push(' ' + (i + 1) + '.' + mark + ' ' + it.tag + ' · ' + it.text);
        } else {
            lines.push(' ' + (i + 1) + '.' + mark + ' ❓ 未识别：' + it.text);
        }
    }
    if (truncated) {
        lines.push('（参数被宿主截断，仅前 200 字可见；完整命令见落盘历史）');
    }
    return lines.join('\n');
}



// ═══════════════════════════════════════════
// 规则表——命令 → 中文动作（顺序即优先级：专用 CLI 先于通用词）
// ═══════════════════════════════════════════

/** 通用「动词 + 首个目标参数」格式化器（目标取最后一个捕获组——单元格/双组规则统一） */
function cmdVerb(verb) {
    return function (seg, m) {
        return verb + cmdTarget(m[m.length - 1] || '');
    };
}

/** 无目标参数的动词格式化器（管道处理类——参数细节见原文块，避免误读开关值） */
function cmdVerbOnly(verb) {
    return function (seg, m) {
        return verb;
    };
}

/** 首个非开关参数（引号感知）→ 「 xxx」；无则空串 */
function cmdTarget(rest) {
    var args = cmdArgs(rest || '');
    if (args.length === 0) {
        return '';
    }
    return ' ' + cmdQuote(cmdTrunc(args[0], 60));
}

/** 分词——引号感知，剔除开关与连接符 */
function cmdArgs(rest) {
    var out = [];
    var re = /"([^"]*)"|'([^']*)'|(\S+)/g;
    var m;
    while ((m = re.exec(rest)) !== null) {
        var v = (m[1] !== undefined) ? m[1] : ((m[2] !== undefined) ? m[2] : m[3]);
        if (!v || v.length === 0) {
            continue;
        }
        if (v.charAt(0) === '-') {
            continue;
        }
        if (v === '|' || v === '>' || v === '>>' || v === ';' || v === '&&' || v === '||') {
            continue;
        }
        out.push(v);
    }
    return out;
}

var CMD_RULES = [
    // ── 宿主 / 基座 CLI（子指令解码——高价值可见面）──
    // 🔴 段首锚定——命令名只认段首（可带可执行路径 / 引号包裹）；段中出现的同名目录不误命中
    //    （判例：`git -C <…\mau> status` 曾被无锚定的 mau 规则抢走——显示成「Mau 执行 status」）
    { re: /^["']?(?:[^"'\s]*[\/\\])?cathome4\.exe["']?\s*(.*)$/i, fmt: cmdFmtHostExe, tag: cmdTagHostExe },
    { re: /^["']?(?:[^"'\s]*[\/\\])?setup\.exe["']?\s*(.*)$/i, fmt: cmdFmtSetUp, tag: cmdTagSetUp },
    { re: /^["']?(?:[^"'\s]*[\/\\])?mau(?:\.exe)?["']?\s+(\S+)/i, fmt: cmdFmtMauCli, tag: cmdTagMau },
    // ── Godot 工具链（控制台可执行——headless 跑测 / 导入）──
    { re: /^["']?(?:[^"'\s]*[\/\\])?godot[^\s"']*_console(?:\.exe)?["']?\s*(.*)$/i, fmt: cmdFmtGodot, tag: cmdTagGodot },
    // ── 外部工具链（媒体 / 包管理 / 校验 / Electron）──
    { re: /^["']?(?:[^"'\s]*[\/\\])?ffprobe(?:\.exe)?["']?\s*(.*)$/i, fmt: cmdFmtFfprobe, tag: 'ffprobe' },
    { re: /^["']?(?:[^"'\s]*[\/\\])?ffmpeg(?:\.exe)?["']?\s*(.*)$/i, fmt: cmdFmtFfmpeg, tag: 'ffmpeg' },
    { re: /^winget(?:\.exe)?\b\s*(.*)$/i, fmt: cmdFmtWinget, tag: 'winget' },
    { re: /^certutil(?:\.exe)?\b\s*(.*)$/i, fmt: cmdFmtCertutil, tag: 'certutil' },
    { re: /^["']?(?:[^"'\s]*[\/\\])?electron(?:\.exe)?["']?\s*(.*)$/i, fmt: cmdFmtElectron, tag: 'electron' },
    // ── 编译链 / 版本控制 / Node ──
    { re: /^dotnet\s+(build|test|publish|run|restore|clean|pack)\b\s*(.*)$/i, fmt: cmdFmtDotnet, tag: cmdTagDotnet },
    // dotnet 形态兜底——dll 直跑（测试程序集）/ 开关查询（子命令之外的常用形态）
    { re: /^dotnet\s+(.*)$/i, fmt: cmdFmtDotnetOther, tag: cmdTagDotnetOther },
    { re: /^git(\.exe)?\s+(\S+)\s*(.*)$/i, fmt: cmdFmtGit, tag: cmdTagGit },
    { re: /^(npm|npx|node)\b\s*(.*)$/i, fmt: cmdVerb('运行 Node 工具链') },
    // ── 嵌套解释器（cmd /c 与 powershell -Command 的内联命令）──
    { re: /^cmd(?:\.exe)?\s+\/c\s+(.*)$/i, fmt: cmdFmtNested, tag: 'cmd' },
    { re: /^(?:powershell|pwsh)(?:\.exe)?\s+(.*)$/i, fmt: cmdFmtShell, tag: cmdTagShell },
    // ── 文件系统 cmdlet（含常用别名）──
    { re: /^(Get-ChildItem|gci|ls|dir)\b\s*(.*)$/i, fmt: cmdVerb('列出目录') },
    { re: /^(Get-Content|gc|cat|type)\b\s*(.*)$/i, fmt: cmdVerb('读取文件内容') },
    { re: /^(Get-FileHash)\b\s*(.*)$/i, fmt: cmdVerb('计算文件哈希') },
    { re: /^(Get-PSDrive)\b\s*(.*)$/i, fmt: cmdVerbOnly('列出驱动器') },
    { re: /^(Get-Command)\b\s*(.*)$/i, fmt: cmdVerb('查询命令') },
    { re: /^(Select-String|findstr)\b\s*(.*)$/i, fmt: cmdVerb('搜索文件内容') },
    { re: /^(Test-Path)\b\s*(.*)$/i, fmt: cmdVerb('检查路径是否存在') },
    { re: /^where\.exe\b\s*(.*)$/i, fmt: cmdVerb('定位可执行文件') },
    { re: /^(Get-Location)\b\s*(.*)$/i, fmt: cmdVerbOnly('查看当前目录') },
    { re: /^(Get-CimInstance)\b\s*(.*)$/i, fmt: cmdVerb('查询系统信息') },
    { re: /^(Expand-Archive)\b\s*(.*)$/i, fmt: cmdVerb('解压归档文件') },
    { re: /^fc(?:\.exe)?\b\s*(.*)$/i, fmt: cmdVerbOnly('比较文件差异') },
    { re: /^find(?:\.exe)?\b\s*(.*)$/i, fmt: cmdFmtFind },
    { re: /^Resolve-Path\b\s*(.*)$/i, fmt: cmdVerb('解析路径') },
    { re: /^Test-NetConnection\b\s*(.*)$/i, fmt: cmdVerb('测试网络连通性') },
    { re: /^(Get-Item|Get-ItemProperty)\b\s*(.*)$/i, fmt: cmdVerb('查看项属性') },
    { re: /^(Get-Process|gps)\b\s*(.*)$/i, fmt: cmdVerb('查看进程') },
    { re: /^(Stop-Process|taskkill)\b\s*(.*)$/i, fmt: cmdVerb('结束进程') },
    { re: /^(Copy-Item|cp|copy|xcopy|robocopy)\b\s*(.*)$/i, fmt: cmdVerb('复制文件') },
    { re: /^(Move-Item|mv|move)\b\s*(.*)$/i, fmt: cmdVerb('移动文件') },
    { re: /^(Remove-Item|rm|del|erase)\b\s*(.*)$/i, fmt: cmdVerb('删除文件') },
    { re: /^(New-Item|mkdir|md)\b\s*(.*)$/i, fmt: cmdVerb('新建项') },
    { re: /^(Set-Content|Add-Content|Out-File|Clear-Content|Tee-Object)\b\s*(.*)$/i, fmt: cmdVerb('写文件（会被拦截）') },
    // ── 管道处理 / 输出格式化 ──
    { re: /^Select-Object\b\s*(.*)$/i, fmt: cmdVerbOnly('取字段/条数') },
    { re: /^Where-Object\b\s*(.*)$/i, fmt: cmdVerbOnly('按条件筛选') },
    { re: /^Sort-Object\b\s*(.*)$/i, fmt: cmdVerbOnly('排序') },
    { re: /^Group-Object\b\s*(.*)$/i, fmt: cmdVerbOnly('分组计数') },
    { re: /^Measure-Object\b\s*(.*)$/i, fmt: cmdVerbOnly('统计数值') },
    { re: /^ForEach-Object\b\s*(.*)$/i, fmt: cmdVerbOnly('逐项处理') },
    { re: /^(Format-Table|Format-List|Out-String|Out-Host|ConvertTo-Json|ConvertFrom-Json)\b\s*(.*)$/i, fmt: cmdVerbOnly('格式化输出') },
    // ── 网络 / 其他 ──
    { re: /^(curl|Invoke-WebRequest|Invoke-RestMethod|iwr|irm)\b\s*(.*)$/i, fmt: cmdVerb('发起 HTTP 请求') },
    { re: /^(netstat|Get-NetTCPConnection)\b\s*(.*)$/i, fmt: cmdVerb('查看网络连接') },
    { re: /^(Start-Sleep|sleep)\b\s*(.*)$/i, fmt: cmdVerb('等待') },
    { re: /^Get-Date\b\s*(.*)$/i, fmt: cmdVerb('获取当前时间') },
    { re: /^(Set-Location|cd|chdir)\b\s*(.*)$/i, fmt: cmdVerb('切换目录') },
    // ── 通用兜底（收集表核销——专用规则在前，此处只保「不静默未识别」）──
    { re: /^["']?(?:[^"'\s]*[\/\\])?([^"'\s\/\\]+\.(?:cmd|bat|ps1))["']?\s*(.*)$/i, fmt: cmdFmtScript, tag: cmdTagLower },
    { re: /^["']?(?:[^"'\s]*[\/\\])?([^"'\s\/\\]+\.dll)["']?\s*(.*)$/i, fmt: cmdFmtDll, tag: cmdTagLower },
    { re: /^["']?(?:[^"'\s]*[\/\\])?([^"'\s\/\\]+\.exe)["']?\s*(.*)$/i, fmt: cmdFmtExeGeneric, tag: cmdTagLower }
];

// ═══════════════════════════════════════════
// 专用格式化器
// ═══════════════════════════════════════════

/** dotnet 子命令 → 中文动词 + 目标项目 */
function cmdFmtDotnet(seg, m) {
    var act = m[1].toLowerCase();
    var map = {
        build: '编译 C# 项目',
        test: '运行 C# 测试',
        publish: '发布 C# 项目',
        run: '运行 C# 项目',
        restore: '还原 NuGet 依赖',
        clean: '清理 C# 构建产物',
        pack: '打包 NuGet 包'
    };
    return (map[act] || ('dotnet ' + act)) + cmdTarget(m[2] || '');
}

/** dotnet 兜底形态（dll 直跑 / 开关查询）——子命令之外的常用写法 */
function cmdFmtDotnetOther(seg, m) {
    var rest = cmdTrim(m[1] || '');
    var dll = /(?:[^"'\s]*[\/\\])?([^"'\s]+\.dll)\b/i.exec(rest);
    if (dll) {
        return '运行 .NET 程序集 ' + cmdQuote(dll[1]);
    }
    if (/^--version\b/i.test(rest)) {
        return '查看 .NET SDK 版本';
    }
    var sw = /^(--[A-Za-z-]+)\b/.exec(rest);
    if (sw) {
        return '查看 .NET 环境信息（' + sw[1] + '）';
    }
    return 'dotnet ' + cmdTrunc(rest, 50);
}

/** git 命令解析——跳过带值开关（-C <repo> 等）取子命令与其余参数 */
function cmdGitParts(seg) {
    var mm = /^git(?:\.exe)?\s+(?:-[A-Za-z\-]+\s+\S+\s+)*(\S+)\s*(.*)$/i.exec(seg);
    return {
        act: mm ? (mm[1] || '').toLowerCase() : '',
        rest: mm ? (mm[2] || '') : ''
    };
}

/** git 子命令 → 中文动作 */
function cmdFmtGit(seg, m) {
    var p = cmdGitParts(seg);
    var act = p.act;
    var rest = p.rest;
    var map = {
        status: '查看仓库状态',
        log: '查看提交历史',
        diff: '查看改动差异',
        show: '查看提交内容',
        branch: '查看/操作分支',
        checkout: '切换分支',
        commit: '提交改动',
        push: '推送远端',
        pull: '拉取远端',
        fetch: '抓取远端更新',
        add: '暂存改动',
        stash: '暂存工作区',
        clone: '克隆仓库',
        rev_parse: '解析版本引用',
        'rev-parse': '解析版本引用'
    };
    var head = map[act] || ('git ' + act);
    return head + cmdTarget(rest);
}

/** 宿主 CLI——--run 子指令解码（部署/重载类命令可见性关键面） */
function cmdFmtHostExe(seg, m) {
    var rest = m[1] || '';
    var run = /--run\s+["']?([^"']*)/i.exec(rest);
    if (run) {
        return '宿主 CLI 执行指令「' + cmdHostCommand(cmdTrim(run[1])) + '」';
    }
    var tc = /--tool-check\s+(\S+)/i.exec(rest);
    if (tc) {
        return '工具全链自检 ' + cmdQuote(tc[1]);
    }
    var script = /--script\s+(\S+)/i.exec(rest);
    if (script) {
        return '批量指令脚本 ' + cmdQuote(cmdTrunc(script[1], 60));
    }
    if (/--selfcheck/i.test(rest)) {
        return '宿主启动自检';
    }
    if (/--probe-llm/i.test(rest)) {
        return 'LLM 返回体探针';
    }
    if (/--majordomopush/i.test(rest)) {
        return '宿主启动·回执注入';
    }
    return '调用宿主（' + cmdTrunc(cmdTrim(rest), 50) + '）';
}

/** --run 子指令 → 中文（前缀表驱动） */
function cmdHostCommand(cmd) {
    var table = [
        { p: 'reload', v: '热重载 Flow ' },
        { p: 'session count', v: '查询会话数' },
        { p: 'session clear', v: '清空全部会话' },
        { p: 'session.rollback', v: '回滚会话' },
        { p: 'session.fork', v: '分支新会话' },
        { p: 'note.start', v: '启动 Note 计划' },
        { p: 'note.add', v: '推进 Note 步骤' },
        { p: 'freeze', v: '冻结宿主轮转' },
        { p: 'status', v: '查询宿主状态' },
        { p: 'restart', v: '重启（宿主内）' },
        { p: 'kill', v: '紧急停止' },
        { p: 'spawn', v: '创建实体' },
        { p: 'load', v: '加载实体' },
        { p: 'unload', v: '卸载实体' },
        { p: 'params', v: '查询参数' },
        { p: 'param', v: '设置参数' },
        { p: 'boards', v: '查询面板' }
    ];
    var low = cmd.toLowerCase();
    for (var i = 0; i < table.length; i = i + 1) {
        if (low.indexOf(table[i].p) === 0) {
            var rest = cmdTrim(cmd.substring(table[i].p.length));
            if (table[i].v.charAt(table[i].v.length - 1) === ' ') {
                return table[i].v + cmdTrunc(rest, 40);
            }
            return table[i].v + (rest.length > 0 ? '（' + cmdTrunc(rest, 40) + '）' : '');
        }
    }
    return cmdTrunc(cmd, 50);
}

/** Godot 控制台 → 中文动作（版本 / 导入 / headless 运行） */
function cmdFmtGodot(seg, m) {
    var rest = m[1] || '';
    if (/--version\b/i.test(rest)) {
        return '查看 Godot 版本';
    }
    if (/--import\b/i.test(rest)) {
        return '导入 Godot 资源';
    }
    var p = /--path\s+("[^"]*"|'[^']*'|[^\s]+)/i.exec(rest);
    var target = p ? (' ' + cmdQuote(cmdTrim(p[1]).replace(/^["']|["']$/g, ''))) : '';
    return (/--headless\b/i.test(rest) ? 'headless 运行 Godot 项目' : '运行 Godot') + target;
}

/** 剥成对的外层引号——内层命令自带的收尾引号保留（判例：cmd /c dir "…" 曾被误剥） */
function cmdStripPairQuote(text) {
    var t = cmdTrim(text || '');
    var q = t.charAt(0);
    if ((q === '"' || q === '\'') && t.charAt(t.length - 1) === q) {
        return cmdTrim(t.substring(1, t.length - 1));
    }
    return t;
}

/** 原始分词（保留开关值顺序——媒体类取末位输出路径用） */
function cmdTokens(rest) {
    var out = [];
    var re = /"([^"]*)"|'([^']*)'|(\S+)/g;
    var m;
    while ((m = re.exec(rest || '')) !== null) {
        var v = (m[1] !== undefined) ? m[1] : ((m[2] !== undefined) ? m[2] : m[3]);
        if (v.length > 0) {
            out.push(v);
        }
    }
    return out;
}

/** 末位非开关 token（媒体输出目标取用；无则空串） */
function cmdLastTarget(rest) {
    var all = cmdTokens(rest);
    for (var i = all.length - 1; i >= 0; i = i - 1) {
        if (all[i].charAt(0) !== '-') {
            return all[i];
        }
    }
    return '';
}

/** ffmpeg —— 版本 / 媒体处理（输入 -i → 末位输出） */
function cmdFmtFfmpeg(seg, m) {
    var rest = m[1] || '';
    if (/^\s*-version\b/i.test(rest)) {
        return '查看 ffmpeg 版本';
    }
    var src = /(?:^|\s)-i\s+("[^"]*"|'[^']*'|[^\s]+)/i.exec(rest);
    var input = src ? cmdStripPairQuote(src[1]) : '';
    var output = cmdLastTarget(rest);
    if (output === input) {
        output = '';
    }
    if (input.length > 0 && output.length > 0) {
        return '处理媒体 ' + cmdQuote(cmdTrunc(input, 60)) + ' → ' + cmdQuote(cmdTrunc(output, 60));
    }
    if (input.length > 0) {
        return '处理媒体 ' + cmdQuote(cmdTrunc(input, 60));
    }
    return '调用 ffmpeg' + cmdTarget(rest);
}

/** ffprobe —— 版本 / 媒体属性读取（时长特判） */
function cmdFmtFfprobe(seg, m) {
    var rest = m[1] || '';
    if (/^\s*-version\b/i.test(rest)) {
        return '查看 ffprobe 版本';
    }
    var head = /(?:^|\s)-show_entries\s+\S*duration/i.test(rest) ? '读取媒体时长' : '读取媒体信息';
    var tgt = cmdLastTarget(rest);
    return (tgt.length > 0) ? (head + ' ' + cmdQuote(cmdTrunc(tgt, 60))) : head;
}

/** winget —— 子命令（search / install / upgrade / list…）+ 包名 */
function cmdFmtWinget(seg, m) {
    var all = cmdArgs(m[1] || '');
    var act = (all[0] || '').toLowerCase();
    var map = {
        search: '搜索软件包',
        install: '安装软件包',
        uninstall: '卸载软件包',
        upgrade: '升级软件包',
        list: '列出已装软件',
        show: '查看软件包信息',
        source: '管理软件源'
    };
    var head = map[act] || ('winget' + (act.length > 0 ? ' ' + act : ''));
    var tgt = (all.length > 1) ? (' ' + cmdQuote(cmdTrunc(all[1], 60))) : '';
    return head + tgt;
}

/** certutil —— 哈希计算 / 编解码 / 其他 */
function cmdFmtCertutil(seg, m) {
    var rest = m[1] || '';
    var h = /-hashfile\s+("[^"]*"|'[^']*'|[^\s]+)/i.exec(rest);
    if (h) {
        return '计算文件哈希 ' + cmdQuote(cmdTrunc(cmdStripPairQuote(h[1]), 60));
    }
    if (/(?:^|\s)-(?:decode|encode)\b/i.test(rest)) {
        return '编解码证书文件';
    }
    return '调用证书工具（' + cmdTrunc(cmdTrim(rest), 50) + '）';
}

/** Electron —— 版本 / 运行 */
function cmdFmtElectron(seg, m) {
    var rest = cmdTrim(m[1] || '');
    if (/^--version\b/i.test(rest)) {
        return '查看 Electron 版本';
    }
    return '运行 Electron' + cmdTarget(rest);
}

/** DOS find —— /c 统计行数 / 其余查找文本（跳过 /c /v 类开关与引号空串） */
function cmdFmtFind(seg, m) {
    var rest = m[1] || '';
    var head = /(?:^|\s)\/c(?:\s|$)/i.test(rest) ? '统计文件行数' : '在文件中查找文本';
    var all = cmdTokens(rest);
    var tgt = '';
    for (var i = all.length - 1; i >= 0; i = i - 1) {
        if (all[i].charAt(0) !== '-' && all[i].charAt(0) !== '/') {
            tgt = all[i];
            break;
        }
    }
    return head + ((tgt.length > 0) ? (' ' + cmdQuote(cmdTrunc(tgt, 60))) : '');
}

/** 通用可执行 —— 「运行可执行文件 <名>」+ 首个目标参数 */
function cmdFmtExeGeneric(seg, m) {
    return '运行可执行文件 ' + cmdQuote(cmdTrunc(m[1] || '', 60)) + cmdTarget(m[2] || '');
}

/** 程序集直跑 —— 「运行 .NET 程序集 <名>」 */
function cmdFmtDll(seg, m) {
    return '运行 .NET 程序集 ' + cmdQuote(cmdTrunc(m[1] || '', 60));
}

/** 脚本执行（.cmd / .bat / .ps1） */
function cmdFmtScript(seg, m) {
    return '执行脚本 ' + cmdQuote(cmdTrunc(m[1] || '', 60));
}

/** PowerShell 家族 —— -Command 内联嵌套 / -File 脚本 / 其余原样带入 */
function cmdFmtShell(seg, m) {
    var rest = cmdTrim(m[1] || '');
    var c = /(?:^|\s)(?:-command|-c)\s+(.*)$/i.exec(rest);
    if (c) {
        return '嵌套执行「' + cmdTrunc(cmdStripPairQuote(c[1]), 60) + '」';
    }
    var f = /(?:^|\s)-file\s+("[^"]*"|'[^']*'|[^\s]+)/i.exec(rest);
    if (f) {
        return '执行 PowerShell 脚本 ' + cmdQuote(cmdTrunc(cmdStripPairQuote(f[1]), 60));
    }
    return '调用 PowerShell（' + cmdTrunc(rest, 50) + '）';
}

/** PowerShell 家族指令类标识——powershell / pwsh */
function cmdTagShell(seg, m) {
    var w = cmdTagWord(seg);
    return (/^pwsh/i.test(w)) ? 'pwsh' : 'powershell';
}

/** 通用兜底指令类标识——首词小写（与显式 tag 风格一致：ffmpeg / akasha） */
function cmdTagLower(seg) {
    return (cmdTagWord(seg) || '').toLowerCase();
}

/** 嵌套解释器 → 中文动作（cmd /c 与 powershell -Command 的内联命令原样带入） */
function cmdFmtNested(seg, m) {
    var inner = cmdStripPairQuote(m[1] || '');
    return '嵌套执行「' + cmdTrunc(inner, 60) + '」';
}

/** SetUp 模式——relaunch / deploy / prepare（无则空串） */
function cmdSetUpMode(seg) {
    if (/relaunch/i.test(seg || '')) { return 'relaunch'; }
    if (/deploy/i.test(seg || '')) { return 'deploy'; }
    if (/prepare/i.test(seg || '')) { return 'prepare'; }
    return '';
}

/** SetUp 一键链 */
function cmdFmtSetUp(seg, m) {
    var mode = cmdSetUpMode(seg);
    if (mode === 'relaunch') { return '一键部署链·重启接力（relaunch）'; }
    if (mode === 'deploy') { return '一键部署链·发布到目标目录（deploy）'; }
    if (mode === 'prepare') { return '一键部署链·就地自举（prepare）'; }
    return '一键部署链（SetUp）';
}

/** mau 子命令——首个非开关参数（无则空串） */
function cmdMauAct(seg) {
    var all = cmdArgs(seg);
    return (all[1] || '').toLowerCase();
}

/** Mau 基座 CLI */
function cmdFmtMauCli(seg, m) {
    var all = cmdArgs(seg);
    var act = cmdMauAct(seg);
    var map = {
        verify: 'Mau 语料验证',
        gen: 'Mau 语料生成',
        proj: 'Mau 组翻译+编译',
        test: 'Mau 全链门禁测试',
        check: 'Mau 门禁检查',
        bricks: 'Mau 积木索引',
        debug: 'Mau 单步调试'
    };
    var head = map[act] || ('Mau 执行 ' + act);
    var target = (all.length > 2) ? (' ' + cmdQuote(cmdTrunc(all[2], 60))) : '';
    return head + target;
}

// ═══════════════════════════════════════════
// 指令类标识（tag）——折叠行 / 展开区前缀（「git status · 查看仓库状态」）
// 口径：tag = 命令标识（去路径 / 去引号 / 去 .exe）+ 子命令；带子命令的 CLI 由规则显式声明
// ═══════════════════════════════════════════

/** 段首命令名——去路径 / 去引号 / 去 .exe 后缀（规则未声明 tag 时的回落） */
function cmdTagWord(seg) {
    var m = /^("[^"]*"|'[^']*'|[^\s]+)/.exec(cmdTrim(seg || ''));
    if (!m) {
        return '';
    }
    var w = m[1].replace(/^["']|["']$/g, '');
    var i = Math.max(w.lastIndexOf('/'), w.lastIndexOf('\\'));
    if (i >= 0 && i + 1 < w.length) {
        w = w.substring(i + 1);
    }
    if (/\.exe$/i.test(w)) {
        w = w.substring(0, w.length - 4);
    }
    return w;
}

/** 规则 tag 取值——显式声明优先（字符串 / 函数），否则首词派生 */
function cmdRuleTag(rule, seg, m) {
    if (typeof rule.tag === 'function') {
        return rule.tag(seg, m);
    }
    if (typeof rule.tag === 'string' && rule.tag.length > 0) {
        return rule.tag;
    }
    return cmdTagWord(seg);
}

/** git 指令类标识——「git <子命令>」 */
function cmdTagGit(seg) {
    var act = cmdGitParts(seg).act;
    return (act.length > 0) ? ('git ' + act) : 'git';
}

/** dotnet 指令类标识——「dotnet <子命令>」 */
function cmdTagDotnet(seg, m) {
    var act = (m && m[1]) ? m[1].toLowerCase() : '';
    return (act.length > 0) ? ('dotnet ' + act) : 'dotnet';
}

/** dotnet 兜底指令类标识——「dotnet <程序集名 / 开关>」 */
function cmdTagDotnetOther(seg, m) {
    var rest = cmdTrim((m && m[1]) || '');
    var dll = /(?:[^"'\s]*[\/\\])?([^"'\s]+\.dll)\b/i.exec(rest);
    if (dll) {
        return 'dotnet ' + dll[1];
    }
    var sw = /^(--[A-Za-z-]+)\b/.exec(rest);
    if (sw) {
        return 'dotnet ' + sw[1];
    }
    return 'dotnet';
}

/** mau 指令类标识——「mau <子命令>」 */
function cmdTagMau(seg) {
    var act = cmdMauAct(seg);
    return (act.length > 0) ? ('mau ' + act) : 'mau';
}

/** Godot 指令类标识——「godot console」 */
function cmdTagGodot(seg) {
    return 'godot console';
}

/** SetUp 指令类标识——「SetUp <模式>」 */
function cmdTagSetUp(seg) {
    var mode = cmdSetUpMode(seg);
    return (mode.length > 0) ? ('SetUp ' + mode) : 'SetUp';
}

/** 宿主 CLI 指令类标识——「CatHome4 <开关>」 */
function cmdTagHostExe(seg) {
    var m = /--(run|tool-check|script|selfcheck|probe-llm|majordomopush)/i.exec(seg || '');
    return m ? ('CatHome4 --' + m[1].toLowerCase()) : 'CatHome4';
}

// ═══════════════════════════════════════════
// 小工具
// ═══════════════════════════════════════════

/** 去首尾空白 */
function cmdTrim(s) {
    return (s || '').replace(/^\s+|\s+$/g, '');
}

/** 截断 + 省略号 */
function cmdTrunc(s, n) {
    var t = s || '';
    if (t.length <= n) {
        return t;
    }
    return t.substring(0, n) + '…';
}

/** 中文引号包裹 */
function cmdQuote(s) {
    return '「' + (s || '') + '」';
}
