// CH4 node 服务胶水——stdio 严格协议（design-ch4-node-services §六 · design-ch4-ts-tools §四）
// 契约：stdout 只出协议行（一行 JSON + \n），stderr 出一律日志；首行 ready 事件。
// 铁律：禁止 console.log(obj)（会 inspect 展开破协议）；一切输出走 writeLine()。
// 路径口径：入参一律绝对路径（受控根寻址由 C# 积木层展开后传入，服务侧不做根解析）。

'use strict';

var readline = require('readline');
var fs = require('fs');
var nodePath = require('path');

// 参数解析——--home <dir> 由宿主注入（可选；本服务当前不消费，保留位）
var homeDir = '';
for (var i = 2; i < process.argv.length; i = i + 1)
{
    if (process.argv[i] === '--home' && i + 1 < process.argv.length)
    {
        homeDir = process.argv[i + 1];
        i = i + 1;
    }
}

/**
 * 写一行协议——唯一 stdout 出口
 * @param {object} obj 协议对象
 */
function writeLine(obj)
{
    process.stdout.write(JSON.stringify(obj) + '\n');
}

/**
 * 写一条日志——stderr 独占（不进协议面）
 * @param {string} text 日志文本
 */
function writeLog(text)
{
    process.stderr.write('[ts-server] ' + text + '\n');
}

// 未捕获异常——写 stderr，不污染 stdout
process.on('uncaughtException', function (err)
{
    writeLog('uncaughtException: ' + (err && err.stack ? err.stack : String(err)));
});

process.on('unhandledRejection', function (reason)
{
    writeLog('unhandledRejection: ' + String(reason));
});

// ---------------------------------------------------------------- TypeScript 面

// typescript 惰性加载——未装依赖时 ping / version 仍可用（不连带崩）
var tsLib = null;

/**
 * 加载 typescript 编译器 API（首次调用时）
 * @returns {object} ts 模块
 */
function ensureTs()
{
    if (tsLib === null)
    {
        tsLib = require('typescript');
    }
    return tsLib;
}

// 工程缓存——tsconfig 绝对路径 → 工程上下文
var projects = Object.create(null);

/**
 * 组装服务错误回执（统一形态：ok=false + error 码 + message）
 * @param {string} code 错误码
 * @param {string} message 说明
 * @returns {object} 回执
 */
function fail(code, message)
{
    return { ok: false, error: code, message: message };
}

/**
 * 从给定路径向上找 tsconfig.json
 * @param {string} startPath 起点（文件或目录）
 * @returns {string} tsconfig 绝对路径；未找到空串
 */
function findTsconfigUpward(startPath)
{
    var current = startPath;
    try
    {
        var stat = fs.statSync(current);
        if (!stat.isDirectory())
        {
            current = nodePath.dirname(current);
        }
    }
    catch (err)
    {
        return '';
    }
    for (var depth = 0; depth < 40; depth = depth + 1)
    {
        var candidate = nodePath.join(current, 'tsconfig.json');
        if (fs.existsSync(candidate))
        {
            return candidate;
        }
        var parent = nodePath.dirname(current);
        if (parent === current)
        {
            return '';
        }
        current = parent;
    }
    return '';
}

/**
 * 把 path 参数解析为工程 tsconfig（目录 / tsconfig / 源文件三态）
 * @param {string} input 入参路径
 * @returns {string} tsconfig 绝对路径；无工程口径空串
 */
function resolveTsconfig(input)
{
    if (!input)
    {
        return '';
    }
    var abs = nodePath.resolve(input);
    if (!fs.existsSync(abs))
    {
        return '';
    }
    var lower = abs.toLowerCase();
    if (lower.endsWith('.json'))
    {
        return abs;
    }
    var stat = fs.statSync(abs);
    if (stat.isDirectory())
    {
        var direct = nodePath.join(abs, 'tsconfig.json');
        if (fs.existsSync(direct))
        {
            return direct;
        }
        return findTsconfigUpward(abs);
    }
    return findTsconfigUpward(abs);
}

/**
 * 取（或建立）工程上下文——LanguageService 常驻，文件版本按 mtime+size 变化
 * @param {string} configPath tsconfig 绝对路径
 * @returns {object} 工程上下文
 */
function getProject(configPath)
{
    var key = nodePath.resolve(configPath).toLowerCase();
    var existing = projects[key];
    if (existing)
    {
        return existing;
    }
    var TS = ensureTs();
    var readResult = TS.readConfigFile(configPath, TS.sys.readFile);
    if (readResult.error)
    {
        throw new Error('tsconfig 读取失败：' + TS.flattenDiagnosticMessageText(readResult.error.messageText, ' '));
    }
    var baseDir = nodePath.dirname(configPath);
    var parsed = TS.parseJsonConfigFileContent(readResult.config, TS.sys, baseDir, null, configPath);
    if (parsed.errors && parsed.errors.length > 0)
    {
        var first = parsed.errors[0];
        throw new Error('tsconfig 解析失败：' + TS.flattenDiagnosticMessageText(first.messageText, ' '));
    }
    var fileNames = (parsed.fileNames || []).slice();
    var versions = Object.create(null);
    var host = {
        getScriptFileNames: function () { return fileNames; },
        getScriptVersion: function (fileName)
        {
            // 现算不缓存——缓存版本会让外部写入（ts-patch / 编辑器）对 LanguageService 不可见，
            // 诊断停留在旧 Program 上（判例 2026-10-30：patch 落盘后 ts-check 仍报旧错）
            var v = fileVersion(fileName);
            versions[fileName] = v;
            return v;
        },
        getScriptSnapshot: function (fileName)
        {
            if (!fs.existsSync(fileName))
            {
                return undefined;
            }
            return TS.ScriptSnapshot.fromString(fs.readFileSync(fileName, 'utf8'));
        },
        getCurrentDirectory: function () { return baseDir; },
        getCompilationSettings: function () { return parsed.options; },
        getDefaultLibFileName: function (options) { return TS.getDefaultLibFilePath(options); },
        fileExists: TS.sys.fileExists,
        readFile: TS.sys.readFile,
        readDirectory: TS.sys.readDirectory,
        directoryExists: TS.sys.directoryExists,
        getDirectories: TS.sys.getDirectories,
        useCaseSensitiveFileNames: function () { return TS.sys.useCaseSensitiveFileNames; }
    };
    var service = TS.createLanguageService(host);
    var project = {
        configPath: nodePath.resolve(configPath),
        baseDir: baseDir,
        fileNames: fileNames,
        options: parsed.options,
        service: service,
        versions: versions,
        fileSet: buildFileSet(fileNames)
    };
    projects[key] = project;
    return project;
}

/**
 * 文件版本串（mtime + size）
 * @param {string} fileName 文件绝对路径
 * @returns {string} 版本串
 */
function fileVersion(fileName)
{
    try
    {
        var stat = fs.statSync(fileName);
        return String(stat.mtimeMs) + '-' + String(stat.size);
    }
    catch (err)
    {
        return '0-0';
    }
}

/**
 * 建工程源文件集（小写规范化，用于诊断过滤）
 * @param {string[]} fileNames 源文件清单
 * @returns {object} 集
 */
function buildFileSet(fileNames)
{
    var set = Object.create(null);
    for (var i = 0; i < fileNames.length; i = i + 1)
    {
        set[nodePath.resolve(fileNames[i]).toLowerCase()] = true;
    }
    return set;
}

/**
 * 相对工程目录的显示路径（正斜杠）
 * @param {string} project 工程上下文
 * @param {string} fileName 绝对路径
 * @returns {string} 相对路径
 */
function relPath(project, fileName)
{
    var rel = nodePath.relative(project.baseDir, fileName);
    return rel.split('\\').join('/');
}

/**
 * 诊断转协议项
 * @param {string} project 工程上下文
 * @param {object} diag ts.Diagnostic
 * @returns {object} 诊断项
 */
function diagItem(project, diag)
{
    var TS = ensureTs();
    var file = diag.file ? relPath(project, diag.file.fileName) : '';
    var line = 0;
    var col = 0;
    if (diag.file && typeof diag.start === 'number')
    {
        var lc = diag.file.getLineAndCharacterOfPosition(diag.start);
        line = lc.line + 1;
        col = lc.character + 1;
    }
    return {
        file: file,
        line: line,
        col: col,
        code: diag.code,
        message: TS.flattenDiagnosticMessageText(diag.messageText, ' ')
    };
}

