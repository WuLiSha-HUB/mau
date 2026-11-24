<#
.SYNOPSIS
    CH4 Linux 产物推送（A368）——调 SetUp publish-linux 产 public/app-linux，打包投递 /srv/inbox。开发机侧到此为止：落位（take-app）与上线（restart-incr）归远端自己。

.DESCRIPTION
    开发机侧：SetUp publish-linux（门禁 + 组翻译 + linux-x64 交叉发布 + 指纹，不自增版本）→ 打包 → scp → /srv/inbox（到此结束）。
    远端侧（归远端自己执行，顺序不可反）：git pull 到与包同源 → take-app 覆盖 public/app → restart-incr 搬运行区。
    开发机不得用 ssh 代跑远端脚本或指令（判据 → Project/CatHomeLinux/deploy-linux.md §1.0）。
    反序会让源码 mtime 新于产物，incr 哨兵直接判「产物过期」（假红）。

    产物端归 SetUp 单一实现——本脚本不重复 publish / 组翻译 / html 镜像；
    版本号不在本链路自增：代际由本地 prepare 产生，Linux 链只是同一份源码的 Linux 形态（同上源码不产第二代际）。

    本脚本只投递到 /srv/inbox——落位（public/app）与搬运（mauout）都需要 root：
    take-app 校验后覆盖产物区，restart-incr 再搬运行区。
    直接覆盖运行区会绕过原子切换与回滚，禁止。

.PARAMETER Remote
    SSH 别名（默认 cathome，见 ~/.ssh/config）。

.PARAMETER SkipSetup
    跳过 SetUp publish-linux（产物区已是当前源码所产时用——省一次 build/test/组翻译）。

.EXAMPLE
    powershell -File tools\deploy\push-app.ps1
#>
param(
    [string]$Remote = "cathome",
    [switch]$SkipSetup
)

$ErrorActionPreference = "Stop"

# [段0] 仓库根与 Linux 产物区——本脚本位于 <仓库根>/tools/deploy
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$appLinuxDir = Join-Path $repoRoot "public\app-linux"
$setupExe = Join-Path $repoRoot "SetUp.exe"
$tarPath = Join-Path $repoRoot "CatTemp\ch4-app.tar.gz"
$remoteTar = "/srv/inbox/ch4-app.tar.gz"

if (!(Test-Path $setupExe))
{
    throw "SetUp.exe 缺失：$setupExe——仓库根须带自举部署器（dotnet publish SetUp/SetUp.csproj -r win-x64 -o <临时目录> 后复制到仓库根）"
}
if (!(Test-Path (Join-Path $repoRoot "CatTemp")))
{
    New-Item -ItemType Directory -Path (Join-Path $repoRoot "CatTemp") | Out-Null
}
Write-Host ("[push-app] 仓库根：" + $repoRoot)

# [段0b] 工作目录切到仓库根（2026-11-19）——SetUp 要求 cwd = 仓库根（据此找 Mau.sln 与相对路径）
# 从任意 cwd 调起时不切换会直接失败（判例：宿主数据根调起 → 「当前目录未找到 Mau.sln」）
Set-Location $repoRoot

# [段1] 产物端——SetUp publish-linux（单一实现：门禁 build/test + 组翻译 + linux-x64 交叉发布 + 指纹；不自增版本）
if ($SkipSetup)
{
    Write-Host "[push-app] 跳过 SetUp publish-linux（产物区须已是当前源码所产）"
}
else
{
    Write-Host "[push-app] SetUp publish-linux -> public\app-linux"
    $setupReport = Join-Path $repoRoot "CatTemp\publish_linux_report.json"
    & $setupExe publish-linux --report $setupReport
    if ($LASTEXITCODE -ne 0)
    {
        throw ("SetUp publish-linux 失败（退出码 " + $LASTEXITCODE + "）——报告：" + $setupReport)
    }
}

# [段2] 外观层镜像与 version.txt 由 SetUp 段5d / 5e 负责——本脚本不重复实现（单一真相源）

# [段3c] 构建指纹校验（2026-11-16 · 方案 C）——推出去的产物必须自带与已推送源码一致的指纹
# 判据：接收端 incr 只比「产物 commit == 仓库 HEAD」，任何一条不满足都会在远端被拒
$buildInfoPath = Join-Path $appLinuxDir "buildinfo.txt"
if (!(Test-Path $buildInfoPath))
{
    throw "产物缺构建指纹（$buildInfoPath）——先跑 SetUp publish-linux（或去掉 -SkipSetup）"
}
$buildInfo = @{}
foreach ($line in (Get-Content $buildInfoPath))
{
    $idx = $line.IndexOf("=")
    if ($idx -gt 0)
    {
        $buildInfo[$line.Substring(0, $idx).Trim()] = $line.Substring($idx + 1).Trim()
    }
}
$biCommit = ""
if ($buildInfo.ContainsKey("commit")) { $biCommit = $buildInfo["commit"] }
$biDirty = ""
if ($buildInfo.ContainsKey("dirty")) { $biDirty = $buildInfo["dirty"] }
if ($biCommit.Length -ne 40)
{
    throw ("构建指纹 commit 非法（「" + $biCommit + "」）——先跑 SetUp publish-linux")
}
if ($biDirty -ne "false")
{
    throw ("产物基于未提交源码（dirty=" + $biDirty + "）——先提交并重跑 SetUp publish-linux")
}
$headCommit = (& git -C $repoRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $headCommit.Length -ne 40)
{
    throw "取本地 git HEAD 失败"
}
if ($biCommit -ne $headCommit)
{
    throw ("产物 commit=" + $biCommit.Substring(0, 8) + "，本地 HEAD=" + $headCommit.Substring(0, 8) + "——产物非当前源码所编，重跑 SetUp publish-linux")
}
$remoteRefs = (& git -C $repoRoot branch -r --contains $headCommit)
if ($LASTEXITCODE -ne 0 -or $remoteRefs.Length -eq 0)
{
    throw ("源码未推送（本地 HEAD " + $headCommit.Substring(0, 8) + " 不在任何远端分支）——先 git push 源码，接收端才 pull 得到对应提交")
}
Write-Host ("[push-app] 构建指纹：commit=" + $biCommit.Substring(0, 8) + " dirty=false（与本地 HEAD 一致且已在远端）")

# [段4] 打包 Linux 产物区——tar 单文件传输（Windows tar 不保留 Unix 权限位，远端 take-app 做权限归一）
if (Test-Path $tarPath)
{
    Remove-Item -Path $tarPath -Force
}
& tar -czf $tarPath -C $appLinuxDir .
if ($LASTEXITCODE -ne 0)
{
    throw "打包失败（退出码 $LASTEXITCODE）"
}
Write-Host ("[push-app] 包：" + $tarPath + " " + [math]::Round((Get-Item $tarPath).Length / 1MB, 1) + " MB")

# [段5] 传输
& scp $tarPath ($Remote + ":" + $remoteTar)
if ($LASTEXITCODE -ne 0)
{
    throw "scp 失败（退出码 $LASTEXITCODE）"
}

# [段6] 落位与上线归远端（2026-11-18 莎定）——本脚本止于投递
# 开发机侧链路到此结束：public/app 覆盖（take-app.sh）与运行区切换（restart-incr）
# 一律由远端自己执行；开发机不得用 ssh 代跑远端脚本或指令
# （判据与流程 → Project/CatHomeLinux/deploy-linux.md §1.0）。
Write-Host "[push-app] 包已投 /srv/inbox —— 开发机侧结束，后续 take-app + restart-incr 归远端"
