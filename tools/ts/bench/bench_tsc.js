// A292 参照基线——同一工程跑 tsc --noEmit，量「原生编译器整工程耗时」
// 用途：区分「成本是 TS 本身的」还是「服务实现放大的」——两者混淆会把实现缺陷说成客观限制
// 用法：node bench_tsc.js --project <工程目录> [--repeat N]
'use strict';

var childProcess = require('child_process');
var nodePath = require('path');

/**
 * 解析参数
 * @param {string[]} argv 进程参数
 * @returns {object} 参数表
 */
function parseArgs(argv)
{
    var result = { project: '', repeat: 3 };
    for (var i = 2; i < argv.length; i = i + 1)
    {
        if (argv[i] === '--project' && i + 1 < argv.length)
        {
            result.project = argv[i + 1];
            i = i + 1;
        }
        else if (argv[i] === '--repeat' && i + 1 < argv.length)
        {
            result.repeat = parseInt(argv[i + 1], 10);
            i = i + 1;
        }
    }
    return result;
}

/**
 * 跑一次 tsc
 * @param {string} tscPath tsc 入口
 * @param {string} projectDir 工程目录
 * @returns {Promise<object>} { ms, code }
 */
function runTsc(tscPath, projectDir)
{
    var startedMs = Date.now();
    return new Promise(function (resolve)
    {
        var proc = childProcess.spawn(process.execPath, [tscPath, '-p', nodePath.join(projectDir, 'tsconfig.json'), '--noEmit'], {
            cwd: projectDir,
            stdio: ['ignore', 'pipe', 'pipe']
        });
        var out = '';
        proc.stdout.on('data', function (chunk) { out = out + String(chunk); });
        proc.stderr.on('data', function (chunk) { out = out + String(chunk); });
        proc.on('close', function (code)
        {
            resolve({ ms: Date.now() - startedMs, code: code, out: out.slice(0, 400) });
        });
    });
}

/**
 * 主流程
 */
async function main()
{
    var args = parseArgs(process.argv);
    if (!args.project)
    {
        process.stderr.write('缺少参数 --project <dir>\n');
        process.exit(2);
    }
    var tscPath = nodePath.join(__dirname, '..', 'node_modules', 'typescript', 'bin', 'tsc');
    var runs = [];
    for (var i = 0; i < args.repeat; i = i + 1)
    {
        runs.push(await runTsc(tscPath, nodePath.resolve(args.project)));
    }
    process.stdout.write(JSON.stringify({
        ok: true,
        project: nodePath.resolve(args.project),
        runs: runs
    }, null, 2) + '\n');
}

main();