/**
 * 收集工程诊断（语义 + 语法；full 时含建议诊断作 warnings）
 * 程序级一次收全——逐文件查询会让语言服务对每个文件重复做依赖检查，
 * 实测同工程 20 倍放大（A292 压测：b1500 逐文件 35.8s vs tsc 1.8s）
 * @param {object} project 工程上下文
 * @param {boolean} full 是否含警告
 * @returns {object} { errors, warnings }
 */
function collectDiagnostics(project, full)
{
    var errors = [];
    var warnings = [];
    var program = project.service.getProgram();
    var i;
    var syntactic = program.getSyntacticDiagnostics() || [];
    for (i = 0; i < syntactic.length; i = i + 1)
    {
        if (inProjectFileSet(project, syntactic[i]))
        {
            errors.push(diagItem(project, syntactic[i]));
        }
    }
    var semantic = program.getSemanticDiagnostics() || [];
    for (i = 0; i < semantic.length; i = i + 1)
    {
        if (inProjectFileSet(project, semantic[i]))
        {
            errors.push(diagItem(project, semantic[i]));
        }
    }
    if (full)
    {
        for (i = 0; i < project.fileNames.length; i = i + 1)
        {
            var suggestion = project.service.getSuggestionDiagnostics(project.fileNames[i]) || [];
            for (var j = 0; j < suggestion.length; j = j + 1)
            {
                warnings.push(diagItem(project, suggestion[j]));
            }
        }
    }
    return { errors: errors, warnings: warnings };
}

/**
 * 诊断是否落在工程源文件集内（程序级诊断会带上库文件 / 配置面，需按工程面收窄）
 * @param {object} project 工程上下文
 * @param {object} diag ts.Diagnostic
 * @returns {boolean} 属于工程源码即 true
 */
function inProjectFileSet(project, diag)
{
    if (!diag.file)
    {
        return false;
    }
    return project.fileSet[nodePath.resolve(diag.file.fileName).toLowerCase()] === true;
}

/**
 * 取源文件（工程内 / 独立）
 * @param {string} absFile 绝对路径
 * @returns {object} ts.SourceFile；失败 null
 */
function readSourceFile(absFile)
{
    var TS = ensureTs();
    if (!fs.existsSync(absFile))
    {
        return null;
    }
    var text = fs.readFileSync(absFile, 'utf8');
    return TS.createSourceFile(absFile, text, TS.ScriptTarget.Latest, true, TS.ScriptKind.TSX);
}

/**
 * 判定是否模块（顶层有 import / export 修饰）
 * @param {object} sourceFile 源文件
 * @returns {boolean} 模块即 true
 */
function isModuleFile(sourceFile)
{
    var TS = ensureTs();
    var statements = sourceFile.statements || [];
    for (var i = 0; i < statements.length; i = i + 1)
    {
        var node = statements[i];
        if (TS.isImportDeclaration(node) || TS.isImportEqualsDeclaration(node))
        {
            return true;
        }
        var modifiers = TS.canHaveModifiers(node) ? TS.getModifiers(node) : undefined;
        if (modifiers)
        {
            for (var j = 0; j < modifiers.length; j = j + 1)
            {
                var kind = modifiers[j].kind;
                if (kind === TS.SyntaxKind.ExportKeyword || kind === TS.SyntaxKind.DefaultKeyword)
                {
                    return true;
                }
            }
        }
    }
    return false;
}

/**
 * 节点种类名（协议用）
 * @param {object} node 声明节点
 * @returns {string} 种类
 */
function nodeKind(node)
{
    var TS = ensureTs();
    if (TS.isClassDeclaration(node))
    {
        return 'class';
    }
    if (TS.isInterfaceDeclaration(node))
    {
        return 'interface';
    }
    if (TS.isFunctionDeclaration(node))
    {
        return 'function';
    }
    if (TS.isEnumDeclaration(node))
    {
        return 'enum';
    }
    if (TS.isTypeAliasDeclaration(node))
    {
        return 'type';
    }
    if (TS.isVariableStatement(node))
    {
        return 'const';
    }
    if (TS.isModuleDeclaration(node))
    {
        return 'namespace';
    }
    if (TS.isMethodDeclaration(node))
    {
        return 'method';
    }
    if (TS.isPropertyDeclaration(node))
    {
        return 'property';
    }
    if (TS.isPropertySignature(node))
    {
        return 'property';
    }
    if (TS.isConstructorDeclaration(node))
    {
        return 'constructor';
    }
    if (TS.isGetAccessorDeclaration(node) || TS.isSetAccessorDeclaration(node))
    {
        return 'accessor';
    }
    return 'other';
}

/**
 * 节点名文本
 * @param {object} node 声明节点
 * @returns {string} 名称
 */
function nodeName(node)
{
    var TS = ensureTs();
    if (node.name && node.name.text)
    {
        return String(node.name.text);
    }
    if (TS.isConstructorDeclaration(node))
    {
        return 'constructor';
    }
    return '';
}

/**
 * 签名行（源码首行，压成单行）
 * @param {object} node 声明节点
 * @returns {string} 签名
 */
function nodeSignature(node)
{
    var text = node.getText();
    var braceIndex = text.indexOf('{');
    if (braceIndex >= 0)
    {
        text = text.substring(0, braceIndex);
    }
    text = text.replace(/\s+/g, ' ').trim();
    if (text.length > 160)
    {
        text = text.substring(0, 160) + '…';
    }
    return text;
}

/**
 * 收集源文件结构项（一层 + 成员一层）
 * @param {object} sourceFile 源文件
 * @param {string} kindFilter 过滤（all 或不限）
 * @returns {object[]} 结构项
 */
function collectStructure(sourceFile, kindFilter)
{
    var TS = ensureTs();
    var items = [];
    var statements = sourceFile.statements || [];
    for (var i = 0; i < statements.length; i = i + 1)
    {
        var node = statements[i];
        var targets = [];
        if (TS.isVariableStatement(node))
        {
            var declarations = node.declarationList.declarations;
            for (var d = 0; d < declarations.length; d = d + 1)
            {
                targets.push(declarations[d]);
            }
        }
        else
        {
            targets.push(node);
        }
        for (var t = 0; t < targets.length; t = t + 1)
        {
            var target = targets[t];
            var kind = nodeKind(target);
            if (kind === 'other' || kind === 'const' && !TS.isVariableStatement(node) && !TS.isVariableDeclaration(target))
            {
                if (!TS.isVariableDeclaration(target))
                {
                    continue;
                }
            }
            if (kindFilter !== 'all' && kind !== kindFilter)
            {
                continue;
            }
            var lc = sourceFile.getLineAndCharacterOfPosition(target.getStart());
            items.push({
                kind: kind,
                name: nodeName(target),
                signature: nodeSignature(target),
                line: lc.line + 1
            });
            // 类 / 接口成员一层
            if ((TS.isClassDeclaration(target) || TS.isInterfaceDeclaration(target)) && target.members)
            {
                for (var m = 0; m < target.members.length; m = m + 1)
                {
                    var member = target.members[m];
                    var memberKind = nodeKind(member);
                    if (memberKind === 'other')
                    {
                        continue;
                    }
                    if (kindFilter !== 'all' && memberKind !== kindFilter)
                    {
                        continue;
                    }
                    var mlc = sourceFile.getLineAndCharacterOfPosition(member.getStart());
                    items.push({
                        kind: memberKind,
                        name: nodeName(member),
                        signature: nodeSignature(member),
                        line: mlc.line + 1
                    });
                }
            }
        }
    }
    return items;
}

/**
 * 找符号声明节点（顶层优先，其次成员）
 * @param {object} sourceFile 源文件
 * @param {string} symbol 符号名
 * @returns {object[]} 命中节点（按出现顺序）
 */
function findDeclarations(sourceFile, symbol)
{
    var TS = ensureTs();
    var hits = [];
    function visit(node)
    {
        var name = '';
        if (TS.isVariableDeclaration(node) && node.name && node.name.text)
        {
            name = String(node.name.text);
        }
        else if (node.name && node.name.text)
        {
            name = String(node.name.text);
        }
        if (name === symbol)
        {
            hits.push(node);
        }
        TS.forEachChild(node, visit);
    }
    visit(sourceFile);
    return hits;
}

