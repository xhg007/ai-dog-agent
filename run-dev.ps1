# 强制控制台使用 UTF-8
chcp 65001 > $null
$OutputEncoding = [System.Text.Encoding]::UTF8
[Console]::InputEncoding  = [System.Text.Encoding]::UTF8

# 设置控制台编码为 UTF-8（无BOM，兼容性更好）
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
# 设置输出编码为 UTF-8
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)

$env:CGO_ENABLED=1
$env:WAILS_MCP=1
wails3 dev >> dev.log