# 强制控制台使用 UTF-8
chcp 65001 > $null
$OutputEncoding = [System.Text.Encoding]::UTF8
[Console]::InputEncoding  = [System.Text.Encoding]::UTF8
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)

# 允许从任意目录调用：后续相对路径都相对于项目根
Set-Location $PSScriptRoot

# DuckDB 动态库模式（vss 扩展需要；静态预构建库下 INSTALL vss 会崩溃）。
# CGO_ENABLED / CGO_CFLAGS / CGO_LDFLAGS / EXTRA_TAGS 由
# build/windows/Taskfile.yml 的 build:native 统一注入，无需在此重复设置。
# 前置：libs/duckdb-shared-libs-windows-amd64 内有 duckdb.dll / duckdb.lib / duckdb.h。
# 若缺少 duckdb.dll，会自动下载 DuckDB v2.0.1 的 Windows 共享库并解压到 libs。

$libDir = Join-Path $PSScriptRoot "libs\duckdb-shared-libs-windows-amd64"
$dllPath = Join-Path $libDir "duckdb.dll"

if (-not (Test-Path $dllPath)) {
    $url = "https://github.com/duckdb/duckdb/releases/download/v2.0.1/duckdb-shared-libs-windows-amd64.tar.gz"
    $token = [guid]::NewGuid().ToString("N")
    $archive = Join-Path $env:TEMP "duckdb-shared-libs-$token.tar.gz"

    Write-Host "未找到 duckdb.dll，正在下载 DuckDB 共享库..." -ForegroundColor Cyan
    try {
        # Windows PowerShell 5.1 默认可能未启用 TLS 1.2，GitHub 会拒绝连接。
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        $ProgressPreference = 'SilentlyContinue'
        Invoke-WebRequest -Uri $url -OutFile $archive -UseBasicParsing
    } catch {
        Write-Error "下载 DuckDB 共享库失败：$_"
        exit 1
    }
    if (-not (Test-Path $archive) -or (Get-Item $archive).Length -lt 1MB) {
        Write-Error "下载文件异常（可能不是有效的 tar.gz）：$archive"
        exit 1
    }
    if (-not (Get-Command tar -ErrorAction SilentlyContinue)) {
        Write-Error "系统缺少 tar，无法解压。请手动解压 $archive 到 $libDir"
        exit 1
    }

    New-Item -ItemType Directory -Force (Split-Path $libDir -Parent) | Out-Null
    if (Test-Path $libDir) { Remove-Item -Recurse -Force $libDir }
    New-Item -ItemType Directory -Force $libDir | Out-Null

    # 直接解压到目标目录：DuckDB v2.0.1 的包内直接是 duckdb.dll / .lib / 头文件。
    tar -xzf $archive -C $libDir
    if ($LASTEXITCODE -ne 0) {
        Write-Error "解压 DuckDB 共享库失败（tar 退出码 $LASTEXITCODE）：$archive"
        exit 1
    }

    # 兼容包内带顶层目录的结构：把顶层目录内容提升到 libDir。
    if (-not (Test-Path $dllPath)) {
        $nested = Get-ChildItem -Path $libDir -Directory | Select-Object -First 1
        if ($nested) {
            Get-ChildItem -Path $nested.FullName | Move-Item -Destination $libDir -Force
            Remove-Item -Recurse -Force $nested.FullName
        }
    }

    # 给杀毒软件/文件系统一点时间完成落盘，再判定。
    for ($i = 0; $i -lt 3 -and -not (Test-Path $dllPath); $i++) {
        Start-Sleep -Milliseconds 300
    }
    if (-not (Test-Path $dllPath)) {
        $names = (Get-ChildItem -Path $libDir | Select-Object -ExpandProperty Name) -join ", "
        Write-Error "解压后未找到 duckdb.dll。目录内容：$names；压缩包：$archive"
        exit 1
    }

    Remove-Item -Force $archive -ErrorAction SilentlyContinue
    Write-Host "DuckDB 共享库已安装到 $libDir" -ForegroundColor Green
}

$env:WAILS_MCP = 1
wails3 dev
