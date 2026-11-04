// StdioServiceHost 测试桩——可控慢回执（A292 T8：超时后迟到回执不得被当作下一次调用的结果）
// 协议：一行 JSON 进 / 一行 JSON 出；首行 ready。delayMs 用忙等模拟慢处理（串行语义下等价于长任务）。
'use strict';

var readline = require('readline');

/**
 * 写一行协议
 * @param {object} obj 协议对象
 */
function writeLine(obj)
{
    process.stdout.write(JSON.stringify(obj) + '\n');
}

var rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });

rl.on('line', function (line)
{
    var req = null;
    try
    {
        req = JSON.parse(line);
    }
    catch (err)
    {
        writeLine({ id: '', ok: false, error: 'BAD_JSON' });
        return;
    }
    if (req.op === 'quit')
    {
        process.exit(0);
    }
    var delay = req.delayMs ? Number(req.delayMs) : 0;
    if (delay > 0)
    {
        // 延迟回执——不阻塞事件循环（setTimeout 单调时钟，墙钟跳变不影响）。
        // 语义等价「回执迟到」：本次请求的应答在 timeout 之后才到；
        // 而后续请求可被立刻处理（不因前一次未完成而排队）。
        setTimeout(function ()
        {
            writeLine({ id: req.id, ok: true, op: req.op, echo: req.id });
        }, delay);
        return;
    }
    writeLine({ id: req.id, ok: true, op: req.op, echo: req.id });
});

rl.on('close', function ()
{
    process.exit(0);
});

writeLine({ event: 'ready', pid: process.pid });
