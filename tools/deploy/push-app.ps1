<#
.SYNOPSIS
    CH4 产物区推送（A368）——本地全产、投递到远端 /srv/inbox，之后由猫会话执行 take-app + restart-incr 落位。

.DESCRIPTION
    链路固定：本地 publish + 18 组组翻译 + html 对齐 → 打包 → scp → /srv/inbox → 由 root 跑 take-app.sh 校验后覆盖 public/app。
    远端使用顺序（不可反）：git pull → push-app → take-app → restart-incr。
    反序会让源码 mtime 新于产物，incr 哨兵直接判「产物过期」（假红）。

    本脚本只投递到 /srv/inbox——落位（public/app）与搬运（mauout）都需要 root：
    take-app 校验后覆盖产物区，restart-incr 再搬运行区。
    直接覆盖运行区会绕过原子切换与回滚，禁止。

.PARAMETER Remote
    SSH 别名（默认 cathome，见 ~/.ssh/config）。

.PARAMETER SkipBuild
    跳过宿主 publish（只在改过 Flows / html 时用——产物区宿主 dll 保持上次）。

.PARAMETER SkipFlows
    跳过组翻译（不改语料 / 积木时用）。

.EXAMPLE
    powershell -File tools\deploy\push-app.ps1
#>
param(
    [string]$Remote = "cathome",
    [switch]$SkipBuild,
    [switch]$SkipFlows
)

$ErrorActionPreference = "Stop"

# [段0] 仓库根与产物区——本脚本位于 <仓库根>/tools/deploy
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$appDir = Join-Path $repoRoot "public\app"
$mauExe = Join-Path $repoRoot "Mau-public\Mau.exe"
$tarPath = Join-Path $repoRoot "CatTemp\ch4-app.tar.gz"
$remoteTar = "/srv/inbox/ch4-app.tar.gz"

if (!(Test-Path $mauExe))
{
    throw "基座 CLI 缺失：$mauExe——先跑 mau-setup prepare"
}
if (!(Test-Path (Join-Path $repoRoot "CatTemp")))
{
    New-Item -ItemType Directory -Path (Join-Path $repoRoot "CatTemp") | Out-Null
}
Write-Host ("[push-app] 仓库根：" + $repoRoot)
Write-Host ("[push-app] 基座 CLI 时间戳：" + (Get-Item $mauExe).LastWriteTime.ToString("yyyy-MM-dd HH:mm:ss") + "（应与源码同批；不一致先跑 mau-setup prepare）")

# [段1] 宿主 publish → public/app（交叉构建 linux-x64，Windows 上可产 Linux apphost）
if ($SkipBuild)
{
    Write-Host "[push-app] 跳过宿主 publish"
}
else
{
    Write-Host "[push-app] publish CatHome4 -> public/app"
    & dotnet publish (Join-Path $repoRoot "CatHome4\CatHome4.csproj") -c Debug -r linux-x64 --self-contained false -o $appDir --nologo
    if ($LASTEXITCODE -ne 0)
    {
        throw "宿主 publish 失败（退出码 $LASTEXITCODE）"
    }
}

