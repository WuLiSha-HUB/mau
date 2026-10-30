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
 * @param {object} project 工程上下文
 * @param {boolean} full 是否含警告
 * @returns {object} { errors, warnings }
 */
function collectDiagnostics(project, full)
{
    var errors = [];
    var warnings = [];
    for (var i = 0; i < project.fileNames.length; i = i + 1)
    {
        var fileName = project.fileNames[i];
        var semantic = project.service.getSemanticDiagnostics(fileName) || [];
        var syntactic = project.service.getSyntacticDiagnostics(fileName) || [];
        var j;
        for (j = 0; j < syntactic.length; j = j + 1)
        {
            errors.push(diagItem(project, syntactic[j]));
        }
        for (j = 0; j < semantic.length; j = j + 1)
        {
            errors.push(diagItem(project, semantic[j]));
        }
        if (full)
        {
            var suggestion = project.service.getSuggestionDiagnostics(fileName) || [];
            for (j = 0; j < suggestion.length; j = j + 1)
            {
                warnings.push(diagItem(project, suggestion[j]));
            }
        }
    }
    return { errors: errors, warnings: warnings };
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
    var position = hits[0].getStart();
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
