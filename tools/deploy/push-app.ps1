<#
.SYNOPSIS
    CH4 产物区推送（A368）——本地全产、整套覆盖远端 public/app，之后由 restart-incr 搬运到运行区。

.DESCRIPTION
    链路固定：本地 publish + 18 组组翻译 + html 对齐 → 打包 → scp → 远端「旧目录改名 .prev → 解包」。
    远端使用顺序（不可反）：git pull → push-app → restart-incr。
    反序会让源码 mtime 新于产物，incr 哨兵直接判「产物过期」（假红）。

    落点固定为 <远端仓库根>/public/app（产物区）。运行区（mauout）只由 incr 搬运——
    直接覆盖运行区会绕过原子切换与回滚，禁止。

.PARAMETER Remote
    SSH 别名（默认 cathome，见 ~/.ssh/config）。

.PARAMETER RemoteRepo
    远端仓库根（默认 /cathome4/mau）。

.PARAMETER SkipBuild
    跳过宿主 publish（只在改过 Flows / html 时用——产物区宿主 dll 保持上次）。

.PARAMETER SkipFlows
    跳过组翻译（不改语料 / 积木时用）。

.EXAMPLE
    powershell -File tools\deploy\push-app.ps1
#>
param(
    [string]$Remote = "cathome",
    [string]$RemoteRepo = "/cathome4/mau",
    [switch]$SkipBuild,
    [switch]$SkipFlows
)

$ErrorActionPreference = "Stop"

# [段0] 仓库根与产物区——本脚本位于 <仓库根>/tools/deploy
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$appDir = Join-Path $repoRoot "public\app"
$mauExe = Join-Path $repoRoot "Mau-public\Mau.exe"
$tarPath = Join-Path $repoRoot "CatTemp\ch4-app.tar.gz"
$remoteTar = "/tmp/ch4-app.tar.gz"

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

# [段6] 远端落位——旧目录改名 .prev（保留一份可回退）→ 解包 → 执行位
function Invoke-Remote
{
    param([string]$CommandText)
    & ssh $Remote $CommandText
    if ($LASTEXITCODE -ne 0)
    {
        throw ("远端执行失败：" + $CommandText)
    }
}

Write-Host "[push-app] 远端落位 public/app"
Invoke-Remote ("rm -rf " + $RemoteRepo + "/public/app.prev")
Invoke-Remote ("if [ -d " + $RemoteRepo + "/public/app ]; then mv " + $RemoteRepo + "/public/app " + $RemoteRepo + "/public/app.prev; fi")
Invoke-Remote ("mkdir -p " + $RemoteRepo + "/public/app")
Invoke-Remote ("tar -xzf " + $remoteTar + " -C " + $RemoteRepo + "/public/app")
Invoke-Remote ("chmod 0755 " + $RemoteRepo + "/public/app/CatHome4")

# [段7] 自证——产物时间戳 + Flows 数量（判据面：后续 incr 搬的就是这一批）
Write-Host "[push-app] 远端产物："
Invoke-Remote ("ls -l --time-style=+%Y-%m-%d%t%H:%M:%S " + $RemoteRepo + "/public/app/CatHome4")
Invoke-Remote ("ls " + $RemoteRepo + "/public/app/Flows | wc -l")

Write-Host "[push-app] 完成——下一步：restart-incr（不传 target，缺省落受控根 mauout）"
