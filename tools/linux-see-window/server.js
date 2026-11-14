'use strict';

// linux-see-window —— Linux 运行健康度窗口（零依赖）
// 数据面一律直读 /proc 与 statfs；不装 npm 包，不依赖外部命令。
// 监听 7900（外围工具下行段），来源只放行 tailnet 100.64.0.0/10 与 localhost。

const fs = require('fs');
const fsp = fs.promises;
const http = require('http');
const path = require('path');

const VERSION = '1.0.0';
const PORT = Number(process.env.LSW_PORT || 7900);
const BIND = process.env.LSW_BIND || '0.0.0.0';
const WEB_ROOT = path.join(__dirname, 'web');
const SECTOR_BYTES = 512;
const PAGE_BYTES = 4096;
const TOP_N = 8;

const startedAt = Date.now();

let prevCpu = null;
let prevNet = null;
let prevDisk = null;
let prevProcs = null;
let prevSampleMs = 0;

/**
 * 读文本文件，失败返回 null 并记入 errors。
 */
async function readText(file, errors) {
    try {
        return await fsp.readFile(file, 'utf8');
    } catch (err) {
        errors.push('read ' + file + ': ' + err.message);
        return null;
    }
}

/**
 * 解析 /proc/stat 的 cpu 行 → { idle, total }。
 */
function parseCpuLine(line) {
    const fields = line.trim().split(/\s+/).slice(1).map(Number);
    const idle = (fields[3] || 0) + (fields[4] || 0);
    let total = 0;
    for (let i = 0; i < fields.length; i = i + 1) {
        total = total + (fields[i] || 0);
    }
    return { idle: idle, total: total };
}

/**
 * 由两次采样差值算使用率（0..100），首轮返回 null。
 */
function usageOf(prev, cur) {
    if (!prev || !cur) {
        return null;
    }
    const totalDiff = cur.total - prev.total;
    const idleDiff = cur.idle - prev.idle;
    if (totalDiff <= 0) {
        return null;
    }
    const used = 100 - (idleDiff * 100) / totalDiff;
    if (used < 0) {
        return 0;
    }
    if (used > 100) {
        return 100;
    }
    return Math.round(used * 10) / 10;
}

/**
 * 采集 CPU：总体 + 每核。
 */
async function collectCpu(errors) {
    const text = await readText('/proc/stat', errors);
    if (!text) {
        return null;
    }
    const lines = text.split('\n');
    let totalLine = null;
    const cores = [];
    for (let i = 0; i < lines.length; i = i + 1) {
        const line = lines[i];
        if (line.indexOf('cpu') !== 0) {
            continue;
        }
        if (line.indexOf('cpu ') === 0) {
            totalLine = line;
        } else {
            cores.push({ name: line.split(/\s+/)[0], stat: parseCpuLine(line) });
        }
    }
    if (!totalLine) {
        errors.push('/proc/stat: 缺 cpu 汇总行');
        return null;
    }
    const totalStat = parseCpuLine(totalLine);
    const prevCores = (prevCpu && prevCpu.cores) || [];
    const coreList = [];
    for (let i = 0; i < cores.length; i = i + 1) {
        coreList.push({
            name: cores[i].name,
            usage: usageOf(prevCores[i] ? prevCores[i].stat : null, cores[i].stat),
        });
    }
    const result = {
        usage: usageOf(prevCpu ? prevCpu.stat : null, totalStat),
        coreCount: cores.length,
        cores: coreList,
    };
    prevCpu = { stat: totalStat, cores: cores };
    return result;
}

/**
 * 采集负载与开机时长。
 */
async function collectLoad(errors) {
    const loadText = await readText('/proc/loadavg', errors);
    const upText = await readText('/proc/uptime', errors);
    const out = { load1: null, load5: null, load15: null, running: null, uptimeSec: null };
    if (loadText) {
        const p = loadText.trim().split(/\s+/);
        out.load1 = Number(p[0]);
        out.load5 = Number(p[1]);
        out.load15 = Number(p[2]);
        out.running = p[3] || null;
    }
    if (upText) {
        out.uptimeSec = Math.round(Number(upText.trim().split(/\s+/)[0]));
    }
    return out;
}

