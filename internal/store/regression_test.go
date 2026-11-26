package store

// 回归测试：统计库损坏时自动自愈（先删 WAL，再备份主文件重建），
// 以及内存库降级路径可用。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenMemoryStore(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存 DuckDB 失败: %v", err)
	}
	defer st.Close()

	if err := st.CreateTask(Task{ID: "t1", SessionID: "s1", Title: "mem", Status: StatusRunning}); err != nil {
		t.Fatalf("写入任务失败: %v", err)
	}
	got, err := st.GetTask("t1")
	if err != nil || got == nil || got.Title != "mem" {
		t.Fatalf("读取任务失败: %+v err=%v", got, err)
	}
}

// 主文件损坏时应备份为 .corrupt 并重建空库，而不是让应用启动失败。
func TestOpenRecoversFromCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.duckdb")
	if err := os.WriteFile(path, []byte("definitely not a duckdb file"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("损坏文件应自愈重建: %v", err)
	}
	defer st.Close()

	if err := st.CreateTask(Task{ID: "t1", SessionID: "s1", Title: "recovered", Status: StatusRunning}); err != nil {
		t.Fatalf("重建后应可写入: %v", err)
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("应保留损坏文件备份: %v", err)
	}
}

// WAL 损坏时优先只丢 WAL：主文件中已 checkpoint 的数据仍在。
func TestOpenRecoversByDroppingCorruptWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.duckdb")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTask(Task{ID: "keep", SessionID: "s1", Title: "keep", Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// 模拟 checkpoint 后 WAL 损坏
	if err := os.WriteFile(path+".wal", []byte("garbage wal"), 0o644); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("WAL 损坏应自愈: %v", err)
	}
	defer st2.Close()

	if _, err := st2.GetTask("keep"); err != nil {
		t.Fatalf("已 checkpoint 的数据不应丢失: %v", err)
	}
	if _, err := os.Stat(path + ".corrupt"); err == nil {
		t.Fatal("WAL 级自愈不应把主文件备份为 .corrupt")
	}
}
