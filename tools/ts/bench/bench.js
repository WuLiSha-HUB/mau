// A292 压测驱动——直连 stdio_server.js（绕过 C# 宿主，测服务层真相）
// 用法：node bench.js --project <工程目录> --scenario <ping|check|list|read|findref|patch|mix|mem> [--conc N] [--count M]
// 输出：单行 JSON 摘要（p50 / p90 / p95 / max / 均值 / 错误 / 回执 id 错配 / 内存）
'use strict';

var childProcess = require('child_process');
var readline = require('readline');
var fs = require('fs');
var nodePath = require('path');

/**
 * 解析命令行参数
 * @param {string[]} argv 进程参数
 * @returns {object} 参数表
 */
function parseArgs(argv)
{
    var result = {
        project: '',
        projects: [],
        scenario: 'ping',
        conc: 1,
        count: 10,
        log: '',
        service: nodePath.join(__dirname, '..', 'stdio_server.js')
    };
    for (var i = 2; i < argv.length; i = i + 1)
    {
        var key = argv[i];
        if (key === '--project' && i + 1 < argv.length)
        {
            result.project = argv[i + 1];
            result.projects.push(argv[i + 1]);
            i = i + 1;
        }
        else if (key === '--scenario' && i + 1 < argv.length)
        {
            result.scenario = argv[i + 1];
            i = i + 1;
        }
        else if (key === '--conc' && i + 1 < argv.length)
        {
            result.conc = parseInt(argv[i + 1], 10);
            i = i + 1;
        }
        else if (key === '--count' && i + 1 < argv.length)
        {
            result.count = parseInt(argv[i + 1], 10);
            i = i + 1;
        }
        else if (key === '--log' && i + 1 < argv.length)
        {
            result.log = argv[i + 1];
            i = i + 1;
        }
    }
    return result;
}

/**
 * 分位数
 * @param {number[]} values 有序或无序样本
 * @param {number} ratio 分位（0..1）
 * @returns {number} 分位值
 */
function percentile(values, ratio)
{
    if (values.length === 0)
    {
        return 0;
    }
    var sorted = values.slice().sort(function (a, b) { return a - b; });
    var index = Math.ceil(ratio * sorted.length) - 1;
    if (index < 0)
    {
        index = 0;
    }
    return sorted[index];
}

/**
 * 服务句柄——一行 JSON 往返，按 FIFO 匹配回执
 * @param {string} scriptPath 服务脚本路径
 */
function Service(scriptPath)
{
    this.scriptPath = scriptPath;
    this.proc = null;
    this.waiters = [];
    this.seq = 0;
    this.mismatch = 0;
    this.stderrLines = 0;
}

/**
 * 拉起服务并等 ready 握手
 * @returns {Promise<number>} 握手耗时毫秒
 */
Service.prototype.start = function ()
{
    var self = this;
    var startedMs = Date.now();
    this.proc = childProcess.spawn(process.execPath, [this.scriptPath], {
        cwd: nodePath.dirname(this.scriptPath),
        stdio: ['pipe', 'pipe', 'pipe']
    });
    var rl = readline.createInterface({ input: this.proc.stdout, crlfDelay: Infinity });
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
        var waiter = self.waiters.shift();
        if (!waiter)
        {
            process.stderr.write('ORPHAN_REPLY: ' + line + '\n');
            return;
        }
        if (reply.event === 'ready')
        {
            waiter.resolve(reply);
            return;
        }
        if (String(reply.id) !== String(waiter.id))
        {
            self.mismatch = self.mismatch + 1;
        }
        waiter.resolve(reply);
    });
    var errRl = readline.createInterface({ input: this.proc.stderr, crlfDelay: Infinity });
    errRl.on('line', function (line)
    {
        self.stderrLines = self.stderrLines + 1;
        if (process.env.BENCH_VERBOSE === '1')
        {
            process.stderr.write('[srv] ' + line + '\n');
        }
    });
    process.on('exit', function ()
    {
        self.close();
    });
    return new Promise(function (resolve, reject)
    {
        self.waiters.push({
            id: '',
            resolve: function (reply) { resolve(Date.now() - startedMs); }
        });
        setTimeout(function () { reject(new Error('ready 握手超时')); }, 60000);
    });
};