/**
 * 取工程上下文（可选 tsconfig；缺省自文件向上查找）
 * @param {string} fileName 文件绝对路径
 * @param {string} tsconfigInput tsconfig 入参（可空）
 * @returns {object} 工程上下文；无工程 null
 */
function projectForFile(fileName, tsconfigInput)
{
    var configPath = '';
    if (tsconfigInput && tsconfigInput.length > 0)
    {
        configPath = resolveTsconfig(tsconfigInput);
    }
    else
    {
        configPath = findTsconfigUpward(fileName);
    }
    if (!configPath)
    {
        return null;
    }
    try
    {
        return getProject(configPath);
    }
    catch (err)
    {
        writeLog('工程装载失败：' + String(err && err.message ? err.message : err));
        return null;
    }
}

/**
 * 行级差异摘要（旧行 / 新行）
 * @param {string} before 旧文本
 * @param {string} after 新文本
 * @returns {object[]} 差异行
 */
function diffLines(before, after)
{
    var oldLines = before.split('\n');
    var newLines = after.split('\n');
    // [段1] 剥离公共前缀——改动前的上下文不进差异面
    var head = 0;
    while (head < oldLines.length && head < newLines.length && oldLines[head] === newLines[head])
    {
        head = head + 1;
    }
    // [段2] 剥离公共后缀——避免「插入一行导致后续全部标红」的对齐噪音
    var tail = 0;
    while (tail < oldLines.length - head && tail < newLines.length - head
        && oldLines[oldLines.length - 1 - tail] === newLines[newLines.length - 1 - tail])
    {
        tail = tail + 1;
    }
    var oldMid = oldLines.slice(head, oldLines.length - tail);
    var newMid = newLines.slice(head, newLines.length - tail);
    var rows = [];
    var i;
    for (i = 0; i < oldMid.length; i = i + 1)
    {
        rows.push('- ' + oldMid[i]);
    }
    for (i = 0; i < newMid.length; i = i + 1)
    {
        rows.push('+ ' + newMid[i]);
    }
    if (rows.length > 60)
    {
        rows = rows.slice(0, 60);
        rows.push('…（差异行截断——共 ' + String(oldMid.length + newMid.length) + ' 行）');
    }
    var changed = oldMid.length > newMid.length ? oldMid.length : newMid.length;
    return { rows: rows, changed: changed, startLine: head + 1 };
}

var handlers = Object.create(null);

/**
 * stats — 服务自观测（内存 / 工程缓存规模；压测与资源页签诊断用，不改任何状态）
 * @param {object} req 请求
 * @returns {object} 回执
 */
handlers['stats'] = function (req)
{
    var mem = process.memoryUsage();
    var keys = Object.keys(projects);
    var files = 0;
    for (var i = 0; i < keys.length; i = i + 1)
    {
        files = files + projects[keys[i]].fileNames.length;
    }
    return {
        ok: true,
        rss: mem.rss,
        heapUsed: mem.heapUsed,
        heapTotal: mem.heapTotal,
        external: mem.external,
        projects: keys.length,
        projectFiles: files,
        uptimeMs: Math.round(process.uptime() * 1000)
    };
};

/**
 * ts.check — 工程口径类型诊断
 * @param {object} req 请求
 * @returns {object} 回执
 */
handlers['ts.check'] = function (req)
{
    var input = req.path ? String(req.path) : '';
    var full = req.full === true;
    if (!input)
    {
        return fail('TS_BAD_ARGS', '缺少参数 path（tsconfig / 目录 / 单文件）');
    }
    var abs = nodePath.resolve(input);
    if (!fs.existsSync(abs))
    {
        return fail('TS_FILE_NOT_FOUND', '路径不存在：' + input);
    }
    var configPath = resolveTsconfig(abs);
    if (configPath)
    {
        var project = null;
        try
        {
            project = getProject(configPath);
        }
        catch (err)
        {
            return fail('TS_TSCONFIG', String(err && err.message ? err.message : err));
        }
        var diag = collectDiagnostics(project, full);
        return {
            ok: true,
            mode: 'project',
            tsconfig: relPath(project, project.configPath),
            files: project.fileNames.length,
            errors: diag.errors,
            warnings: diag.warnings,
            counts: { errors: diag.errors.length, warnings: diag.warnings.length }
        };
    }
    // 单文件降级——无工程口径（跨文件类型错误不可见）
    var TS = ensureTs();
    var sourceText = fs.readFileSync(abs, 'utf8');
    var program = TS.createProgram([abs], { noEmit: true, target: TS.ScriptTarget.Latest, module: TS.ModuleKind.CommonJS, strict: true });
    var single = TS.getPreEmitDiagnostics(program);
    var errors = [];
    for (var i = 0; i < single.length; i = i + 1)
    {
        var diagRow = single[i];
        var line = 0;
        var col = 0;
        if (diagRow.file && typeof diagRow.start === 'number')
        {
            var lc = diagRow.file.getLineAndCharacterOfPosition(diagRow.start);
            line = lc.line + 1;
            col = lc.character + 1;
        }
        errors.push({
            file: diagRow.file ? nodePath.basename(diagRow.file.fileName) : '',
            line: line,
            col: col,
            code: diagRow.code,
            message: TS.flattenDiagnosticMessageText(diagRow.messageText, ' ')
        });
    }
    return {
        ok: true,
        mode: 'single',
        tsconfig: '',
        files: 1,
        note: '降级为单文件口径——跨文件类型错误不可见（未找到 tsconfig）',
        errors: errors,
        warnings: [],
        counts: { errors: errors.length, warnings: 0 }
    };
};

/**
 * ts.list — 单文件结构清单
 * @param {object} req 请求
 * @returns {object} 回执
 */
handlers['ts.list'] = function (req)
{
    var file = req.file ? String(req.file) : '';
    if (!file)
    {
        return fail('TS_BAD_ARGS', '缺少参数 file');
    }
    var abs = nodePath.resolve(file);
    var sourceFile = readSourceFile(abs);
    if (sourceFile === null)
    {
        return fail('TS_FILE_NOT_FOUND', '文件不存在：' + file);
    }
    var kindFilter = req.kind ? String(req.kind) : 'all';
    var items = collectStructure(sourceFile, kindFilter);
    return {
        ok: true,
        file: abs,
        isModule: isModuleFile(sourceFile),
        kind: kindFilter,
        items: items,
        total: items.length
    };
};

/**
 * ts.read — 符号源码 / 文件概览
 * @param {object} req 请求
 * @returns {object} 回执
 */
handlers['ts.read'] = function (req)
{
    var TS = ensureTs();
    var file = req.file ? String(req.file) : '';
    if (!file)
    {
        return fail('TS_BAD_ARGS', '缺少参数 file');
    }
    var abs = nodePath.resolve(file);
    var sourceFile = readSourceFile(abs);
    if (sourceFile === null)
    {
        return fail('TS_FILE_NOT_FOUND', '文件不存在：' + file);
    }
    var symbol = req.symbol ? String(req.symbol) : '';
    var lines = sourceFile.text.split('\n');
    if (!symbol)
    {
        // 文件概览——导入段 + 全部声明头（带行号）
        var headers = [];
        for (var i = 0; i < lines.length; i = i + 1)
        {
            var text = lines[i];
            if (i < 40 || /\b(export|import|class|interface|function|enum|type|const|let|var|namespace)\b/.test(text))
            {
                headers.push(String(i + 1) + ': ' + text);
            }
        }
        return { ok: true, file: abs, symbol: '', text: headers.join('\n'), range: { start: 1, end: lines.length }, overview: true };
    }
    var hits = findDeclarations(sourceFile, symbol);
    if (hits.length === 0)
    {
        return fail('TS_SYMBOL_NOT_FOUND', '符号不存在：' + symbol);
    }
    var node = hits[0];
    var startPos = node.getStart();
    var startLc = sourceFile.getLineAndCharacterOfPosition(startPos);
    var leadStart = node.getFullStart();
    var leadText = sourceFile.text.substring(leadStart, startPos);
    // 注释归属——紧邻声明前的注释块纳入（含 JSDoc）
    if (leadText.indexOf('*/') >= 0)
    {
        var commentIndex = leadText.lastIndexOf('/**');
        if (commentIndex < 0)
        {
            commentIndex = leadText.lastIndexOf('//');
        }
        if (commentIndex >= 0)
        {
            startPos = leadStart + commentIndex;
            startLc = sourceFile.getLineAndCharacterOfPosition(startPos);
        }
    }
    var startLine = startLc.line;
    var endPos = node.getEnd();
    var endLc = sourceFile.getLineAndCharacterOfPosition(endPos);
    var endLine = endLc.line;
    var body = lines.slice(startLine, endLine + 1);
    var numbered = [];
    for (var j = 0; j < body.length; j = j + 1)
    {
        numbered.push(String(startLine + 1 + j) + ': ' + body[j]);
    }
    return {
        ok: true,
        file: abs,
        symbol: symbol,
        kind: nodeKind(node),
        text: numbered.join('\n'),
        range: { start: startLine + 1, end: endLine + 1 },
        candidates: hits.length
    };
};

