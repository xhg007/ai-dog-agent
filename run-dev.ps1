# 强制控制台使用 UTF-8
chcp 65001 > $null
$OutputEncoding = [System.Text.Encoding]::UTF8
[Console]::InputEncoding  = [System.Text.Encoding]::UTF8
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)

# 允许从任意目录调用：后续相对路径都相对于项目根
Set-Location $PSScriptRoot

# DuckDB 动态库模式：让 vss 扩展可用（静态预构建库下 INSTALL vss 会崩溃）。
# 前置：libs/duckdb-shared-libs-windows-amd64 内有 duckdb.dll / duckdb.lib / duckdb.h。
$libDir = (Resolve-Path ".\libs\duckdb-shared-libs-windows-amd64" -ErrorAction Stop).Path
$env:CGO_ENABLED = "1"
$env:CGO_CFLAGS = "-I$libDir"
$env:CGO_LDFLAGS = "-L$libDir -lduckdb"
$env:PATH = "$libDir;$env:PATH"
$env:EXTRA_TAGS = "duckdb_use_lib"

$env:WAILS_MCP = 1
wails3 dev > dev.log
