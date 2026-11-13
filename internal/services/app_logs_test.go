package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecentLogs(t *testing.T) {
	app := &AppService{S: &Services{}}
	logPath := filepath.Join(t.TempDir(), "app.log")

	// 文件缺失：友好的空提示。
	app.SetLogPath(logPath)
	lines, err := app.RecentLogs(50)
	if err != nil || len(lines) != 1 {
		t.Fatalf("missing file should yield placeholder: %v %v", lines, err)
	}

	// 尾部语义：写入 300 行，返回最后 50 行。
	var sb strings.Builder
	for i := 1; i <= 300; i++ {
		sb.WriteString("line-" + itoaTest(i) + "\n")
	}
	if err := os.WriteFile(logPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err = app.RecentLogs(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 50 {
		t.Fatalf("expected 50 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "line-300") {
		t.Fatalf("tail wrong: %v", lines[len(lines)-1])
	}
	if strings.Contains(strings.Join(lines, "\n"), "line-250") {
		t.Fatal("tail included lines beyond the window")
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	d := ""
	for n > 0 {
		d = string(rune('0'+n%10)) + d
		n /= 10
	}
	return d
}

// TestStorageStatsAndVacuum：大小取自真实文件，VACUUM
// 在正在使用的会话数据库上运行无报错。
func TestStorageStatsAndVacuum(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")
	kPath := filepath.Join(dir, "knowledge.index.db")
	db, _, err := newTestSessionService(t, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	app := NewAppService(&Services{}, dbPath, kPath, db)

	info := app.StorageStats()
	if info.SessionsDBBytes <= 0 {
		t.Fatalf("sessions db size missing: %+v", info)
	}
	if info.KnowledgeDBBytes != -1 {
		t.Fatalf("absent knowledge db should report -1: %+v", info)
	}
	if err := os.WriteFile(kPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info = app.StorageStats(); info.KnowledgeDBBytes != 1 {
		t.Fatalf("knowledge db size wrong: %+v", info)
	}
	if err := app.VacuumSessions(); err != nil {
		t.Fatalf("vacuum failed: %v", err)
	}
	// 之后数据库仍可用：count 查询可正常工作。
	if err := db.Exec("SELECT count(*) FROM sqlite_master").Error; err != nil {
		t.Fatalf("db unusable after vacuum: %v", err)
	}
}

// TestRecentToolAuditTail：工具审计查看器通过相同的尾部语义读取 JSONL
// 文件（取最后 N 行，文件缺失时返回占位内容）。
func TestRecentToolAuditTail(t *testing.T) {
	app := &AppService{S: &Services{}}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	app.SetToolAuditPath(auditPath)

	lines, err := app.RecentToolAudit(10)
	if err != nil || len(lines) != 1 {
		t.Fatalf("missing audit should yield placeholder: %v %v", lines, err)
	}

	if err := os.WriteFile(auditPath, []byte("r1\nr2\nr3\nr4\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	lines, err = app.RecentToolAudit(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "r3" || lines[1] != "r4" {
		t.Fatalf("tail wrong: %v", lines)
	}
}