/**
 * ts.findRef — 语义级引用查找（全量返回，不预裁剪）
 * @param {object} req 请求
 * @returns {object} 回执
 */
handlers['ts.findRef'] = function (req)
{
    var TS = ensureTs();
    var file = req.file ? String(req.file) : '';
    var symbol = req.symbol ? String(req.symbol) : '';
    if (!file || !symbol)
    {
        return fail('TS_BAD_ARGS', '缺少参数 file / symbol');
    }
    var abs = nodePath.resolve(file);
    var sourceFile = readSourceFile(abs);
    if (sourceFile === null)
    {
        return fail('TS_FILE_NOT_FOUND', '文件不存在：' + file);
    }
    var hits = findDeclarations(sourceFile, symbol);
    if (hits.length === 0)
    {
        return fail('TS_SYMBOL_NOT_FOUND', '符号不存在：' + symbol);
    }
    var project = projectForFile(abs, req.tsconfig ? String(req.tsconfig) : '');
    if (project === null)
    {
        return fail('TS_NO_TSCONFIG', '未找到 tsconfig——引用查找需要工程口径');
    }
    // 位置取「名字」起点——声明节点 getStart() 含装饰器（@ccclass 等），
    // 从那里查引用解析不到符号（A292：Cocos 载具自文件内查类返回 0 引用）
    var anchorNode = hits[0].name ? hits[0].name : hits[0];
    var position = anchorNode.getStart();
    var groups = project.service.findReferences(abs, position) || [];
    var refs = [];
    var seen = Object.create(null);
    for (var i = 0; i < groups.length; i = i + 1)
    {
        var group = groups[i];
        var groupDefs = group.definition ? [group.definition] : [];
        var j;
        for (j = 0; j < groupDefs.length; j = j + 1)
        {
            pushRef(project, refs, seen, groupDefs[j].fileName, groupDefs[j].textSpan.start, 'decl');
        }
        var refList = group.references || [];
        for (j = 0; j < refList.length; j = j + 1)
        {
            var ref = refList[j];
            var kind = 'ref';
            if (ref.isDefinition)
            {
                kind = 'decl';
            }
            else if (ref.isWriteAccess)
            {
                kind = 'write';
            }
            pushRef(project, refs, seen, ref.fileName, ref.textSpan.start, kind);
        }
    }
    return { ok: true, file: abs, symbol: symbol, refs: refs, total: refs.length };
};

/**
 * 追加引用项（去重）
 * @param {object} project 工程上下文
 * @param {object[]} refs 结果集
 * @param {object} seen 去重表
 * @param {string} fileName 文件绝对路径
 * @param {number} position 位置
 * @param {string} kind 种类
 */
function pushRef(project, refs, seen, fileName, position, kind)
{
    var key = fileName + ':' + String(position);
    if (seen[key])
    {
        return;
    }
    seen[key] = true;
    var sourceFile = project.service.getProgram().getSourceFile(fileName);
    var line = 0;
    var col = 0;
    if (sourceFile)
    {
        var lc = sourceFile.getLineAndCharacterOfPosition(position);
        line = lc.line + 1;
        col = lc.character + 1;
    }
    refs.push({ file: relPath(project, fileName), line: line, col: col, kind: kind });
}

/**
 * ts.patch — 改动（符号模式 / 替换模式），回执带 diff + 诊断
 * @param {object} req 请求
 * @returns {object} 回执
 */
handlers['ts.patch'] = function (req)
{
    var file = req.file ? String(req.file) : '';
    if (!file)
    {
        return fail('TS_BAD_ARGS', '缺少参数 file');
    }
    var abs = nodePath.resolve(file);
    if (!fs.existsSync(abs))
    {
        return fail('TS_FILE_NOT_FOUND', '文件不存在：' + file);
    }
    var before = fs.readFileSync(abs, 'utf8');
    var after = before;
    var symbol = req.symbol ? String(req.symbol) : '';
    if (symbol.length > 0)
    {
        var body = req.body ? String(req.body) : '';
        if (!body || body.trim().length === 0)
        {
            // 空 body 一律拒绝——区分「显式删除」与「漏传 / 生成错误」
            return fail('TS_BAD_ARGS', '符号模式下 body 不可为空');
        }
        var TS = ensureTs();
        var sourceFile = readSourceFile(abs);
        if (sourceFile === null)
        {
            return fail('TS_FILE_NOT_FOUND', '文件不存在：' + file);
        }
        var hits = findDeclarations(sourceFile, symbol);
        if (hits.length === 0)
        {
            return fail('TS_SYMBOL_NOT_FOUND', '符号不存在：' + symbol);
        }
        if (hits.length > 1)
        {
            return fail('TS_SYMBOL_AMBIGUOUS', '符号多处定义（' + String(hits.length) + ' 处）——请改用 old/new 模式');
        }
        var node = hits[0];
        var startPos = node.getStart();
        // [段1] 前导注释归属——文档注释属节点之前的 trivia；替换范围须一并覆盖，否则注释双写
        var fullStart = node.getFullStart();
        var trivia = before.substring(fullStart, startPos);
        var commentAt = trivia.lastIndexOf('/**');
        var lineCommentAt = trivia.lastIndexOf('//');
        if (lineCommentAt > commentAt)
        {
            commentAt = lineCommentAt;
        }
        if (commentAt >= 0)
        {
            startPos = fullStart + commentAt;
        }
        // [段2] 尾随换行归一——范围吸收一个行尾换行，body 结尾补齐，避免空行累积
        var endPos = node.getEnd();
        var lineEnding = before.indexOf('\r\n') >= 0 ? '\r\n' : '\n';
        if (before.substr(endPos, 2) === '\r\n')
        {
            endPos = endPos + 2;
        }
        else if (before.substr(endPos, 1) === '\n')
        {
            endPos = endPos + 1;
        }
        if (!/\r?\n$/.test(body))
        {
            body = body + lineEnding;
        }
        after = before.substring(0, startPos) + body + before.substring(endPos);
        if (after === before)
        {
            return fail('TS_PATCH_FAILED', '替换未产生任何变化');
        }
    }
    else
    {
        var oldText = req.old ? String(req.old) : '';
        var newText = req['new'] !== undefined ? String(req['new']) : '';
        if (!oldText)
        {
            return fail('TS_BAD_ARGS', '缺少参数 symbol 或 old');
        }
        if (req['new'] === undefined)
        {
            return fail('TS_BAD_ARGS', '缺少参数 new（删除请显式传空串）');
        }
        var first = before.indexOf(oldText);
        if (first < 0)
        {
            return fail('TS_OLD_NOT_FOUND', 'old 未在文件中出现');
        }
        var second = before.indexOf(oldText, first + oldText.length);
        if (second >= 0)
        {
            return fail('TS_OLD_NOT_UNIQUE', 'old 在文件中出现多处——请扩大上下文使其唯一');
        }
        after = before.substring(0, first) + newText + before.substring(first + oldText.length);
    }
    fs.writeFileSync(abs, after, 'utf8');
    var diff = diffLines(before, after);
    // 诊断随回执（写侧恒落盘；诊断不阻断）
    var diagnostics = [];
    var project = projectForFile(abs, req.tsconfig ? String(req.tsconfig) : '');
    if (project !== null)
    {
        try
        {
            var diag = collectDiagnostics(project, false);
            diagnostics = diag.errors;
        }
        catch (err)
        {
            writeLog('诊断收集失败：' + String(err && err.message ? err.message : err));
        }
    }
    return {
        ok: true,
        file: abs,
        mode: symbol.length > 0 ? 'symbol' : 'replace',
        changedLines: diff.changed,
        diff: diff.rows,
        diagnostics: diagnostics,
        errorCount: diagnostics.length
    };
};

