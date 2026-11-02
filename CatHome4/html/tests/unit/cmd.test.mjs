// tests/unit/cmd.test.mjs —— PowerShell 命令解读器单元测试（js/chat/lib/cmd.js 纯函数 · A174 恢复）
// 覆盖：单段/多段切分 + 引号感知 + 管道标记 + 指令类标识（tag）+ 宿主 CLI 子指令解码 + git 带值开关 + 段首锚定回归 + 未识别标注 + 参数截断兜底 + 重定向上报
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { expect, test, beforeAll } from 'vitest';

beforeAll(async () => {
  const src = await readFile(new URL('../../js/chat/lib/cmd.js', import.meta.url), 'utf-8');
  vm.runInThisContext(src, { filename: 'cmd.js' });
});

function args(command) {
  return JSON.stringify({ command: command });
}

// ── 单段：编译链（tag = 指令类 + 子命令）──
test('单段 dotnet build——指令类前缀 + 折叠行与逐段解读', () => {
  const r = cmdDecodeTool(args('dotnet build CatHome4.sln'));
  expect(r.brief).toBe('dotnet build · 编译 C# 项目 「CatHome4.sln」');
  expect(r.detail).toBe('🔎 命令意图（1 段）\n 1. dotnet build · 编译 C# 项目 「CatHome4.sln」');
  expect(r.truncated).toBe(false);
});

// ── 多段：分号切分 + 管道标记 ──
test('多段切分 + 管道标记', () => {
  const r = cmdDecodeTool(args('git status; Get-ChildItem -Recurse Flows | Select-Object -First 5'));
  expect(r.brief).toBe('git status · 查看仓库状态 等 3 段');
  expect(r.detail).toContain(' 1. git status · 查看仓库状态');
  expect(r.detail).toContain(' 2. Get-ChildItem · 列出目录 「Flows」');
  expect(r.detail).toContain(' 3. ⤷ Select-Object · 取字段/条数');
});

// ── 段首锚定——命令名只认段首（段中同名目录不误命中；2026-09-20 莎报缺陷）──
test('git 操作 mau 仓库——不被 mau 规则误命中', () => {
  const r = cmdDecodeTool(args('git -C C:\\Users\\ASUS\\Desktop\\Work\\Gitee\\mau status'));
  expect(r.brief).toBe('git status · 查看仓库状态');
});

test('段中 mau 目录 + 后续词——仍走段首命令规则', () => {
  const r = cmdDecodeTool(args('Get-ChildItem C:\\Work\\Gitee\\mau -Recurse'));
  expect(r.brief).toBe('Get-ChildItem · 列出目录 「C:\\Work\\Gitee\\mau」');
});

test('mau 可执行路径形态仍识别（锚定不误伤）', () => {
  expect(cmdDecodeTool(args('Mau-public\\Mau.exe verify corpus\\ch4\\PsCat\\ps_cat.mau')).brief)
    .toContain('mau verify · Mau 语料验证');
  expect(cmdDecodeTool(args('"C:\\Work\\Gitee\\mau\\Mau-public\\Mau.exe" check')).brief)
    .toContain('mau check · Mau 门禁检查');
});

// ── 宿主 CLI——子指令解码（部署/重载类可见性关键面）──
test('宿主 CLI --run 子指令解码（reload）', () => {
  const r = cmdDecodeTool(JSON.stringify({ command: '& CatHome4.exe --run "reload TextCat"' }));
  expect(r.brief).toBe('CatHome4 --run · 宿主 CLI 执行指令「热重载 Flow TextCat」');
});

test('宿主 CLI --selfcheck', () => {
  expect(cmdDecodeTool(args('CatHome4.exe --selfcheck')).brief).toBe('CatHome4 --selfcheck · 宿主启动自检');
});

// ── 一键部署链 / Mau 基座 CLI ──
test('SetUp 模式识别', () => {
  expect(cmdDecodeTool(args('SetUp.exe prepare --report r.json')).brief).toBe('SetUp prepare · 一键部署链·就地自举（prepare）');
  expect(cmdDecodeTool(args('SetUp.exe deploy C:\\out')).brief).toBe('SetUp deploy · 一键部署链·发布到目标目录（deploy）');
});

