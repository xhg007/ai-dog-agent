module ai-dog-agent

// DuckDB 固定使用动态库模式（-tags=duckdb_use_lib），go.mod 不再包含各平台预编译静态库。
// 不要运行 go mod tidy / go mod vendor：它们会把 go.duckdb.org/libduckdb/* 重新写回（约 750MB）。
// 所有 go 命令（build/test/vet）与 IDE/gopls 都必须带 -tags=duckdb_use_lib。

go 1.26.0

require (
	github.com/cloudwego/eino v0.9.23
	github.com/cloudwego/eino-ext/components/model/openai v0.1.13
	github.com/duckdb/duckdb-go/v2 v2.20001.0
	github.com/eino-contrib/jsonschema v1.0.3
	github.com/google/uuid v1.6.0
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/smacker/go-tree-sitter v0.0.0-20240827094217-dd81d9e9be82
	github.com/wailsapp/wails/v3 v3.0.0
	github.com/yuin/goldmark v1.7.17
	golang.org/x/mod v0.42.0
	golang.org/x/net v0.63.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/apache/arrow-go/v18 v18.5.2 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/bytedance/gopkg v0.1.3 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/cloudwego/eino-ext/libs/acl/openai v0.1.17 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/duckdb/duckdb-go-bindings v0.20001.0 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/evanphx/json-patch v0.5.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/goccy/go-json v0.12.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/google/flatbuffers v25.12.19+incompatible // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/goph/emperror v0.17.2 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/klauspost/cpuid/v2 v2.4.2 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/meguminnnnnnnnn/go-openai v0.1.2 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/nikolalohinski/gonja v1.5.3 // indirect
	github.com/pelletier/go-toml/v2 v2.0.9 // indirect
	github.com/pierrec/lz4/v4 v4.1.33 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/slongfield/pyfmt v0.0.0-20220222012616-ea85ff4c361f // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/wk8/go-ordered-map/v2 v2.1.8 // indirect
	github.com/yargevad/filepathx v1.0.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.duckdb.org/libduckdb/darwin-amd64 v0.20001.0 // indirect
	go.duckdb.org/libduckdb/darwin-arm64 v0.20001.0 // indirect
	go.duckdb.org/libduckdb/linux-amd64 v0.20001.0 // indirect
	go.duckdb.org/libduckdb/linux-arm64 v0.20001.0 // indirect
	go.duckdb.org/libduckdb/windows-amd64 v0.20001.0 // indirect
	golang.org/x/arch v0.11.0 // indirect
	golang.org/x/exp v0.0.0-20260410095643-746e56fc9e2f // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/telemetry v0.0.0-20261009151841-97e8ea77aa2a // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
	golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da // indirect
)
