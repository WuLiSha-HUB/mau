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
    var until = Date.now() + delay;
    while (Date.now() < until)
    {
        // 忙等——阻塞事件循环，复现「回执迟到」
    }
    writeLine({ id: req.id, ok: true, op: req.op, echo: req.id });
});

rl.on('close', function ()
{
    process.exit(0);
});

writeLine({ event: 'ready', pid: process.pid });