test('Mau 组翻译命令解读', () => {
  const r = cmdDecodeTool(args('Mau-public\\Mau.exe proj corpus\\ch4\\PsCat\\ps_cat.mauproj -o public\\src\\PsCat --build'));
  expect(r.brief).toContain('mau proj · Mau 组翻译+编译');
});

// ── 版本控制：带值开关在动作之前 ──
test('git 带值开关跳过——动作在开关之后', () => {
  const r = cmdDecodeTool(args('git -C C:\\repo status'));
  expect(r.brief).toBe('git status · 查看仓库状态');
});

test('git 子命令映射 + 目标参数', () => {
  expect(cmdDecodeTool(args('git log -1 --oneline')).brief).toBe('git log · 查看提交历史');
  expect(cmdDecodeTool(args('git add L1/Tree.md')).brief).toBe('git add · 暂存改动 「L1/Tree.md」');
});

// ── 引号感知 ──
test('引号内分号不分段', () => {
  const r = cmdDecodeTool(args('Get-Content "a;b.txt"'));
  expect(r.brief).toBe('Get-Content · 读取文件内容 「a;b.txt」');
});

// ── 重定向上报（写文件语义——历史命令可能含）──
test('重定向上报为写文件', () => {
  const r = cmdDecodeTool(args('Get-ChildItem > out.txt'));
  expect(r.brief).toBe('Get-ChildItem · 列出目录（结果写入文件 「out.txt」）');
});

// ── 补充规则（2026-09-20 收集表核销：Godot / 嵌套解释器 / 哈希与命令查询）──
test('Godot 控制台——版本 / 导入 / headless 运行', () => {
  expect(cmdDecodeTool(args('godot_console --version')).brief).toBe('godot console · 查看 Godot 版本');
  expect(cmdDecodeTool(args('C:\\Work\\Godot\\godot_console.exe --headless --path C:\\Cyber\\GodotView --import')).brief)
    .toBe('godot console · 导入 Godot 资源');
  expect(cmdDecodeTool(args('"C:\\Work\\Godot\\Godot_v4.7-stable_mono_win64_console.exe" --headless --path "C:\\Cyber\\GodotView" -- --cyber-smoke')).brief)
    .toBe('godot console · headless 运行 Godot 项目 「C:\\Cyber\\GodotView」');
});

test('嵌套解释器——cmd /c 与 powershell -Command 内联命令原样带入', () => {
  expect(cmdDecodeTool(args('cmd /c dir "C:\\Work\\Gitee\\mau\\CatHome4\\html\\js"')).brief)
    .toBe('cmd · 嵌套执行「dir "C:\\Work\\Gitee\\mau\\CatHome4\\html\\js"」');
  expect(cmdDecodeTool(args('powershell -Command "where.exe godot_console"')).brief)
    .toBe('powershell · 嵌套执行「where.exe godot_console」');
});

test('哈希与命令查询——补充规则', () => {
  expect(cmdDecodeTool(args('Get-FileHash -LiteralPath "C:\\Work\\a.js" -Algorithm SHA256')).brief)
    .toBe('Get-FileHash · 计算文件哈希 「C:\\Work\\a.js」');
  expect(cmdDecodeTool(args('Get-PSDrive -PSProvider FileSystem -Name')).brief)
    .toBe('Get-PSDrive · 列出驱动器');
  expect(cmdDecodeTool(args('Get-Command godot_console -ErrorAction SilentlyContinue')).brief)
    .toBe('Get-Command · 查询命令 「godot_console」');
});

// ── 未识别段——显式标注（不编造、不静默）──
test('未识别段显式标注', () => {
  const r = cmdDecodeTool(args('Whatever-Func -Foo bar'));
  expect(r.brief).toBe('未识别命令');
  expect(r.detail).toContain('❓ 未识别：Whatever-Func -Foo bar');
});

