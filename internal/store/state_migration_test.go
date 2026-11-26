package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestInterruptedStatusMigratedToCancelled 旧库 interrupted 在重新打开时迁移为 cancelled。
func TestInterruptedStatusMigratedToCancelled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migrate.duckdb")
	s1, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s1.CreateTask(Task{
		ID: "t1", SessionID: "s1", Title: "旧任务", Status: StatusInterrupted, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	defer s2.Close()
	task, err := s2.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusCancelled {
		t.Fatalf("interrupted 应迁移为 cancelled，实际: %s", task.Status)
	}
}
