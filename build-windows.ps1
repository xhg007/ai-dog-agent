# 构建 Windows 版（DuckDB 动态库模式）。
#
# 为什么用动态库：静态预构建库（windows_amd64_mingw）下 INSTALL vss 会原生崩溃；
# 动态链接 duckdb.dll（DuckDB 2.0.1）后 vss 的 INSTALL/LOAD/HNSW 均已验证可用。
#
# 前置：libs/duckdb-shared-libs-windows-amd64 内有 duckdb.dll / duckdb.lib / duckdb.h。
# 该目录不入仓（.gitignore），需要从发布包或 go-duckdb-demo 复制。

chcp 65001 > $null
$OutputEncoding = [System.Text.Encoding]::UTF8
[Console]::InputEncoding  = [System.Text.Encoding]::UTF8
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)

# 允许从任意目录调用：后续相对路径都相对于项目根
Set-Location $PSScriptRoot

$libDir = (Resolve-Path ".\libs\duckdb-shared-libs-windows-amd64" -ErrorAction Stop).Path
$env:CGO_ENABLED = "1"
$env:CGO_CFLAGS = "-I$libDir"
$env:CGO_LDFLAGS = "-L$libDir -lduckdb"
$env:PATH = "$libDir;$env:PATH"
$env:EXTRA_TAGS = "duckdb_use_lib"

wails3 build
if ($LASTEXITCODE -ne 0) {
    Write-Error "构建失败（exit $LASTEXITCODE）"
    exit $LASTEXITCODE
}

# Windows 加载器在进程启动时就要解析 duckdb.dll 的导入表，
# 因此 DLL 必须与 exe 同目录（或启动前已在 PATH 中），不能靠 init 里释放。
Copy-Item (Join-Path $libDir "duckdb.dll") ".\bin\duckdb.dll" -Force
Write-Output "已构建 bin\ai-dog-agent.exe，并复制 duckdb.dll 到 bin\"