// ── 参数截断兜底（宿主实时 payload 截 200 字，JSON 未闭合）──
test('参数截断兜底——JSON 未闭合仍可解读', () => {
  const r = cmdDecodeTool('{"command":"git status; Get-ChildItem -Recurse');
  expect(r.truncated).toBe(true);
  expect(r.brief).toBe('git status · 查看仓库状态 等 2 段（截断）');
  expect(r.detail).toContain('参数被宿主截断');
});

// ── 兜底：无法解读返回 null（调用方回退覆盖表 headline / 骨架兜底）──
test('无 command 字段返回 null', () => {
  expect(cmdDecodeTool('{"path":"a.txt"}')).toBe(null);
  expect(cmdDecodeTool('')).toBe(null);
  expect(cmdDecodeTool(null)).toBe(null);
  expect(cmdDecodeTool('{"command":"   "}')).toBe(null);
});

// ── 补充规则（2026-09-23 收集表核销：dotnet 形态 / 表达式括号 / 外部命令）──
test('dotnet 兜底形态——dll 直跑与开关查询', () => {
  const r = cmdDecodeTool(args('dotnet C:\\Work\\Gitee\\mau\\Mau\\Mau.Development.Tests\\bin\\Debug\\net10.0\\Mau.Development.Tests.dll -class FormatTests'));
  expect(r.brief).toBe('dotnet Mau.Development.Tests.dll · 运行 .NET 程序集 「Mau.Development.Tests.dll」');
  expect(cmdDecodeTool(args('dotnet --version')).brief).toBe('dotnet --version · 查看 .NET SDK 版本');
  expect(cmdDecodeTool(args('dotnet --info')).brief).toBe('dotnet --info · 查看 .NET 环境信息（--info）');
});

test('表达式括号前缀——(Get-Item …).VersionInfo 形态识别', () => {
  expect(cmdDecodeTool(args('(Get-Item C:\\Work\\Gitee\\mau\\SetUp.exe).VersionInfo.ProductVersion')).brief)
    .toContain('Get-Item · 查看项属性');
});

test('文件系统补充规则——where.exe / Get-Location / Get-CimInstance / Expand-Archive / fc', () => {
  expect(cmdDecodeTool(args('where.exe ffmpeg')).brief).toBe('where · 定位可执行文件 「ffmpeg」');
  expect(cmdDecodeTool(args('Get-Location')).brief).toBe('Get-Location · 查看当前目录');
  expect(cmdDecodeTool(args('Get-CimInstance Win32_Process')).brief).toBe('Get-CimInstance · 查询系统信息 「Win32_Process」');
  expect(cmdDecodeTool(args('Expand-Archive -Path C:\\Temp\\ffmpeg-shared.zip -DestinationPath C:\\Temp\\ffmpeg-shared -Force')).brief)
    .toBe('Expand-Archive · 解压归档文件 「C:\\Temp\\ffmpeg-shared.zip」');
  expect(cmdDecodeTool(args('fc.exe /b a.txt b.txt')).brief).toBe('fc · 比较文件差异');
});

// ── JSON 转义还原——截断载荷兜底（cmdUnescapeJson 与 C# TextUtil.JsonUnescape 同规则，A137）──
test('截断载荷还原——\\b \\f 支持 + 未识别序列原样保留', () => {
  const r = cmdExtractCommand('{"command":"echo \\\"a\\\" \\u0041 \\b\\f \\q');
  expect(r.truncated).toBe(true);
  expect(r.command).toBe('echo "a" A \b\f \\q');
});

// ── 补充规则（2026-10-19 收集表核销：外部工具链 / 解释器家族 / 通用兜底）──
test('媒体工具——ffmpeg 版本与转码、ffprobe 时长与版本', () => {
  expect(cmdDecodeTool(args('ffmpeg -version')).brief).toBe('ffmpeg · 查看 ffmpeg 版本');
  expect(cmdDecodeTool(args('C:\\Tools\\ffmpeg\\bin\\ffmpeg.exe -y -v error -i C:\\a\\白云.mp4 -t 5 -c:v libx264 -an C:\\out\\短片.mp4')).brief)
    .toBe('ffmpeg · 处理媒体 「C:\\a\\白云.mp4」 → 「C:\\out\\短片.mp4」');
  expect(cmdDecodeTool(args('ffprobe -v error -show_entries format=duration -of csv=p=0 夕阳.mp4')).brief)
    .toBe('ffprobe · 读取媒体时长 「夕阳.mp4」');
  expect(cmdDecodeTool(args('ffprobe -version')).brief).toBe('ffprobe · 查看 ffprobe 版本');
});

