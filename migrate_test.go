package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyCacheMoves(t *testing.T) {
	dir := t.TempDir()
	oldDir := filepath.Join(dir, ".cache")
	newDir := filepath.Join(dir, "conf", "cache")
	if err := os.MkdirAll(filepath.Join(oldDir, "holidays"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "auto-booking-cache.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	migrateLegacyCache(oldDir, newDir)

	if _, err := os.Stat(filepath.Join(newDir, "auto-booking-cache.json")); err != nil {
		t.Errorf("旧缓存文件应整体搬到新目录: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newDir, "holidays")); err != nil {
		t.Errorf("旧缓存子目录应整体搬到新目录: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("搬移后旧目录不应存在")
	}
}

func TestMigrateLegacyCacheNoops(t *testing.T) {
	dir := t.TempDir()
	oldDir := filepath.Join(dir, ".cache")
	newDir := filepath.Join(dir, "cache")

	// 旧目录不存在:什么都不做,也不创建新目录
	migrateLegacyCache(oldDir, newDir)
	if _, err := os.Stat(newDir); !os.IsNotExist(err) {
		t.Error("无旧缓存时不应创建新目录")
	}

	// 新目录已存在:不动旧目录(避免覆盖已迁移的数据)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "a.json"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	migrateLegacyCache(oldDir, newDir)
	if _, err := os.Stat(filepath.Join(oldDir, "a.json")); err != nil {
		t.Errorf("新目录已存在时旧目录应原样保留: %v", err)
	}
}