# [段2] 组翻译——逐组重翻，dll 落 public/app/Flows
if ($SkipFlows)
{
    Write-Host "[push-app] 跳过组翻译"
}
else
{
    $projects = Get-ChildItem -Path (Join-Path $repoRoot "corpus\ch4") -Filter "*.mauproj" -Recurse -File
    Write-Host ("[push-app] 组翻译 " + $projects.Count + " 组")
    foreach ($proj in $projects)
    {
        $groupName = $proj.BaseName
        $outDir = Join-Path $repoRoot ("public\src\" + $groupName)
        & $mauExe proj $proj.FullName -o $outDir --build
        if ($LASTEXITCODE -ne 0)
        {
            throw ("组翻译失败：" + $groupName + "（退出码 " + $LASTEXITCODE + "）")
        }
    }
}

# [段3] 外观层对齐——源区 CatHome4/html → public/app/html（整棵镜像，删多余）
$htmlSource = Join-Path $repoRoot "CatHome4\html"
$htmlTarget = Join-Path $appDir "html"
if (Test-Path $htmlSource)
{
    Write-Host "[push-app] html 源区 → 产物区"
    # 排除 tests/node_modules——Windows 本机依赖 66 MB 且含平台相关二进制（lightningcss-win32-x64-msvc 等），
    # 推给 Linux 只增体积并埋「能跑单元测试、浏览器用例必缺」的假绿（A368 缺口）
    & robocopy $htmlSource $htmlTarget /MIR /XD node_modules /NFL /NDL /NJH /NJS /NP | Out-Null
    if ($LASTEXITCODE -ge 8)
    {
        throw ("html 镜像失败（robocopy 退出码 " + $LASTEXITCODE + "）")
    }
}

# [段3b] 版本落盘——version.txt（与 SetUp deploy 同规：读 Directory.Build.props 单点，无换行）
# 缺口补正（2026-11-15）：publish 产物不含 version.txt，包内缺此项时远端切换后升级比对面即消失
$propsPath = Join-Path $repoRoot "Directory.Build.props"
$versionText = ""
if (Test-Path $propsPath)
{
    $versionMatch = Select-String -Path $propsPath -Pattern "<Version>([^<]+)</Version>"
    if ($versionMatch)
    {
        $versionText = $versionMatch.Matches[0].Groups[1].Value
    }
}
if ($versionText.Length -gt 0)
{
    Set-Content -Path (Join-Path $appDir "version.txt") -Value $versionText -NoNewline -Encoding ascii
    Write-Host ("[push-app] version.txt：" + $versionText)
}
else
{
    Write-Host "[push-app] 警告：未能从 Directory.Build.props 读到 Version——version.txt 未落盘"
}

# [段3c] 构建指纹校验（2026-11-16 · 方案 C）——推出去的产物必须自带与已推送源码一致的指纹
# 判据：接收端 incr 只比「产物 commit == 仓库 HEAD」，任何一条不满足都会在远端被拒
$buildInfoPath = Join-Path $appDir "buildinfo.txt"
if (!(Test-Path $buildInfoPath))
{
    throw "产物缺构建指纹（$buildInfoPath）——先跑 mau-setup prepare"
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
    throw ("构建指纹 commit 非法（「" + $biCommit + "」）——先跑 mau-setup prepare")
}
if ($biDirty -ne "false")
{
    throw ("产物基于未提交源码（dirty=" + $biDirty + "）——先提交并重跑 prepare")
}
$headCommit = (& git -C $repoRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $headCommit.Length -ne 40)
{
    throw "取本地 git HEAD 失败"
}
if ($biCommit -ne $headCommit)
{
    throw ("产物 commit=" + $biCommit.Substring(0, 8) + "，本地 HEAD=" + $headCommit.Substring(0, 8) + "——产物非当前源码所编，重跑 prepare")
}
$remoteRefs = (& git -C $repoRoot branch -r --contains $headCommit)
if ($LASTEXITCODE -ne 0 -or $remoteRefs.Length -eq 0)
{
    throw ("源码未推送（本地 HEAD " + $headCommit.Substring(0, 8) + " 不在任何远端分支）——先 git push 源码，接收端才 pull 得到对应提交")
}
Write-Host ("[push-app] 构建指纹：commit=" + $biCommit.Substring(0, 8) + " dirty=false（与本地 HEAD 一致且已在远端）")

# [段4] 打包——tar 单文件传输（Windows tar 不保留 Unix 权限位，远端落位后显式 chmod）
if (Test-Path $tarPath)
{
    Remove-Item -Path $tarPath -Force
}
& tar -czf $tarPath -C $appDir .
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

# [段6] 落位交由远端 root 侧接管（2026-11-16 甲案）
# 本脚本只投递——public/app 的覆盖与执行位设置都在 take-app.sh 内（root）；
# ubuntu 侧既无权改产物区，也无权读回自证，故不再内联 ssh 落位与核验。
Write-Host "[push-app] 包已投 /srv/inbox —— 等猫会话执行 take-app + restart-incr"
