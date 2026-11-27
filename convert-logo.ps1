# 将 SVG logo 转换为 Wails3 应用图标，并同步前端 logo/favicon。
#
# 用法：
#   .\convert-logo.ps1                          # 使用根目录 logo.svg
#   .\convert-logo.ps1 -Svg assets\logo-b.svg   # 指定其他 SVG
#   .\convert-logo.ps1 -SkipSyso                # 只生成 PNG/ICO/ICNS
#   .\convert-logo.ps1 -NoBackup                # 不备份旧图标
#
# 产物：
#   build/appicon.png            1024x1024 透明 PNG（Linux / 通用）
#   build/windows/icon.ico       256/128/64/48/32/16
#   build/darwin/icons.icns      macOS 图标
#   wails_windows_<arch>.syso    Windows exe 资源
#   frontend/public/logo.svg     应用内 logo 与 favicon
#
# 依赖：ImageMagick（magick）、wails3 CLI。

param(
    [string]$Svg = "logo.svg",
    [string]$Arch = "amd64",
    [switch]$SkipSyso,
    [switch]$SkipFrontend,
    [switch]$NoBackup
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

function Fail($message) {
    Write-Error $message
    exit 1
}

if (-not (Test-Path $Svg)) {
    Fail "找不到输入 SVG：$Svg"
}
if (-not (Get-Command magick -ErrorAction SilentlyContinue)) {
    Fail "未安装 ImageMagick（magick），无法把 SVG 渲染为 PNG。"
}
if (-not (Get-Command wails3 -ErrorAction SilentlyContinue)) {
    Fail "未找到 wails3 CLI。"
}

$buildDir = Join-Path $PSScriptRoot "build"
$png = Join-Path $buildDir "appicon.png"
$ico = Join-Path $buildDir "windows\icon.ico"
$icns = Join-Path $buildDir "darwin\icons.icns"
$syso = Join-Path $PSScriptRoot "wails_windows_$Arch.syso"
$frontendLogo = Join-Path $PSScriptRoot "frontend\public\logo.svg"

# 备份旧图标，方便在多套 logo 之间切换。
if (-not $NoBackup) {
    $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
    $backupDir = Join-Path $PSScriptRoot ".icon-backups\$stamp"
    New-Item -ItemType Directory -Force $backupDir | Out-Null
    foreach ($f in @($png, $ico, $icns)) {
        if (Test-Path $f) {
            Copy-Item $f (Join-Path $backupDir (Split-Path $f -Leaf)) -Force
        }
    }
    Write-Host "旧图标已备份到 $backupDir" -ForegroundColor DarkGray
}

# 1. SVG -> 1024x1024 透明 PNG
Write-Host "渲染 SVG -> 1024x1024 PNG..." -ForegroundColor Cyan
magick -background none -density 384 $Svg -alpha set -resize 1024x1024 -depth 8 "PNG32:$png"
if ($LASTEXITCODE -ne 0) {
    Fail "ImageMagick 渲染失败（exit $LASTEXITCODE）"
}

# 2. PNG -> Windows ICO / macOS ICNS（在 build 目录运行，路径与 Taskfile 一致）
Write-Host "生成 Windows ICO / macOS ICNS..." -ForegroundColor Cyan
Push-Location $buildDir
wails3 generate icons -input appicon.png -windowsfilename windows/icon.ico -macfilename darwin/icons.icns
$code = $LASTEXITCODE
Pop-Location
if ($code -ne 0) {
    Fail "wails3 generate icons 失败（exit $code）"
}

# 3. Windows syso（exe/任务栏图标）
if (-not $SkipSyso) {
    Write-Host "生成 Windows syso..." -ForegroundColor Cyan
    Push-Location $buildDir
    wails3 generate syso -arch $Arch -icon windows/icon.ico -manifest windows/wails.exe.manifest -info windows/info.json -out "../wails_windows_$Arch.syso"
    $code = $LASTEXITCODE
    Pop-Location
    if ($code -ne 0) {
        Fail "wails3 generate syso 失败（exit $code）"
    }
}

# 4. 同步前端 logo / favicon（index.html 与 Sidebar 引用 /logo.svg）
if (-not $SkipFrontend) {
    Copy-Item $Svg $frontendLogo -Force
    Write-Host "前端 logo 已更新：$frontendLogo" -ForegroundColor Cyan
}

# 5. 校验产物
$outputs = @($png, $ico, $icns)
if (-not $SkipSyso) { $outputs += $syso }
foreach ($f in $outputs) {
    if (-not (Test-Path $f)) {
        Fail "产物缺失：$f"
    }
}

Get-Item $outputs | Select-Object Name, Length, LastWriteTime | Format-Table -AutoSize
Write-Host "完成。重新构建应用后，新图标生效。" -ForegroundColor Green
