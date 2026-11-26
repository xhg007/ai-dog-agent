# 向量文档搜索（VSS）评估与动态库落地

日期：2026-11-26
状态：动态库方案已落地，vss 扩展已实测可用（尚未接入向量检索功能）

## 背景

统计持久层用 DuckDB（任务 / 事件 / Token 用量 / 检查点）。DuckDB 的分析能力
和单机查询性能也适合承载未来的「向量文档搜索」：把代码/文档分块与 embedding
存进 DuckDB，用 VSS 扩展的 HNSW 索引做近邻检索。

本文件记录：为什么静态库方案不可用、动态库方案如何验证成功、以及落地后的
构建/分发约束。

## 实测结论（Windows + 嵌入式驱动）

环境：Windows amd64、`github.com/duckdb/duckdb-go/v2 v2.20001.0`（DuckDB 2.0.1）、
MSYS2 GCC16。

| 方案 | 结果 |
|---|---|
| 静态预构建库（2.0.0，`windows_amd64_mingw`）+ `INSTALL vss` | **原生访问冲突 0xc0000005，整个进程崩溃** |
| 静态库 + 关闭自动安装后 `LOAD vss`（未安装） | 安全返回 `IO Error: ...vss.duckdb_extension not found` |
| 驱动降级到 1.5（`v2.10506.0`） | **更糟**：`sql.Open` 在 `duckdb_open_ext` 崩溃，连 `:memory:` 都打不开；已回滚 |
| **动态库**（`-tags=duckdb_use_lib` + DuckDB 2.0.1 `duckdb.dll`） | **成功**：`INSTALL vss` → `LOAD vss` → `CREATE INDEX ... USING HNSW` → `array_distance` 检索，返回正确最近邻 |

结论：VSS 在这个 Windows 环境下**只能走动态库**。静态预构建库（mingw）与官方扩展
ABI 不匹配，`INSTALL` 会直接拖垮进程；1.5 的预构建库与当前工具链也不兼容。

## 已落地的方案

- 依赖升到 `duckdb-go/v2 v2.20001.0`（与 2.0.1 共享库匹配）。
- 动态库放在 `libs/`（**不入仓**，`.gitignore`）：
  - Windows：`duckdb-shared-libs-windows-amd64/`（`duckdb.dll` 59MB、`duckdb.lib`、头文件）
  - Linux：`duckdb-shared-libs-linux-amd64/`（`libduckdb.so` 92MB + 头文件）
  - macOS：`duckdb-shared-libs-osx-amd64/`（`libduckdb.dylib` 65MB + 头文件）
- 三个平台的 `build/{windows,linux,darwin}/Taskfile.yml` 已按平台设置
  `DUCKDB_LIB_DIR`（任务级 vars，避免 include 变量互相覆盖）、`CGO_CFLAGS`、
  `CGO_LDFLAGS`、`EXTRA_TAGS=duckdb_use_lib`：
  - Linux 加 `-Wl,-rpath,$ORIGIN`，macOS 加 `-Wl,-rpath,@loader_path`。
  - `wails3 build` 后把共享库复制到 `bin/`；macOS 的 `.app` 也复制进 `Contents/MacOS`。
- `build-windows.ps1` / `run-dev.ps1`（Windows）与 `build.sh` / `run-dev.sh`
  （Linux/macOS）提供显式环境；`wails3 dev` 不经过 Taskfile，必须用这些脚本。
- **共享库必须与可执行文件同目录**（或启动前在 PATH / `LD_LIBRARY_PATH` /
  `DYLD_LIBRARY_PATH` 中）：Windows 加载器在进程启动时就解析导入表，早于 Go `init`。
  demo 里“内嵌 DLL + init 释放”的做法对直接运行无效（实测 `0xC0000135`），不要照搬。
- 验证：Windows 上 `wails3 build` 产物 `bin/ai-dog-agent.exe`（31MB）+
  `bin/duckdb.dll` 可正常启动；动态库模式下 `go test -tags=duckdb_use_lib ./internal/...` 全绿。
  Linux/macOS 的 Taskfile 已配置，但未在本机（Windows）实机验证。

## VSS 的硬约束（来自官方文档）

- VSS 是 **experimental** 扩展；HNSW 语法：
  `CREATE INDEX ... ON t USING HNSW (vec) WITH (metric = 'cosine')`，
  查询形如 `ORDER BY array_distance(vec, [...]::FLOAT[N]) LIMIT k`。
- **磁盘库默认不能建 HNSW**，除非 `SET hnsw_enable_experimental_persistence = true`。
- 官方明确：自定义索引的 **WAL 恢复尚未正确实现**，异常关闭（崩溃/强杀/断电）可能
  导致索引损坏甚至数据丢失，**不建议在生产环境使用**。
- 索引持久化是「每次 checkpoint 全量序列化」，重启后整体反序列化；更新/删除较多时
  需要 `PRAGMA hnsw_compact_index` 或重建索引。
- 扩展版本与 DuckDB 版本、平台、构建方式绑定；可用 `INSTALL ext VERSION 'x'` 固定，
  `SELECT extension_name, extension_version FROM duckdb_extensions()` 查询。

这与桌面应用的现实冲突：进程可能被强杀，统计库已经做了 WAL/主文件自愈；
如果向量索引也放在同一个库上，会放大索引损坏风险。

## 设计结论

1. **向量数据与统计库分离**：不要往 `stats.duckdb` 加 HNSW 索引；用独立库
   （例如 `vectors.duckdb`）或独立进程。
2. **不依赖 HNSW 持久化**：持久化「文档 + 分块 + embedding」原始表，HNSW 索引
   按需在内存重建，避开 `hnsw_enable_experimental_persistence` 的实验性路径。
3. **扩展加载仍应隔离**：动态库让主进程可以 `INSTALL/LOAD`，但 `INSTALL` 依赖网络、
   `LOAD` 失败可能来自扩展/版本不匹配。建议安装包里预置扩展，运行时只 `LOAD`，
   并关闭 `autoinstall_known_extensions` / `autoload_known_extensions`。
4. **embedding 维度固定**：`FLOAT[N]` 在建表时确定，不同模型分表或统一维度。
5. **离线优先**：扩展与模型随安装包预置；运行时不做隐式 `INSTALL`。

## 待验证 / 开放问题

- 动态库方案下 vss 的**磁盘库 + HNSW 持久化**（`hnsw_enable_experimental_persistence`）
  在异常关闭后的恢复行为——按官方说明仍不推荐。
- 安装包（NSIS/MSIX）需要把 `duckdb.dll` 一起打包到安装目录，当前只验证了 `bin/` 运行。
- 扩展预置与版本锁定（`INSTALL vss VERSION '...'`）的具体分发方式。
- embedding 生成放在哪里（本地模型 / 供应商 API）、维度、成本。
- 索引重建的触发策略与耗时预算。
- 跨平台：`duckdb_use_lib` 需要各平台对应的 `libduckdb` 共享库，目前只处理 Windows。

## 风险

- `duckdb.dll` 59MB，安装包体积明显增大；exe 本身从 ~147MB（静态）降到 ~31MB。
- 主进程执行 `INSTALL vss` 仍可能因网络/扩展版本失败；失败必须可降级（统计功能不受影响）。
- 动态库与 Go 驱动的版本必须匹配（驱动 2.0.1 ↔ DLL 2.0.1），升级时要同步。
