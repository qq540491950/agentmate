package agentkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestReadBoundedFileTruncatesAtRuneBoundary:
// 超过读取上限的文件必须被截断并带上标记返回，绝不把整个文件加载进内存，也不拆
// 分 UTF-8 序列。
func TestReadBoundedFileTruncatesAtRuneBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.txt")
	big := strings.Repeat("汉", 40*1024) // 120KB 的 UTF-8 内容，上限为 64KB
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	content, truncated, err := ReadBoundedFile(path, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("expected truncation")
	}
	if !utf8.ValidString(content) {
		t.Fatal("truncated content is not valid UTF-8")
	}
	if !strings.HasSuffix(content, "\n...[truncated]") {
		t.Fatalf("truncation marker missing, tail: %q", content[len(content)-30:])
	}
	if len(content) > 64*1024+len("\n...[truncated]") {
		t.Fatalf("content exceeds cap: %d bytes", len(content))
	}
}

func TestReadBoundedFileSmallAndExactLimit(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(small, []byte("你好，工作区"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, truncated, err := ReadBoundedFile(small, 64*1024)
	if err != nil || truncated || content != "你好，工作区" {
		t.Fatalf("small file: content=%q truncated=%v err=%v", content, truncated, err)
	}

	exact := filepath.Join(dir, "exact.txt")
	payload := strings.Repeat("a", 64*1024)
	if err := os.WriteFile(exact, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	content, truncated, err = ReadBoundedFile(exact, 64*1024)
	if err != nil || truncated || content != payload {
		t.Fatalf("exact-limit file must not truncate: truncated=%v len=%d err=%v", truncated, len(content), err)
	}
}

// TestWriteBoundedFileRejectsOversize:
// 写入工具必须在一开始就拒绝超大负载，而不是让失控的生成把磁盘写满。
func TestWriteBoundedFileRejectsOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := WriteBoundedFile(path, strings.Repeat("x", 6<<20+1), 6<<20); err == nil {
		t.Fatal("expected oversize rejection")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("rejected write must not create the file")
	}
	if err := WriteBoundedFile(path, "正常内容", 6<<20); err != nil {
		t.Fatalf("bounded write failed: %v", err)
	}
}