// ---------------------------------------------------------------- 工程质量面（A332 · design-ch4-ts-tools §十）

var childProcess = require('child_process');

// 换行与反斜杠常量——避免正文散落转义字面量
var NEWLINE = '\n';
var BACKSLASH = '\\';

// lint 规则名全集——工具描述里显式列出（禁隐式默认集，各工程结论才可比）
var LINT_RULES = ['any', 'as-unknown-as', 'non-null-assertion', 'ts-ignore', 'ts-expect-error', 'empty-catch', 'console-log', 'undefined-compare'];

/**
 * 枚举值反查名字
 * @param {object} enumObj TS 枚举对象
 * @param {number} value 枚举值
 * @returns {string} 名字；缺失空串
 */
function enumName(enumObj, value)
{
    if (value === undefined || value === null)
    {
        return '';
    }
    var name = enumObj[value];
    return typeof name === 'string' ? name : String(value);
}

/**
 * 相对基准目录的显示路径（正斜杠）
 * @param {string} baseDir 基准目录
 * @param {string} fileName 绝对路径
 * @returns {string} 相对路径
 */
function relName(baseDir, fileName)
{
    var rel = nodePath.relative(baseDir, fileName);
    return rel.split(BACKSLASH).join('/');
}

/**
 * 剥离行尾空白（空格 / 制表符）
 * @param {string} line 单行
 * @returns {string} 剥离后
 */
function trimTrailing(line)
{
    var end = line.length;
    while (end > 0)
    {
        var ch = line.charAt(end - 1);
        if (ch !== ' ' && ch !== '\t')
        {
            break;
        }
        end = end - 1;
    }
    return line.slice(0, end);
}

/**
 * 剥离行首空白
 * @param {string} line 单行
 * @returns {string} 剥离后
 */
function trimLeading(line)
{
    var start = 0;
    while (start < line.length)
    {
        var ch = line.charAt(start);
        if (ch !== ' ' && ch !== '\t')
        {
            break;
        }
        start = start + 1;
    }
    return line.slice(start);
}

/**
 * 取文本行首数字（无数字返回 -1）
 * @param {string} text 文本
 * @returns {number} 数字
 */
function leadingNumber(text)
{
    var digits = '';
    for (var i = 0; i < text.length; i = i + 1)
    {
        var ch = text.charAt(i);
        if (ch >= '0' && ch <= '9')
        {
            digits = digits + ch;
        }
        else
        {
            break;
        }
    }
    if (digits.length === 0)
    {
        return -1;
    }
    return Number(digits);
}

/**
 * 解析 extends 目标（相对路径；包名写法首版不支持——显式出声，不静默跳过）
 * @param {string} fromConfig 引用方 tsconfig 绝对路径
 * @param {string} spec extends 取值
 * @returns {string} 目标绝对路径；无法解析空串
 */
function resolveExtendsPath(fromConfig, spec)
{
    if (!spec || spec.length === 0)
    {
        return '';
    }
    var first = spec.charAt(0);
    var isRelative = (first === '.') || nodePath.isAbsolute(spec);
    if (!isRelative)
    {
        return '';
    }
    var base = nodePath.resolve(nodePath.dirname(fromConfig), spec);
    var candidates = [base, base + '.json', nodePath.join(base, 'tsconfig.json')];
    for (var i = 0; i < candidates.length; i = i + 1)
    {
        try
        {
            if (fs.existsSync(candidates[i]) && fs.statSync(candidates[i]).isFile())
            {
                return candidates[i];
            }
        }
        catch (err)
        {
            // 探测失败按未命中继续下一个候选（不影响结论）
        }
    }
    return '';
}

/**
 * 读 tsconfig extends 链（逐级展开）并汇总 include / exclude
 * @param {object} TS ts 模块
 * @param {string} configPath tsconfig 绝对路径
 * @param {number} depth 当前层号
 * @param {string[]} chain 链收集器
 * @returns {object} {include, exclude, error}
 */
function readTsconfigChain(TS, configPath, depth, chain)
{
    var out = { include: [], exclude: [], error: '' };
    if (depth > 10)
    {
        out.error = 'tsconfig extends 链超过 10 层：' + configPath;
        return out;
    }
    var read = TS.readConfigFile(configPath, TS.sys.readFile);
    if (read.error)
    {
        out.error = 'tsconfig 读取失败：' + TS.flattenDiagnosticMessageText(read.error.messageText, ' ');
        return out;
    }
    chain.push(configPath);
    var conf = read.config || {};
    var parents = conf.extends;
    if (typeof parents === 'string')
    {
        parents = [parents];
    }
    if (parents && parents.length > 0)
    {
        for (var i = 0; i < parents.length; i = i + 1)
        {
            var spec = String(parents[i]);
            var parentPath = resolveExtendsPath(configPath, spec);
            if (!parentPath)
            {
                out.error = 'extends 无法解析：' + spec + '（自 ' + configPath + '；包名写法首版不支持）';
                return out;
            }
            var parentOut = readTsconfigChain(TS, parentPath, depth + 1, chain);
            if (parentOut.error)
            {
                return parentOut;
            }
            if (out.include.length === 0 && parentOut.include.length > 0)
            {
                out.include = parentOut.include.slice();
            }
            if (out.exclude.length === 0 && parentOut.exclude.length > 0)
            {
                out.exclude = parentOut.exclude.slice();
            }
        }
    }
    if (Array.isArray(conf.include) && conf.include.length > 0)
    {
        out.include = conf.include.slice();
    }
    if (Array.isArray(conf.files) && conf.files.length > 0)
    {
        out.include = conf.files.slice();
    }
    if (Array.isArray(conf.exclude) && conf.exclude.length > 0)
    {
        out.exclude = conf.exclude.slice();
    }
    return out;
}

/**
 * ts.info — 工程口径回执：让「这份工程用什么口径编译」在回执里可见
 * @param {object} req 请求
 * @returns {object} 回执
 */
handlers['ts.info'] = function (req)
{
    var input = req.path ? String(req.path) : '';
    if (!input)
    {
        return fail('TS_BAD_ARGS', '缺少参数 path（tsconfig / 目录 / 单文件）');
    }
    var abs = nodePath.resolve(input);
    if (!fs.existsSync(abs))
    {
        return fail('TS_FILE_NOT_FOUND', '路径不存在：' + input);
    }
    var configPath = resolveTsconfig(abs);
    if (!configPath)
    {
        return fail('TS_TSCONFIG', '未找到 tsconfig：' + input);
    }
    var TS = ensureTs();
    var chain = [];
    var merged = readTsconfigChain(TS, configPath, 0, chain);
    if (merged.error.length > 0)
    {
        return fail('TS_TSCONFIG', merged.error);
    }
    var project = null;
    try
    {
        project = getProject(configPath);
    }
    catch (err)
    {
        return fail('TS_TSCONFIG', String(err && err.message ? err.message : err));
    }
    var options = project.options || {};
    var nodeModulesInScope = 0;
    for (var i = 0; i < project.fileNames.length; i = i + 1)
    {
        if (project.fileNames[i].indexOf('node_modules') >= 0)
        {
            nodeModulesInScope = nodeModulesInScope + 1;
        }
    }
    return {
        ok: true,
        tsconfig: nodePath.resolve(configPath),
        extendsChain: chain,
        strict: options.strict === true,
        target: enumName(TS.ScriptTarget, options.target),
        module: enumName(TS.ModuleKind, options.module),
        moduleResolution: enumName(TS.ModuleResolutionKind, options.moduleResolution),
        lib: (options.lib || []).slice(),
        types: (options.types || []).slice(),
        skipLibCheck: options.skipLibCheck === true,
        noImplicitAny: options.noImplicitAny === true,
        strictNullChecks: options.strictNullChecks === true,
        esModuleInterop: options.esModuleInterop === true,
        baseUrl: options.baseUrl ? String(options.baseUrl) : '',
        paths: options.paths || {},
        include: merged.include,
        exclude: merged.exclude,
        files: project.fileNames.length,
        nodeModulesInScope: nodeModulesInScope
    };
};