/**
 * 发一条请求并等回执
 * @param {object} payload 请求体（id 自动分配）
 * @returns {Promise<object>} 回执
 */
Service.prototype.rpc = function (payload)
{
    var self = this;
    this.seq = this.seq + 1;
    var id = 'b' + String(this.seq);
    payload.id = id;
    return new Promise(function (resolve)
    {
        self.waiters.push({
            id: id,
            resolve: function (reply)
            {
                resolve({ reply: reply, id: id });
            }
        });
        self.proc.stdin.write(JSON.stringify(payload) + '\n');
    });
};

/**
 * 关闭服务（stdin 关闭即退出）
 */
Service.prototype.close = function ()
{
    if (this.proc)
    {
        try
        {
            this.proc.stdin.end();
        }
        catch (err)
        {
            // stdin 已关闭——忽略
        }
    }
};

/**
 * 构造场景请求
 * @param {object} args 参数表
 * @param {number} index 序号（patch 用）
 * @returns {object} 请求体（不含 id）
 */
function buildPayload(args, index)
{
    var project = nodePath.resolve(args.project);
    var tsconfig = nodePath.join(project, 'tsconfig.json');
    var sampleFile = nodePath.join(project, 'src', 'node_0005.ts');
    if (args.scenario === 'ping')
    {
        return { op: 'ping' };
    }
    if (args.scenario === 'check')
    {
        return { op: 'ts.check', path: tsconfig };
    }
    if (args.scenario === 'list')
    {
        return { op: 'ts.list', file: sampleFile };
    }
    if (args.scenario === 'read')
    {
        return { op: 'ts.read', file: sampleFile, symbol: 'Item0005' };
    }
    if (args.scenario === 'findref')
    {
        return { op: 'ts.findRef', file: sampleFile, symbol: 'Entity', tsconfig: tsconfig };
    }
    if (args.scenario === 'mem')
    {
        return { op: 'stats' };
    }
    return { op: 'ping' };
}

/**
 * 并发跑一批请求
 * @param {Service} service 服务句柄
 * @param {object} args 参数表
 * @param {number} total 总请求数
 * @param {number} conc 并发度
 * @returns {Promise<object>} 统计
 */
function runBatch(service, args, total, conc)
{
    var latencies = [];
    var errors = 0;
    var errorCodes = Object.create(null);
    var inFlight = 0;
    var started = 0;
    var patchValue = 0;
    var targetFile = nodePath.join(nodePath.resolve(args.project), 'src', 'bench_target.ts');

    return new Promise(function (resolve)
    {
        function launch()
        {
            while (inFlight < conc && started < total)
            {
                started = started + 1;
                inFlight = inFlight + 1;
                var payload = buildPayload(args, started);
                if (args.scenario === 'patch')
                {
                    var next = patchValue + 1;
                    payload = {
                        op: 'ts.patch',
                        file: targetFile,
                        old: 'export var benchFlag = ' + String(patchValue) + ';',
                        new: 'export var benchFlag = ' + String(next) + ';'
                    };
                    patchValue = next;
                }
                var t0 = process.hrtime.bigint();
                service.rpc(payload).then(function (result)
                {
                    var elapsed = Number(process.hrtime.bigint() - t0) / 1e6;
                    latencies.push(elapsed);
                    if (args.log)
                    {
                        fs.appendFileSync(args.log, JSON.stringify({
                            t: Date.now(),
                            scenario: args.scenario,
                            ms: Math.round(elapsed),
                            ok: result.reply.ok === true,
                            error: result.reply.error ? String(result.reply.error) : ''
                        }) + '\n', 'utf8');
                    }
                    if (result.reply.ok !== true)
                    {
                        errors = errors + 1;
                        var code = result.reply.error ? String(result.reply.error) : 'UNKNOWN';
                        errorCodes[code] = (errorCodes[code] || 0) + 1;
                    }
                    inFlight = inFlight - 1;
                    if (started >= total && inFlight === 0)
                    {
                        finish();
                    }
                    else
                    {
                        launch();
                    }
                });
            }
        }

        function finish()
        {
            var sum = 0;
            for (var i = 0; i < latencies.length; i = i + 1)
            {
                sum = sum + latencies[i];
            }
            resolve({
                count: latencies.length,
                errors: errors,
                errorCodes: errorCodes,
                mean: latencies.length > 0 ? Math.round(sum / latencies.length) : 0,
                min: latencies.length > 0 ? Math.round(percentile(latencies, 0)) : 0,
                p50: Math.round(percentile(latencies, 0.5)),
                p90: Math.round(percentile(latencies, 0.9)),
                p95: Math.round(percentile(latencies, 0.95)),
                max: latencies.length > 0 ? Math.round(percentile(latencies, 1)) : 0
            });
        }

        launch();
    });
}

