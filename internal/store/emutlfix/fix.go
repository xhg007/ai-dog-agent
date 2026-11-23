// Package emutlfix 提供 libduckdb 预构建静态库在 Windows 上缺失的 emutls 控制变量。
//
// 背景：duckdb-go-bindings 的 windows-amd64 预构建库构建于旧版 GCC（C++ thread_local
// 走 emutls 机制），其 libtpch_extension.a 等引用 libstdc++ call_once 内部 TLS 变量的
// emutls 控制块 __emutls_v._ZSt11__once_call / __emutls_v._ZSt15__once_callable；
// 而 MSYS2 GCC 16 的 libstdc++ 已改用原生 TLS，libgcc 也移除了 emutls runtime，
// 导致链接期 undefined reference。__emutls_get_address 由 duckdb 库自带，仅需提供
// 这两个 __emutls_object 控制块的数据定义（布局：size/align/index/templ 各一个 word，
// 均为 8 字节指针对齐、无初始模板，与 libstdc++ 中对应变量的语义一致）。
//
// 若未来 duckdb-go 驱动更新使链接不再缺符号，可删除本包。
package emutlfix

/*
typedef unsigned long long word;
typedef struct { word size; word align; word index; void* templ; } emutls_obj;

__attribute__((used))
const emutls_obj emutls_once_call __asm__("__emutls_v._ZSt11__once_call") = { 8, 8, 0, 0 };

__attribute__((used))
const emutls_obj emutls_once_callable __asm__("__emutls_v._ZSt15__once_callable") = { 8, 8, 0, 0 };
*/
import "C"