/**
 * 单文件 lint 扫描——AST 面规则 + 文本面规则（压制指令）
 * @param {object} TS ts 模块
 * @param {string} fileName 文件绝对路径
 * @param {string} baseDir 基准目录
 * @param {object} filter 规则过滤集合
 * @param {object[]} items 命中收集器
 */
function lintFile(TS, fileName, baseDir, filter, items)
{
    var text = '';
    try
    {
        text = fs.readFileSync(fileName, 'utf8');
    }
    catch (err)
    {
        return;
    }
    var rel = relName(baseDir, fileName);
    var lines = text.split(NEWLINE);

    function excerptAt(lineNo)
    {
        if (lineNo < 1 || lineNo > lines.length)
        {
            return '';
        }
        var raw = trimTrailing(trimLeading(lines[lineNo - 1]));
        if (raw.length > 80)
        {
            raw = raw.slice(0, 80) + '…';
        }
        return raw;
    }

    function add(rule, lineNo, note)
    {
        if (filter[rule] !== true)
        {
            return;
        }
        items.push({ file: rel, line: lineNo, rule: rule, note: note, excerpt: excerptAt(lineNo) });
    }

    var sf = TS.createSourceFile(fileName, text, TS.ScriptTarget.Latest, true);

    function lineOf(node)
    {
        var lc = sf.getLineAndCharacterOfPosition(node.getStart(sf));
        return lc.line + 1;
    }

    function visit(node)
    {
        var kind = node.kind;
        if (kind === TS.SyntaxKind.AnyKeyword)
        {
            add('any', lineOf(node), '显式 any');
        }
        if (kind === TS.SyntaxKind.AsExpression && node.expression && node.expression.kind === TS.SyntaxKind.AsExpression)
        {
            var inner = node.expression.type;
            if (inner && inner.kind === TS.SyntaxKind.UnknownKeyword)
            {
                add('as-unknown-as', lineOf(node), 'as unknown as 双断言');
            }
        }
        if (kind === TS.SyntaxKind.NonNullExpression)
        {
            add('non-null-assertion', lineOf(node), '非空断言');
        }
        if (kind === TS.SyntaxKind.CallExpression && node.expression && node.expression.kind === TS.SyntaxKind.PropertyAccessExpression)
        {
            var calleeText = node.expression.getText(sf);
            if (calleeText === 'console.log')
            {
                add('console-log', lineOf(node), '裸 console.log');
            }
        }
        if (kind === TS.SyntaxKind.BinaryExpression && node.operatorToken && node.left && node.right)
        {
            var opKind = node.operatorToken.kind;
            // 只报松散比较——== / != 才做隐式转换（静默面）；显式 === undefined 是防御写法，不属本规则
            var isLoose = opKind === TS.SyntaxKind.EqualsEqualsToken
                || opKind === TS.SyntaxKind.ExclamationEqualsToken;
            if (isLoose)
            {
                var leftText = node.left.getText(sf);
                var rightText = node.right.getText(sf);
                if (leftText === 'undefined' || rightText === 'undefined')
                {
                    add('undefined-compare', lineOf(node), 'undefined 松散比较');
                }
            }
        }
        if (kind === TS.SyntaxKind.TryStatement && node.catchClause && node.catchClause.block)
        {
            var body = node.catchClause.block.statements;
            if (body.length === 0)
            {
                add('empty-catch', lineOf(node.catchClause), '空 catch');
            }
            else if (body.length === 1 && body[0].kind === TS.SyntaxKind.ExpressionStatement && body[0].expression && body[0].expression.kind === TS.SyntaxKind.VoidExpression)
            {
                add('empty-catch', lineOf(node.catchClause), '空 catch（void 吞错）');
            }
        }
        TS.forEachChild(node, visit);
    }

    visit(sf);

    // 文本面——压制指令（注释里的 @ts-ignore / @ts-expect-error）
    for (var i = 0; i < lines.length; i = i + 1)
    {
        if (lines[i].indexOf('@ts-ignore') >= 0)
        {
            add('ts-ignore', i + 1, '@ts-ignore 压制');
        }
        if (lines[i].indexOf('@ts-expect-error') >= 0)
        {
            add('ts-expect-error', i + 1, '@ts-expect-error（仅提示）');
        }
    }
}

/**
 * ts.lint — 松散写法扫描（只做可机检规则，不做风格审美）
 * @param {object} req 请求
 * @returns {object} 回执
 */
handlers['ts.lint'] = function (req)
{
    var input = req.path ? String(req.path) : '';
    if (!input)
    {
        return fail('TS_BAD_ARGS', '缺少参数 path（tsconfig / 目录 / 单文件）');
    }
    var abs = nodePath.resolve(input);
    if (!fs.existsSync(abs))
    {
        return fail('TS_FILE_NOT_FOUND', '路径不存在：' + input);
    }
    var filter = Object.create(null);
    var i;
    if (req.rules !== undefined && req.rules !== null)
    {
        if (Array.isArray(req.rules) === false)
        {
            return fail('TS_BAD_ARGS', 'rules 须为规则名数组');
        }
        for (i = 0; i < req.rules.length; i = i + 1)
        {
            var wanted = String(req.rules[i]);
            if (LINT_RULES.indexOf(wanted) < 0)
            {
                return fail('TS_BAD_RULES', '未知规则：' + wanted + '（可用：' + LINT_RULES.join(' / ') + '）');
            }
            filter[wanted] = true;
        }
    }
    else
    {
        for (i = 0; i < LINT_RULES.length; i = i + 1)
        {
            filter[LINT_RULES[i]] = true;
        }
    }
    var configPath = resolveTsconfig(abs);
    var targets = [];
    var baseDir = '';
    if (configPath)
    {
        var project = null;
        try
        {
            project = getProject(configPath);
        }
        catch (err)
        {
            return fail('TS_TSCONFIG', String(err && err.message ? err.message : err));
        }
        targets = project.fileNames.slice();
        baseDir = project.baseDir;
    }
    else
    {
        var stat = fs.statSync(abs);
        if (stat.isDirectory())
        {
            return fail('TS_TSCONFIG', '未找到 tsconfig：' + input);
        }
        targets = [abs];
        baseDir = nodePath.dirname(abs);
    }
    var TS = ensureTs();
    var items = [];
    for (i = 0; i < targets.length; i = i + 1)
    {
        lintFile(TS, targets[i], baseDir, filter, items);
    }
    var counts = Object.create(null);
    for (i = 0; i < LINT_RULES.length; i = i + 1)
    {
        if (filter[LINT_RULES[i]] === true)
        {
            counts[LINT_RULES[i]] = 0;
        }
    }
    for (i = 0; i < items.length; i = i + 1)
    {
        counts[items[i].rule] = counts[items[i].rule] + 1;
    }
    return {
        ok: true,
        mode: configPath ? 'project' : 'single',
        tsconfig: configPath ? nodePath.resolve(configPath) : '',
        files: targets.length,
        rules: LINT_RULES.filter(function (name) { return filter[name] === true; }),
        counts: counts,
        total: items.length,
        items: items
    };
};

/**
 * 剥离 ANSI 转义序列（跑测输出带色码）
 * @param {string} text 原文
 * @returns {string} 纯文本
 */
function stripAnsi(text)
{
    var out = '';
    var i = 0;
    while (i < text.length)
    {
        if (text.charCodeAt(i) === 27)
        {
            i = i + 1;
            while (i < text.length && text.charAt(i) !== 'm')
            {
                i = i + 1;
            }
            i = i + 1;
            continue;
        }
        out = out + text.charAt(i);
        i = i + 1;
    }
    return out;
}

/**
 * 取末 n 行文本（失败回执的上下文证据）
 * @param {string} text 正文
 * @param {number} n 行数
 * @returns {string} 尾部文本
 */
function tailText(text, n)
{
    var rows = text.split(NEWLINE);
    var start = rows.length > n ? rows.length - n : 0;
    var out = '';
    for (var i = start; i < rows.length; i = i + 1)
    {
        var row = trimTrailing(rows[i]);
        if (row.length === 0)
        {
            continue;
        }
        if (out.length > 0)
        {
            out = out + ' / ';
        }
        out = out + trimLeading(row);
    }
    return out;
}

/**
 * 找摘要行（自尾向前首个含标签的行）
 * @param {string} text 正文
 * @param {string} label 标签（Test Files / Tests）
 * @returns {string} 该行；未命中空串
 */
