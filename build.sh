#!/usr/bin/env bash
# 生产构建（Linux/macOS）。CGO/tags/库路径由 Taskfile 的 build:native 设置，
# 这里只做前置检查，避免在缺库时构建出静态链接版本（vss 会崩）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

case "$(uname -s)" in
  Linux*)  PLAT=linux ;;
  Darwin*) PLAT=osx ;;
  *) echo "不支持的平台: $(uname -s)" >&2; exit 1 ;;
esac

ARCH="$(go env GOARCH)"
LIB_DIR="$ROOT/libs/duckdb-shared-libs-${PLAT}-${ARCH}"
if [ ! -d "$LIB_DIR" ]; then
  echo "缺少 $LIB_DIR（需要 duckdb 动态库与头文件）" >&2
  exit 1
fi

cd "$ROOT"
exec wails3 build "$@"
