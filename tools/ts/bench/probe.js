// A292 探针——单次请求原样回执（校验语义正确性用；压测看分布，探针看内容）
// 用法：node probe.js --op <check|list|read|findref|patch|stats|ping> [--project dir] [--file f] [--symbol s] [--old o] [--new n]
'use strict';

var childProcess = require('child_process');
var readline = require('readline');
var nodePath = require('path');

/**
 * 解析参数
 * @param {string[]} argv 进程参数
 * @returns {object} 参数表
 */
function parseArgs(argv)
{
    var result = { op: 'ping', project: '', file: '', symbol: '', old: '', newText: '', full: false };
    for (var i = 2; i < argv.length; i = i + 1)
    {
        var key = argv[i];
        if (key === '--op' && i + 1 < argv.length)
        {
            result.op = argv[i + 1];
            i = i + 1;
        }
        else if (key === '--project' && i + 1 < argv.length)
        {
            result.project = argv[i + 1];
            i = i + 1;
        }
        else if (key === '--file' && i + 1 < argv.length)
        {
            result.file = argv[i + 1];
            i = i + 1;
        }
        else if (key === '--symbol' && i + 1 < argv.length)
        {
            result.symbol = argv[i + 1];
            i = i + 1;
        }
        else if (key === '--old' && i + 1 < argv.length)
        {
            result.old = argv[i + 1];
            i = i + 1;
        }
        else if (key === '--new' && i + 1 < argv.length)
        {
            result.newText = argv[i + 1];
            i = i + 1;
        }
        else if (key === '--full')
        {
            result.full = true;
        }
    }
    return result;
}

/**
 * 组装请求体
 * @param {object} args 参数表
 * @returns {object} 请求体
 */
function buildPayload(args)
{
    var tsconfig = args.project ? nodePath.join(nodePath.resolve(args.project), 'tsconfig.json') : '';
    if (args.op === 'check')
    {
        return { op: 'ts.check', path: tsconfig || args.file, full: args.full };
    }
    if (args.op === 'list')
    {
        return { op: 'ts.list', file: args.file };
    }
    if (args.op === 'read')
    {
        return { op: 'ts.read', file: args.file, symbol: args.symbol };
    }
    if (args.op === 'findref')
    {
        return { op: 'ts.findRef', file: args.file, symbol: args.symbol, tsconfig: tsconfig };
    }
    if (args.op === 'patch')
    {
        return { op: 'ts.patch', file: args.file, old: args.old, new: args.newText, tsconfig: tsconfig };
    }
    if (args.op === 'stats')
    {
        return { op: 'stats' };
    }
    return { op: 'ping' };
}

/**
 * 主流程
 */
function main()
{
    var args = parseArgs(process.argv);
    var scriptPath = nodePath.join(__dirname, '..', 'stdio_server.js');
    var proc = childProcess.spawn(process.execPath, [scriptPath], {
        cwd: nodePath.dirname(scriptPath),
        stdio: ['pipe', 'pipe', 'inherit']
    });
    var rl = readline.createInterface({ input: proc.stdout, crlfDelay: Infinity });
    var sent = false;
    rl.on('line', function (line)
    {
        var reply = null;
        try
        {
            reply = JSON.parse(line);
        }
        catch (err)
        {
            process.stderr.write('BAD_JSON: ' + line + '\n');
            return;
        }
        if (reply.event === 'ready')
        {
            if (sent)
            {
                return;
            }
            sent = true;
            proc.stdin.write(JSON.stringify(buildPayload(args)) + '\n');
            return;
        }
        process.stdout.write(JSON.stringify(reply, null, 2) + '\n');
        proc.stdin.end();
    });
    proc.on('close', function (code)
    {
        process.exit(code === null ? 0 : code);
    });
}

main();
