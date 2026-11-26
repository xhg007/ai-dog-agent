#!/usr/bin/env bash
# 开发模式（Linux/macOS）：设置 DuckDB 动态库环境后运行 wails3 dev。
# wails3 dev 不经过 Taskfile 的 build:native，因此需要在这里显式设置 CGO 环境。
set -euo pipefail

case "$(uname -s)" in
  Linux*)  PLAT=linux ;;
  Darwin*) PLAT=osx ;;
  *) echo "不支持的平台: $(uname -s)" >&2; exit 1 ;;
esac

ARCH="$(go env GOARCH)"
LIB_DIR="$PWD/libs/duckdb-shared-libs-${PLAT}-${ARCH}"
if [ ! -d "$LIB_DIR" ]; then
  echo "缺少 $LIB_DIR（需要 duckdb 动态库与头文件）" >&2
  exit 1
fi

export CGO_ENABLED=1
export CGO_CFLAGS="-I$LIB_DIR"
export CGO_LDFLAGS="-L$LIB_DIR -lduckdb"
export EXTRA_TAGS=duckdb_use_lib

if [ "$PLAT" = "linux" ]; then
  export LD_LIBRARY_PATH="$LIB_DIR:${LD_LIBRARY_PATH:-}"
else
  export DYLD_LIBRARY_PATH="$LIB_DIR:${DYLD_LIBRARY_PATH:-}"
fi

exec wails3 dev "$@"
