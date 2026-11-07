// tests/server.js —— 前端测试服务（宿主启动时自动拉起；持久化访问入口）
// 定位：前端测试框架之于前端 = Mau.exe 之于 CH4——CH4 通过 HTTP 端点随时触发前端测试
//       （自举闭环：改前端 → 跑测试 → 刷新生效）
// 端点：
//   GET /health          → 健康检查
//   GET /api/test/unit   → 运行 Vitest 单元测试（无需宿主）
//   GET /api/test        → 运行 Vitest 单元测试
// 端口：环境变量 FE_TEST_PORT（默认 8099）
const http = require('http');
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');

// 部署标记清理——自动部署链末端（npm install 完成后 server.js 启动），删除 .fe-deploying 标记
try { fs.unlinkSync(path.join(__dirname, '.fe-deploying')); } catch (e) { /* 标记不存在忽略 */ }

const PORT = parseInt(process.env.FE_TEST_PORT || '8099', 10);
const TEST_DIR = __dirname;

// 宿主守望——FE_HOST_PID = 拉起本服务的宿主进程 PID（C# 侧注入）
// 宿主退出后 node 自杀（D9 修复：孤儿 node 锁目录——宿主死 → 8099 释放 → 仓库可删）
const HOST_PID = parseInt(process.env.FE_HOST_PID || '', 10);
if (!Number.isNaN(HOST_PID) && HOST_PID > 0) {
  const watchdog = setInterval(() => {
    try {
      // 信号 0 仅探测存在性，不产生实际信号
      process.kill(HOST_PID, 0);
    } catch (e) {
      // ESRCH = 进程不存在 → 宿主已退出，自杀；EPERM = 存在但无权限 → 存活，忽略
      if (e && e.code === 'ESRCH') {
        console.log('[fe-test] 宿主进程已退出，前端测试服务自动关闭');
        process.exit(0);
      }
    }
  }, 2000);
  // 不阻止进程自然退出（listen 句柄仍保持——退出路径唯一是宿主死）
  if (typeof watchdog.unref === 'function') {
    watchdog.unref();
  }
}
// Vitest 入口——直调 node_modules 内 vitest.mjs（跑测纪律 #8⑧：禁 npx——首调解析卡 120s，判例 2026-09-08）
const VITEST_ENTRY = path.join(TEST_DIR, 'node_modules', 'vitest', 'vitest.mjs');

// 单次测试运行上限——到点强杀子进程并按超时结算（防挂起无限期占住请求）
const TEST_TIMEOUT_MS = parseInt(process.env.FE_TEST_TIMEOUT_MS || '120000', 10);

// 统一结算——约定 ok 决定状态码
function settle(res, payload) {
  res.writeHead(payload.ok ? 200 : 500, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(payload));
}

function runTest(res) {
  const child = spawn(process.execPath, [VITEST_ENTRY, 'run', '--root', TEST_DIR], {
    cwd: TEST_DIR,
    env: { ...process.env, CI: '1' },
  });
  let stdout = '';
  let stderr = '';
  let settled = false;

  // [段1] 超时守卫——到点强杀子进程
  const timer = setTimeout(() => {
    if (settled) { return; }
    settled = true;
    child.kill();
    settle(res, { ok: false, code: -1, timeout: true, stdout: stdout.slice(-6000), stderr: stderr.slice(-2000) });
  }, TEST_TIMEOUT_MS);

  // [段2] 输出收集
  child.stdout.on('data', (d) => { stdout += d.toString(); });
  child.stderr.on('data', (d) => { stderr += d.toString(); });

  // [段3] 正常退出——退出码决定成败
  child.on('close', (code) => {
    if (settled) { return; }
    settled = true;
    clearTimeout(timer);
    settle(res, { ok: code === 0, code, stdout: stdout.slice(-6000), stderr: stderr.slice(-2000) });
  });

  // [段4] 启动失败——入口缺失等（不再挂起等待）
  child.on('error', (err) => {
    if (settled) { return; }
    settled = true;
    clearTimeout(timer);
    settle(res, { ok: false, code: -1, error: String((err && err.message) || err) });
  });
}

const server = http.createServer((req, res) => {
  if (req.url === '/health') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: true, service: 'ch4-frontend-test', port: PORT }));
    return;
  }
  // 测试路由（两别名同入口——直调 vitest run）
  if (req.url === '/api/test' || req.url === '/api/test/unit') {
    runTest(res);
    return;
  }
  res.writeHead(404, { 'Content-Type': 'text/plain' });
  res.end('not found: ' + req.url);
});

server.listen(PORT, '127.0.0.1', () => {
  console.log('[fe-test] 前端测试服务就绪: http://127.0.0.1:' + PORT);
});