function findSummaryLine(text, label)
{
    var rows = text.split(NEWLINE);
    for (var i = rows.length - 1; i >= 0; i = i - 1)
    {
        if (rows[i].indexOf(label) >= 0)
        {
            return rows[i];
        }
    }
    return '';
}

/**
 * 解析计数行——形如 `245 passed (245)` / `1 failed | 244 passed (245)`
 * @param {string} line 摘要行
 * @returns {object} {failed, passed, skipped, total}
 */
function parseCounts(line)
{
    var out = { failed: 0, passed: 0, skipped: 0, total: 0 };
    if (!line || line.length === 0)
    {
        return out;
    }
    // 按 `<数字> <词>` 全局抓取——行首带 "Tests" / "Test Files" 前缀，逐个数字起点匹配才不会漏（实测根因）
    var re = /(\d+)\s+(failed|passed|skipped|todo)/g;
    var m = re.exec(line);
    while (m !== null)
    {
        var num = Number(m[1]);
        var word = m[2];
        if (word === 'failed')
        {
            out.failed = num;
        }
        else if (word === 'passed')
        {
            out.passed = num;
        }
        else
        {
            out.skipped = out.skipped + num;
        }
        m = re.exec(line);
    }
    var open = line.indexOf('(');
    var close = line.indexOf(')', open + 1);
    if (open >= 0 && close > open)
    {
        var inner = leadingNumber(trimLeading(line.slice(open + 1, close)));
        if (inner >= 0)
        {
            out.total = inner;
        }
    }
    if (out.total === 0)
    {
        out.total = out.failed + out.passed + out.skipped;
    }
    return out;
}

/**
 * 探测工程运行器——只认宿主已配置的 vitest / node:test，不替工程决定测试框架
 * @param {string} rootDir 工程根
 * @returns {object} {ok, kind, exe, args, message}
 */
function detectRunner(rootDir)
{
    var vitestMjs = nodePath.join(rootDir, 'node_modules', 'vitest', 'vitest.mjs');
    if (fs.existsSync(vitestMjs))
    {
        return { ok: true, kind: 'vitest', exe: process.execPath, args: [vitestMjs, 'run'], message: '' };
    }
    var pkgPath = nodePath.join(rootDir, 'package.json');
    if (fs.existsSync(pkgPath))
    {
        var pkg = null;
        try
        {
            pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));
        }
        catch (err)
        {
            return { ok: false, kind: '', exe: '', args: [], message: 'package.json 解析失败：' + String(err && err.message ? err.message : err) };
        }
        var testScript = pkg && pkg.scripts && pkg.scripts.test ? String(pkg.scripts.test) : '';
        if (testScript.indexOf('node --test') >= 0)
        {
            return { ok: true, kind: 'node-test', exe: process.execPath, args: ['--test'], message: '' };
        }
        if (testScript.length > 0)
        {
            return { ok: false, kind: '', exe: '', args: [], message: '工程 test 脚本不是 vitest / node:test（本工具不替工程决定测试框架）：' + testScript };
        }
    }
    return { ok: false, kind: '', exe: '', args: [], message: '未找到受支持运行器（node_modules/vitest/vitest.mjs 或 package.json test 脚本含 node --test）' };
}

/**
 * ts.test — 跑测窗口：逐项计数 + 失败用例 + 剥 ANSI；同步阻塞（与服务串行模型一致）
 * @param {object} req 请求
 * @returns {object} 回执
 */
handlers['ts.test'] = function (req)
{
    var input = req.path ? String(req.path) : '';
    if (!input)
    {
        return fail('TS_BAD_ARGS', '缺少参数 path（工程目录 / tsconfig / 测试文件）');
    }
    var abs = nodePath.resolve(input);
    if (!fs.existsSync(abs))
    {
        return fail('TS_FILE_NOT_FOUND', '路径不存在：' + input);
    }
    var configPath = resolveTsconfig(abs);
    var stat = fs.statSync(abs);
    var rootDir = '';
    if (configPath)
    {
        rootDir = nodePath.dirname(configPath);
    }
    else if (stat.isDirectory())
    {
        rootDir = abs;
    }
    else
    {
        rootDir = nodePath.dirname(abs);
    }
    var runner = detectRunner(rootDir);
    if (runner.ok !== true)
    {
        return fail('TS_TEST_RUNNER', runner.message);
    }
    var args = runner.args.slice();
    if (runner.kind === 'vitest')
    {
        args.push('--root', rootDir);
        if (req.filter)
        {
            args.push('-t', String(req.filter));
        }
    }
    var startedMs = Date.now();
    var res = childProcess.spawnSync(runner.exe, args, { cwd: rootDir, encoding: 'utf8', timeout: 115000, windowsHide: true, maxBuffer: 67108864 });
    var raw = '';
    if (res.stdout)
    {
        raw = raw + res.stdout;
    }
    if (res.stderr)
    {
        raw = raw + NEWLINE + res.stderr;
    }
    if (res.error)
    {
        return fail('TS_TEST_FAILED', '跑测未启动或超时：' + String(res.error.message ? res.error.message : res.error));
    }
    var clean = stripAnsi(raw);
    var caseRow = findSummaryLine(clean, 'Tests');
    var cases = parseCounts(caseRow);
    if (cases.total <= 0)
    {
        return fail('TS_TEST_NO_COUNT', '回执无用例计数（退出码 0 且零输出不构成证据）｜运行器 ' + runner.kind + '｜尾部：' + tailText(clean, 6));
    }
    // 全跳过 = 什么都没跑（实测：filter 无匹配时 vitest 报 245 skipped、退出码 0）——零执行不构成通过
    if (cases.passed + cases.failed === 0)
    {
        return fail('TS_TEST_NO_COUNT', '无实际执行用例（通过 0 / 失败 0 / 跳过 ' + String(cases.skipped) + '）——filter 可能无匹配｜运行器 ' + runner.kind);
    }
    var failures = [];
    var rows = clean.split(NEWLINE);
    for (var i = 0; i < rows.length; i = i + 1)
    {
        var row = trimTrailing(rows[i]);
        if (row.indexOf('FAIL ') === 0 || row.indexOf('not ok ') === 0)
        {
            failures.push(trimLeading(row));
        }
    }
    return {
        ok: cases.failed === 0,
        runner: runner.kind,
        root: rootDir,
        files: parseCounts(findSummaryLine(clean, 'Test Files')).total,
        cases: cases.total,
        passed: cases.passed,
        failed: cases.failed,
        skipped: cases.skipped,
        failures: failures,
        elapsedMs: Date.now() - startedMs
    };
};

/**
 * 读 .editorconfig 的缩进口径（自工程根向上首个命中；未命中则不regulate 缩进）
 * @param {string} rootDir 起点
 * @returns {object} {style, size}
 */
function readIndentHint(rootDir)
{
    var out = { style: '', size: 0 };
    var current = rootDir;
    for (var depth = 0; depth < 20; depth = depth + 1)
    {
        var candidate = nodePath.join(current, '.editorconfig');
        if (fs.existsSync(candidate))
        {
            var text = '';
            try
            {
                text = fs.readFileSync(candidate, 'utf8');
            }
            catch (err)
            {
                text = '';
            }
            var rows = text.split(NEWLINE);
            for (var i = 0; i < rows.length; i = i + 1)
            {
                var row = trimTrailing(trimLeading(rows[i]));
                var eq = row.indexOf('=');
                if (eq <= 0)
                {
                    continue;
                }
                var key = trimTrailing(trimLeading(row.slice(0, eq)));
                var value = trimLeading(row.slice(eq + 1));
                if (key === 'indent_style' && out.style.length === 0)
                {
                    out.style = value;
                }
                if (key === 'indent_size' && out.size === 0)
                {
                    var size = leadingNumber(value);
                    if (size > 0)
                    {
                        out.size = size;
                    }
                }
            }
            break;
        }
        var parent = nodePath.dirname(current);
        if (parent === current)
        {
            break;
        }
        current = parent;
    }
    return out;
}

/**
 * 检测文件主行尾（多数侧）
 * @param {string} text 全文
 * @returns {string} 行尾串
 */