/**
 * 主流程
 */
async function main()
{
    var args = parseArgs(process.argv);
    var service = new Service(args.service);
    var handshakeMs = await service.start();
    var statsBefore = (await service.rpc({ op: 'stats' })).reply;
    var firstMs = 0;
    var results = [];

    var memSeries = [];
    if (args.scenario === 'multi')
    {
        for (var cycle = 0; cycle < args.count; cycle = cycle + 1)
        {
            for (var p = 0; p < args.projects.length; p = p + 1)
            {
                var subArgs = { project: args.projects[p], scenario: 'check' };
                results.push(Object.assign({ cycle: cycle, project: args.projects[p] }, await runBatch(service, subArgs, 1, 1)));
            }
            var snap = (await service.rpc({ op: 'stats' })).reply;
            memSeries.push({
                cycle: cycle,
                rssMb: Math.round(snap.rss / 1048576),
                heapUsedMb: Math.round(snap.heapUsed / 1048576),
                projects: snap.projects,
                projectFiles: snap.projectFiles
            });
        }
    }
    else if (args.scenario === 'mix')
    {
        var scenarios = ['check', 'list', 'read', 'findref'];
        for (var i = 0; i < scenarios.length; i = i + 1)
        {
            var subArgs = { project: args.project, scenario: scenarios[i] };
            results.push(Object.assign({ scenario: scenarios[i] }, await runBatch(service, subArgs, args.count, args.conc)));
        }
    }
    else
    {
        var t0 = Date.now();
        await service.rpc(buildPayload(args, 1));
        firstMs = Date.now() - t0;
        results.push(Object.assign({ scenario: args.scenario }, await runBatch(service, args, args.count, args.conc)));
    }

    var statsAfter = (await service.rpc({ op: 'stats' })).reply;
    service.close();

    process.stdout.write(JSON.stringify({
        ok: true,
        project: nodePath.resolve(args.project),
        scenario: args.scenario,
        conc: args.conc,
        handshakeMs: handshakeMs,
        firstRequestMs: firstMs,
        idMismatch: service.mismatch,
        stderrLines: service.stderrLines,
        mem: {
            rssBeforeMb: Math.round(statsBefore.rss / 1048576),
            rssAfterMb: Math.round(statsAfter.rss / 1048576),
            heapUsedAfterMb: Math.round(statsAfter.heapUsed / 1048576),
            projects: statsAfter.projects,
            projectFiles: statsAfter.projectFiles
        },
        memSeries: memSeries,
        results: results
    }, null, 2) + '\n');
}

main().catch(function (err)
{
    process.stderr.write('BENCH_FAIL: ' + (err && err.stack ? err.stack : String(err)) + '\n');
    process.exit(1);
});