/**
 * 采集内存与交换区。
 */
async function collectMemory(errors) {
    const text = await readText('/proc/meminfo', errors);
    if (!text) {
        return null;
    }
    const map = {};
    const lines = text.split('\n');
    for (let i = 0; i < lines.length; i = i + 1) {
        const m = lines[i].match(/^(\w+):\s+(\d+)/);
        if (m) {
            map[m[1]] = Number(m[2]) * 1024;
        }
    }
    const total = map.MemTotal || 0;
    const available = map.MemAvailable || 0;
    const swapTotal = map.SwapTotal || 0;
    const swapFree = map.SwapFree || 0;
    return {
        totalBytes: total,
        availableBytes: available,
        usedBytes: total - available,
        usedPercent: total > 0 ? Math.round(((total - available) * 1000) / total) / 10 : null,
        cachedBytes: (map.Cached || 0) + (map.Buffers || 0),
        swapTotalBytes: swapTotal,
        swapUsedBytes: swapTotal - swapFree,
        swapUsedPercent: swapTotal > 0 ? Math.round(((swapTotal - swapFree) * 1000) / swapTotal) / 10 : null,
    };
}

/**
 * 采集体积用量（statfs）。
 */
async function collectDisks(errors) {
    const targets = ['/', '/cathome4'];
    const list = [];
    for (let i = 0; i < targets.length; i = i + 1) {
        const target = targets[i];
        try {
            const st = await fsp.statfs(target);
            const total = st.blocks * st.bsize;
            const free = st.bfree * st.bsize;
            const avail = st.bavail * st.bsize;
            list.push({
                mount: target,
                totalBytes: total,
                freeBytes: free,
                usedBytes: total - free,
                usedPercent: total > 0 ? Math.round(((total - free) * 1000) / total) / 10 : null,
                availBytes: avail,
            });
        } catch (err) {
            errors.push('statfs ' + target + ': ' + err.message);
        }
    }
    return list;
}

/**
 * 采集网络读写速率（非 lo 接口合计）。
 */
async function collectNetwork(errors) {
    const text = await readText('/proc/net/dev', errors);
    if (!text) {
        return null;
    }
    const now = Date.now();
    const lines = text.split('\n');
    let rxBytes = 0;
    let txBytes = 0;
    const ifaces = [];
    for (let i = 2; i < lines.length; i = i + 1) {
        const line = lines[i];
        const idx = line.indexOf(':');
        if (idx < 0) {
            continue;
        }
        const name = line.slice(0, idx).trim();
        const f = line.slice(idx + 1).trim().split(/\s+/).map(Number);
        if (name === 'lo' || f.length < 9) {
            continue;
        }
        rxBytes = rxBytes + f[0];
        txBytes = txBytes + f[8];
        ifaces.push({ name: name, rxBytes: f[0], txBytes: f[8] });
    }
    const out = { rxTotalBytes: rxBytes, txTotalBytes: txBytes, rxPerSec: null, txPerSec: null, ifaces: ifaces };
    if (prevNet && prevSampleMs > 0) {
        const dt = (now - prevSampleMs) / 1000;
        if (dt > 0) {
            out.rxPerSec = Math.max(0, Math.round((rxBytes - prevNet.rxBytes) / dt));
            out.txPerSec = Math.max(0, Math.round((txBytes - prevNet.txBytes) / dt));
        }
    }
    prevNet = { rxBytes: rxBytes, txBytes: txBytes };
    return out;
}

/**
 * 采集磁盘读写速率（/proc/diskstats，只计物理盘）。
 */
