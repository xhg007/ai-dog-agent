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

$env:WAILS_MCP = 1

wails3 dev > dev.log
