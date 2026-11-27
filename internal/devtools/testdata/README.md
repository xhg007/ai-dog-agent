# Golden 测试数据来源

本目录的 HTML 快照与 `.golden` 期望输出复制自
[`github.com/fpt/go-dev-mcp`](https://github.com/fpt/go-dev-mcp) 的
`internal/app/testdata/`（MIT License），用于离线验证文档解析逻辑，
测试过程不访问外网。

- `doc_*.html` / `*.golden`：pkg.go.dev 文档页与搜索结果
- `rustdoc_*.html` / `*.golden`：docs.rs 文档页与搜索结果
- `pydoc_*.html` / `*.golden`：docs.python.org 文档页与模块索引
- `readme_testify.*`：pkg.go.dev README 区块