async function collectDiskIo(errors) {
    const text = await readText('/proc/diskstats', errors);
    if (!text) {
        return null;
    }
    const now = Date.now();
    const lines = text.split('\n');
    let rdSectors = 0;
    let wrSectors = 0;
    let ioMs = 0;
    for (let i = 0; i < lines.length; i = i + 1) {
        const f = lines[i].trim().split(/\s+/);
        if (f.length < 14) {
            continue;
        }
        const name = f[2];
        if (/^(loop|ram|sr|dm-)/.test(name)) {
            continue;
        }
        if (/[0-9]$/.test(name)) {
            continue;
        }
        rdSectors = rdSectors + Number(f[5] || 0);
        wrSectors = wrSectors + Number(f[9] || 0);
        ioMs = ioMs + Number(f[12] || 0);
    }
    const out = { readBytesTotal: rdSectors * SECTOR_BYTES, writeBytesTotal: wrSectors * SECTOR_BYTES, readPerSec: null, writePerSec: null, utilPercent: null };
    if (prevDisk && prevSampleMs > 0) {
        const dt = (now - prevSampleMs) / 1000;
        if (dt > 0) {
            out.readPerSec = Math.max(0, Math.round((rdSectors - prevDisk.rdSectors) * SECTOR_BYTES / dt));
            out.writePerSec = Math.max(0, Math.round((wrSectors - prevDisk.wrSectors) * SECTOR_BYTES / dt));
            const busy = (ioMs - prevDisk.ioMs) / (dt * 1000) * 100;
            out.utilPercent = Math.max(0, Math.min(100, Math.round(busy * 10) / 10));
        }
    }
    prevDisk = { rdSectors: rdSectors, wrSectors: wrSectors, ioMs: ioMs };
    return out;
}

/**
 * 采集进程 TOP N（CPU% 与常驻内存）。
 */
async function collectProcesses(errors) {
    const now = Date.now();
    const dtMs = prevSampleMs > 0 ? now - prevSampleMs : 0;
    const cur = new Map();
    let pids = [];
    try {
        pids = await fsp.readdir('/proc');
    } catch (err) {
        errors.push('read /proc: ' + err.message);
        return null;
    }
    const list = [];
    for (let i = 0; i < pids.length; i = i + 1) {
        const pid = pids[i];
        if (!/^\d+$/.test(pid)) {
            continue;
        }
        let text = null;
        try {
            text = await fsp.readFile('/proc/' + pid + '/stat', 'utf8');
        } catch (err) {
            continue;
        }
        const close = text.lastIndexOf(')');
        if (close < 0) {
            continue;
        }
        const comm = text.slice(text.indexOf('(') + 1, close);
        const rest = text.slice(close + 2).split(/\s+/);
        const ticks = Number(rest[11] || 0) + Number(rest[12] || 0);
        const rssPages = Number(rest[21] || 0);
        cur.set(pid, ticks);
        let cpuPercent = null;
        if (prevProcs && prevProcs.has(pid) && dtMs > 0) {
            const deltaTicks = ticks - prevProcs.get(pid);
            cpuPercent = Math.max(0, Math.round((deltaTicks * 1000 * 10) / (dtMs * 10)) / 10);
        }
        list.push({ pid: Number(pid), comm: comm, cpuPercent: cpuPercent, rssBytes: rssPages * PAGE_BYTES });
    }
    prevProcs = cur;
    list.sort(function (a, b) {
        return (b.cpuPercent || 0) - (a.cpuPercent || 0) || b.rssBytes - a.rssBytes;
    });
    return list.slice(0, TOP_N);
}

/**
 * 采集系统身份信息。
 */
async function collectSystem(errors) {
    const os = require('os');
    const hostname = os.hostname();
    const kernel = os.release();
    const text = await readText('/etc/os-release', errors);
    let distro = null;
    if (text) {
        const m = text.match(/^PRETTY_NAME="?(.+?)"?$/m);
        if (m) {
            distro = m[1];
        }
    }
    let tempC = null;
    try {
        const t = await fsp.readFile('/sys/class/thermal/thermal_zone0/temp', 'utf8');
        tempC = Number(t.trim()) / 1000;
    } catch (err) {
        tempC = null;
    }
    return { hostname: hostname, kernel: kernel, distro: distro, arch: os.arch(), tempC: tempC, nodeVersion: process.version };
}

