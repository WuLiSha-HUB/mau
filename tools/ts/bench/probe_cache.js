// A344 验证探针——同一服务进程内验证编译面 / tsconfig 是否每次重取
// 用法：node probe_cache.js
'use strict';

var childProcess = require('child_process');
var readline = require('readline');
var fs = require('fs');
var nodePath = require('path');

var repo = 'C:/Users/yufei/Desktop/gitee/mau';
var serverScript = process.argv[2] ? nodePath.resolve(process.argv[2]) : nodePath.join(repo, 'tools', 'ts', 'stdio_server.js');
var engDir = nodePath.join(__dirname, 'eng');
var configPath = nodePath.join(engDir, 'tsconfig.json');
var addedFile = nodePath.join(engDir, 'src', 'added_bad.ts');
var globalFile = nodePath.join(engDir, 'src', 'global.ts');

var proc = null;
var pending = null;
var results = [];
var done = false;

/**
 * 计时结束——打印结论并退出
 */
function finish()
{
    if (done)
    {
        return;
    }
    done = true;
    if (proc)
    {
        proc.stdin.end();
    }
    process.stdout.write('RESULTS\n');
    for (var i = 0; i < results.length; i = i + 1)
    {
        process.stdout.write('  ' + results[i] + '\n');
    }
    fs.rmSync(engDir, { recursive: true, force: true });
    process.exit(0);
}

/**
 * 发一条请求，拿一条回执
 * @param {object} payload 请求体
 * @param {function} cb 回调（回执）
 */
function call(payload, cb)
{
    pending = cb;
    proc.stdin.write(JSON.stringify(payload) + '\n');
}

/**
 * 跑一次工程级 ts.check 并记录摘要
 * @param {string} label 步骤标签
 * @param {function} cb 回调
 */
function check(label, cb)
{
    call({ op: 'ts.check', path: configPath }, function (reply)
    {
        var line = label + ' | ok=' + String(reply.ok) + ' mode=' + String(reply.mode)
            + ' files=' + String(reply.files)
            + ' errors=' + String(reply.counts ? reply.counts.errors : 'na')
            + (reply.error ? ' error=' + String(reply.error) + ' msg=' + String(reply.message) : '');
        results.push(line);
        cb();
    });
}

/**
 * 主流程——五步链
 */
function main()
{
    fs.rmSync(engDir, { recursive: true, force: true });
    fs.cpSync(nodePath.join(repo, 'tools', 'ts', 'fixtures'), engDir, { recursive: true });

    proc = childProcess.spawn(process.execPath, [serverScript], {
        cwd: nodePath.dirname(serverScript),
        stdio: ['pipe', 'pipe', 'inherit']
    });
    var rl = readline.createInterface({ input: proc.stdout, crlfDelay: Infinity });
    rl.on('line', function (line)
    {
        var reply = null;
        try
        {
            reply = JSON.parse(line);
        }
        catch (err)
        {
            process.stdout.write('BAD_JSON ' + line + '\n');
            return;
        }
        if (reply.event === 'ready')
        {
            runSteps();
            return;
        }
        if (pending)
        {
            var cb = pending;
            pending = null;
            cb(reply);
        }
    });
    proc.on('error', function (err)
    {
        process.stdout.write('SPAWN_FAIL ' + String(err && err.message) + '\n');
        finish();
    });
    setTimeout(function ()
    {
        results.push('TIMEOUT——链路未走完');
        finish();
    }, 20000);
}

/**
 * 五步链：初始 → 新增坏文件 → 删回 → tsconfig 收窄 → 复原
 */
function runSteps()
{
    check('[1] 初始', function ()
    {
        fs.writeFileSync(addedFile, 'const badValue: number = "not-a-number";\nexport { badValue };\n', 'utf8');
        check('[2] 新增坏文件后', function ()
        {
            fs.rmSync(addedFile, { force: true });
            check('[3] 删回后', function ()
            {
                var config = JSON.parse(fs.readFileSync(configPath, 'utf8'));
                config.exclude = ['src/global.ts'];
                fs.writeFileSync(configPath, JSON.stringify(config, null, 2) + '\n', 'utf8');
                check('[4] tsconfig 排除一文件后', function ()
                {
                    fs.cpSync(nodePath.join(repo, 'tools', 'ts', 'fixtures', 'tsconfig.json'), configPath);
                    check('[5] tsconfig 复原后', function ()
                    {
                        finish();
                    });
                });
            });
        });
    });
}

main();
