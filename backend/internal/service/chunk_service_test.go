package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteChunkAtomic(t *testing.T) {
	dir := t.TempDir()
	data := []byte("first-version")

	if err := writeChunkAtomic(dir, 0, data); err != nil {
		t.Fatalf("writeChunkAtomic() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "0"))
	if err != nil {
		t.Fatalf("read chunk: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("chunk content = %q, want %q", got, data)
	}
	info, err := os.Stat(filepath.Join(dir, "0"))
	if err != nil {
		t.Fatalf("stat chunk: %v", err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("chunk must not be executable, perm = %v", info.Mode().Perm())
	}

	// 模拟网络中断后重传同一分片：应覆盖旧内容，且不残留临时文件
	replay := []byte("second-version")
	if err := writeChunkAtomic(dir, 0, replay); err != nil {
		t.Fatalf("rewrite chunk: %v", err)
	}
	got, err = os.ReadFile(filepath.Join(dir, "0"))
	if err != nil {
		t.Fatalf("read rewritten chunk: %v", err)
	}
	if string(got) != string(replay) {
		t.Fatalf("rewritten chunk content = %q, want %q", got, replay)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read chunk dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("chunk dir should contain only final chunk, got %d entries", len(entries))
	}
}

func TestVerifyChunkFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyChunkFile(dir, 0, 3); err != nil {
		t.Fatalf("verify existing chunk: %v", err)
	}
	if err := verifyChunkFile(dir, 0, 4); !errors.Is(err, ErrChunkMissing) {
		t.Fatalf("size mismatch error = %v, want ErrChunkMissing", err)
	}
	if err := verifyChunkFile(dir, 1, 3); !errors.Is(err, ErrChunkMissing) {
		t.Fatalf("missing chunk error = %v, want ErrChunkMissing", err)
	}
}

func TestIsValidUploadID(t *testing.T) {
	valid := []string{"550e8400-e29b-41d4-a716-446655440000", "chunk_123-abc"}
	invalid := []string{"", "short", "../escape", "a/b", strings.Repeat("a", 65)}
	for _, id := range valid {
		if !isValidUploadID(id) {
			t.Fatalf("upload id %q should be valid", id)
		}
	}
	for _, id := range invalid {
		if isValidUploadID(id) {
			t.Fatalf("upload id %q should be invalid", id)
		}
	}
}

func TestIsValidSHA256(t *testing.T) {
	if !isValidSHA256("") {
		t.Fatal("empty hash means skip verification and should be valid")
	}
	if !isValidSHA256(strings.Repeat("a", 64)) {
		t.Fatal("64-char hex hash should be valid")
	}
	for _, hash := range []string{"abc", strings.Repeat("z", 64), strings.Repeat("a", 63)} {
		if isValidSHA256(hash) {
			t.Fatalf("hash %q should be invalid", hash)
		}
	}
}