test('包管理与校验——winget / certutil / electron', () => {
  expect(cmdDecodeTool(args('winget search Microsoft.DotNet.SDK.10')).brief).toBe('winget · 搜索软件包 「Microsoft.DotNet.SDK.10」');
  expect(cmdDecodeTool(args('winget install --id Microsoft.DotNet.SDK.10 --exact --accept-package-agreements')).brief)
    .toBe('winget · 安装软件包 「Microsoft.DotNet.SDK.10」');
  expect(cmdDecodeTool(args('certutil -hashfile C:\\Temp\\a.zip SHA256')).brief).toBe('certutil · 计算文件哈希 「C:\\Temp\\a.zip」');
  expect(cmdDecodeTool(args('electron.exe --version')).brief).toBe('electron · 查看 Electron 版本');
});

test('解释器家族——pwsh -File / -Command 与 powershell 兼容', () => {
  expect(cmdDecodeTool(args('pwsh -NoProfile -File C:\\Work\\wt3\\tools\\run-acceptance.ps1')).brief)
    .toBe('pwsh · 执行 PowerShell 脚本 「C:\\Work\\wt3\\tools\\run-acceptance.ps1」');
  expect(cmdDecodeTool(args('pwsh -NoProfile -Command $PSVersionTable.PSVersion.ToString()')).brief)
    .toBe('pwsh · 嵌套执行「$PSVersionTable.PSVersion.ToString()」');
  expect(cmdDecodeTool(args('powershell -Command "where.exe godot_console"')).brief)
    .toBe('powershell · 嵌套执行「where.exe godot_console」');
  expect(cmdDecodeTool(args('powershell.exe -NoProfile -File C:\\a\\b.ps1')).brief)
    .toBe('powershell · 执行 PowerShell 脚本 「C:\\a\\b.ps1」');
});

test('通用兜底——可执行 / 程序集 / 脚本（不再落「未识别命令」）', () => {
  expect(cmdDecodeTool(args('C:\\Work\\WorldTree3\\_build\\WT3Play.exe --headless --pack C:\\Work\\WT3\\夏令营.assets.pack --decode-probe')).brief)
    .toBe('wt3play · 运行可执行文件 「WT3Play.exe」 「C:\\Work\\WT3\\夏令营.assets.pack」');
  expect(cmdDecodeTool(args('.\\Akasha-public\\Akasha.exe index-status')).brief)
    .toBe('akasha · 运行可执行文件 「Akasha.exe」 「index-status」');
  expect(cmdDecodeTool(args('"C:\\Work\\Gitee\\mau\\Mau\\Mau.Development.Tests\\bin\\Debug\\net10.0\\Mau.Development.Tests.dll" -method Mau.Development.Tests.FormatTests'))
    .brief).toBe('mau.development.tests.dll · 运行 .NET 程序集 「Mau.Development.Tests.dll」');
  expect(cmdDecodeTool(args('C:\\Work\\WT3\\release.cmd --no-pause')).brief)
    .toBe('release.cmd · 执行脚本 「release.cmd」');
});

test('DOS find 与 Resolve-Path', () => {
  expect(cmdDecodeTool(args('find /c /v "" "C:\\Work\\Gitee\\mau\\public\\src\\BrowserCat\\FL_BrowserCat.cs"')).brief)
    .toBe('find · 统计文件行数 「C:\\Work\\Gitee\\mau\\public\\src\\BrowserCat\\FL_BrowserCat.cs」');
  expect(cmdDecodeTool(args('Resolve-Path C:\\Work\\Gitee\\mau')).brief).toBe('Resolve-Path · 解析路径 「C:\\Work\\Gitee\\mau」');
});