function detectEol(text)
{
    var crlf = 0;
    var lf = 0;
    for (var i = 0; i < text.length; i = i + 1)
    {
        if (text.charAt(i) !== '\n')
        {
            continue;
        }
        if (i > 0 && text.charAt(i - 1) === '\r')
        {
            crlf = crlf + 1;
        }
        else
        {
            lf = lf + 1;
        }
    }
    if (crlf === 0 && lf === 0)
    {
        return NEWLINE;
    }
    return crlf >= lf ? '\r\n' : NEWLINE;
}

/**
 * 缩进规整——只按 .editorconfig 依据换算（无依据原样返回，不猜风格）
 * @param {string} line 单行
 * @param {object} hint 缩进口径
 * @returns {string} 规整后
 */
function regulateIndent(line, hint)
{
    if (line.length === 0)
    {
        return line;
    }
    var head = '';
    var k = 0;
    while (k < line.length)
    {
        var ch = line.charAt(k);
        if (ch !== ' ' && ch !== '\t')
        {
            break;
        }
        head = head + ch;
        k = k + 1;
    }
    if (head.length === 0 || hint.size <= 0)
    {
        return line;
    }
    var rest = line.slice(head.length);
    var width = 0;
    for (var i = 0; i < head.length; i = i + 1)
    {
        width = width + (head.charAt(i) === '\t' ? hint.size : 1);
    }
    var levels = Math.round(width / hint.size);
    var built = '';
    if (hint.style === 'tab')
    {
        for (var j = 0; j < levels; j = j + 1)
        {
            built = built + '\t';
        }
        return built + rest;
    }
    if (hint.style === 'space')
    {
        for (var j2 = 0; j2 < levels * hint.size; j2 = j2 + 1)
        {
            built = built + ' ';
        }
        return built + rest;
    }
    return line;
}

/**
 * 文本规整——尾随空白剥离 / 行尾统一 / 缩进按 .editorconfig / 末尾单换行 / BOM 保真
 * @param {string} raw 原文
 * @param {string} eol 目标行尾
 * @param {object} hint 缩进口径
 * @returns {string} 规整后全文
 */
function formatText(raw, eol, hint)
{
    var hasBom = raw.length > 0 && raw.charCodeAt(0) === 65279;
    var text = hasBom ? raw.slice(1) : raw;
    var lines = text.split(NEWLINE);
    var out = [];
    for (var i = 0; i < lines.length; i = i + 1)
    {
        var line = lines[i];
        if (line.length > 0 && line.charAt(line.length - 1) === '\r')
        {
            line = line.slice(0, line.length - 1);
        }
        line = trimTrailing(line);
        out.push(regulateIndent(line, hint));
    }
    while (out.length > 0 && out[out.length - 1].length === 0)
    {
        out.pop();
    }
    var joined = out.join(eol);
    if (joined.length > 0)
    {
        joined = joined + eol;
    }
    if (hasBom)
    {
        joined = String.fromCharCode(65279) + joined;
    }
    return joined;
}

/**
 * ts.format — 文本层规整（check 干跑 / apply 写盘）；零语义变更，不做业务校验
 * @param {object} req 请求
 * @returns {object} 回执
 */
handlers['ts.format'] = function (req)
{
    var input = req.path ? String(req.path) : '';
    if (!input)
    {
        return fail('TS_BAD_ARGS', '缺少参数 path（tsconfig / 目录 / 单文件）');
    }
    var abs = nodePath.resolve(input);
    if (!fs.existsSync(abs))
    {
        return fail('TS_FILE_NOT_FOUND', '路径不存在：' + input);
    }
    var mode = req.mode ? String(req.mode) : 'check';
    if (mode !== 'check' && mode !== 'apply')
    {
        return fail('TS_BAD_ARGS', 'mode 只支持 check / apply');
    }
    var configPath = resolveTsconfig(abs);
    var stat = fs.statSync(abs);
    var targets = [];
    if (configPath)
    {
        var project = null;
        try
        {
            project = getProject(configPath);
        }
        catch (err)
        {
            return fail('TS_TSCONFIG', String(err && err.message ? err.message : err));
        }
        // 声明文件不进格式面——由引擎 / 工具生成（真工程实测：temp/declarations/*.d.ts 会被误判为需规整）
        var allFiles = project.fileNames;
        for (var f = 0; f < allFiles.length; f = f + 1)
        {
            if (allFiles[f].slice(-5) === '.d.ts')
            {
                continue;
            }
            targets.push(allFiles[f]);
        }
    }
    else
    {
        if (stat.isDirectory())
        {
            return fail('TS_TSCONFIG', '未找到 tsconfig：' + input);
        }
        targets = [abs];
    }
    var baseDir = configPath ? nodePath.dirname(configPath) : (stat.isDirectory() ? abs : nodePath.dirname(abs));
    var hint = readIndentHint(baseDir);
    var items = [];
    var changedFiles = 0;
    var changedLines = 0;
    for (var i = 0; i < targets.length; i = i + 1)
    {
        var fileName = targets[i];
        var raw = '';
        try
        {
            raw = fs.readFileSync(fileName, 'utf8');
        }
        catch (err)
        {
            continue;
        }
        var formatted = formatText(raw, detectEol(raw), hint);
        if (formatted === raw)
        {
            continue;
        }
        var before = raw.split(NEWLINE);
        var after = formatted.split(NEWLINE);
        var rowsTotal = before.length > after.length ? before.length : after.length;
        var diff = 0;
        for (var r = 0; r < rowsTotal; r = r + 1)
        {
            var oldLine = r < before.length ? before[r] : '';
            var newLine = r < after.length ? after[r] : '';
            if (oldLine !== newLine)
            {
                diff = diff + 1;
            }
        }
        if (diff === 0)
        {
            diff = 1;
        }
        changedFiles = changedFiles + 1;
        changedLines = changedLines + diff;
        var item = { file: relName(baseDir, fileName), changedLines: diff, written: false };
        if (mode === 'apply')
        {
            try
            {
                fs.writeFileSync(fileName, formatted, 'utf8');
                item.written = true;
            }
            catch (err)
            {
                item.error = String(err && err.message ? err.message : err);
            }
        }
        items.push(item);
    }
    return {
        ok: true,
        mode: mode,
        files: targets.length,
        changedFiles: changedFiles,
        changedLines: changedLines,
        indent: hint.style.length > 0 ? hint.style + ' ' + String(hint.size) : '（未发现 .editorconfig——缩进不动）',
        items: items
    };
};

// ---------------------------------------------------------------- 协议主循环

var rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });

rl.on('line', function (line)
{
    var text = line;
    if (text.length === 0)
    {
        return;
    }
    var req = null;
    try
    {
        req = JSON.parse(text);
    }
    catch (err)
    {
        // 解析失败必须出声——不静默跳过（否则表现成调用超时）
        writeLine({ id: '', ok: false, error: 'BAD_JSON', message: String(err && err.message ? err.message : err) });
        return;
    }
    var id = req && req.id !== undefined ? req.id : '';
    var op = req && req.op ? String(req.op) : '';
    var startedMs = Date.now();
    try
    {
        if (op === 'ping')
        {
            writeLine({ id: id, ok: true, op: 'ping', ms: Date.now() - startedMs });
            return;
        }
        if (op === 'version')
        {
            var tsVersion = '';
            try
            {
                tsVersion = ensureTs().version;
            }
            catch (err)
            {
                tsVersion = '未安装（npm install typescript）';
            }
            writeLine({
                id: id,
                ok: true,
                op: 'version',
                version: process.version,
                host: 'catsrv',
                node: process.execPath,
                typescript: tsVersion,
                home: homeDir,
                ms: Date.now() - startedMs
            });
            return;
        }
        var handler = handlers[op];
        if (!handler)
        {
            writeLine({ id: id, ok: false, error: 'BAD_OP', message: '未知操作：' + op });
            return;
        }
        var result = handler(req);
        result.id = id;
        result.op = op;
        result.ms = Date.now() - startedMs;
        writeLine(result);
    }
    catch (ex)
    {
        writeLine({ id: id, ok: false, error: 'TS_EX', message: String(ex && ex.message ? ex.message : ex) });
    }
});

rl.on('close', function ()
{
    // stdin 关闭 = 宿主退出——进程自然结束（不留孤儿）
    process.exit(0);
});

// ready 握手——首行必须为 ready 事件（严格协议）
writeLine({ event: 'ready', pid: process.pid, version: process.version });