/**
 * 采集一次完整快照。
 */
async function collect() {
    const errors = [];
    const now = Date.now();
    const sys = await collectSystem(errors);
    const cpu = await collectCpu(errors);
    const load = await collectLoad(errors);
    const memory = await collectMemory(errors);
    const disks = await collectDisks(errors);
    const network = await collectNetwork(errors);
    const diskIo = await collectDiskIo(errors);
    const processes = await collectProcesses(errors);
    const selfMem = process.memoryUsage();
    prevSampleMs = now;
    return {
        ok: true,
        tool: 'linux-see-window',
        version: VERSION,
        sampledAt: new Date(now).toISOString(),
        serviceUptimeSec: Math.round((now - startedAt) / 1000),
        serviceRssBytes: selfMem.rss,
        system: sys,
        cpu: cpu,
        load: load,
        memory: memory,
        disks: disks,
        network: network,
        diskIo: diskIo,
        processes: processes,
        errors: errors,
    };
}

/**
 * 判断来源地址是否在放行面（tailnet 100.64.0.0/10 或 localhost）。
 */
function isAllowedSource(remote) {
    if (!remote) {
        return false;
    }
    let addr = remote;
    if (addr.indexOf('::ffff:') === 0) {
        addr = addr.slice(7);
    }
    if (addr === '::1' || addr === '127.0.0.1') {
        return true;
    }
    const parts = addr.split('.');
    if (parts.length !== 4) {
        return false;
    }
    const a = Number(parts[0]);
    const b = Number(parts[1]);
    if (a !== 100) {
        return false;
    }
    return b >= 64 && b <= 127;
}

/**
 * 读取前端静态文件（限定 web 目录，禁目录穿越）。
 */
async function serveStatic(res, urlPath) {
    const rel = urlPath === '/' ? 'index.html' : urlPath.replace(/^\/+/, '');
    if (rel.indexOf('..') >= 0) {
        res.writeHead(403, { 'Content-Type': 'text/plain; charset=utf-8' });
        res.end('forbidden');
        return;
    }
    const file = path.join(WEB_ROOT, rel);
    const ext = path.extname(file).toLowerCase();
    const types = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8' };
    try {
        const buf = await fsp.readFile(file);
        res.writeHead(200, { 'Content-Type': types[ext] || 'application/octet-stream', 'Cache-Control': 'no-store' });
        res.end(buf);
    } catch (err) {
        res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
        res.end('not found');
    }
}

const server = http.createServer(async function (req, res) {
    const remote = req.socket.remoteAddress;
    if (!isAllowedSource(remote)) {
        res.writeHead(403, { 'Content-Type': 'text/plain; charset=utf-8' });
        res.end('forbidden: ' + remote);
        return;
    }
    const url = (req.url || '/').split('?')[0];
    if (url === '/api/health') {
        let payload = null;
        try {
            payload = await collect();
        } catch (err) {
            res.writeHead(500, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' });
            res.end(JSON.stringify({ ok: false, error: err.message }));
            return;
        }
        const body = JSON.stringify(payload);
        res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' });
        res.end(body);
        return;
    }
    if (url === '/api/ping') {
        res.writeHead(200, { 'Content-Type': 'text/plain; charset=utf-8', 'Cache-Control': 'no-store' });
        res.end('pong');
        return;
    }
    await serveStatic(res, url);
});

server.listen(PORT, BIND, function () {
    console.log('linux-see-window v' + VERSION + ' listening on ' + BIND + ':' + PORT);
});

process.on('SIGTERM', function () {
    console.log('linux-see-window: SIGTERM received, closing');
    server.close(function () {
        process.exit(0);
    });
    setTimeout(function () {
        process.exit(0);
    }, 3000);
});
