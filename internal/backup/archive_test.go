package backup

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarwar/mongo-drive-backup/internal/logger"
)

func TestGenerateBackupFilename(t *testing.T) {
	filename := GenerateBackupFilename("mydb")
	expected := time.Now().UTC().Format("2006-01-02-150405") + "-mydb.tar.gz"

	if filename != expected {
		t.Errorf("expected %s, got %s", expected, filename)
	}

	if !strings.HasPrefix(filename, time.Now().UTC().Format("2006-01-02-150405")+"-mydb") {
		t.Errorf("filename should start with timestamp-db")
	}
	if !strings.HasSuffix(filename, ".tar.gz") {
		t.Errorf("filename should end with .tar.gz")
	}
}

func TestMatchesBackupPattern(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid backup", "2026-09-08-020000-mydb.tar.gz", true},
		{"missing prefix", "mydb-2026-09-08-020000.tar.gz", false},
		{"too short", "abc.tar.gz", false},
		{"empty", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := MatchesBackupPattern(tc.input)
			if result != tc.expected {
				t.Errorf("expected %v, got %v for %s", tc.expected, result, tc.input)
			}
		})
	}
}

func TestCompress(t *testing.T) {
	tempDir := t.TempDir()
	log := logger.New("test", nil)

	archiver := NewArchiver(tempDir, log)

	sourceDir := filepath.Join(tempDir, "source")
	os.MkdirAll(sourceDir, 0755)
	if err := os.WriteFile(filepath.Join(sourceDir, "test.txt"), []byte("hello world"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	destPath := filepath.Join(tempDir, "backup.tar.gz")

	if err := archiver.Compress(nil, sourceDir, destPath); err != nil {
		t.Fatalf("compress failed: %v", err)
	}

	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		t.Errorf("archive file does not exist")
	}

	if err := archiver.VerifyArchive(destPath); err != nil {
		t.Errorf("archive verification failed: %v", err)
	}
}

func TestVerifyArchive_Invalid(t *testing.T) {
	tempDir := t.TempDir()
	badFile := filepath.Join(tempDir, "bad.tar.gz")
	os.WriteFile(badFile, []byte("not a real archive"), 0644)

	log := logger.New("test", nil)
	archiver := NewArchiver(tempDir, log)

	err := archiver.VerifyArchive(badFile)
	if err == nil {
		t.Errorf("expected error for invalid archive")
	}
}

func readTarGz(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var names []string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		names = append(names, header.Name)
	}
	return names, nil
}

func TestCompressPreservesStructure(t *testing.T) {
	tempDir := t.TempDir()
	log := logger.New("test", nil)

	archiver := NewArchiver(tempDir, log)

	sourceDir := filepath.Join(tempDir, "source")
	os.MkdirAll(filepath.Join(sourceDir, "subdir"), 0755)
	if err := os.WriteFile(filepath.Join(sourceDir, "file1.txt"), []byte("file1"), 0644); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "subdir", "file2.txt"), []byte("file2"), 0644); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	destPath := filepath.Join(tempDir, "backup.tar.gz")
	if err := archiver.Compress(nil, sourceDir, destPath); err != nil {
		t.Fatalf("compress failed: %v", err)
	}

	names, err := readTarGz(destPath)
	if err != nil {
		t.Fatalf("read tar gz: %v", err)
	}

	found := false
	for _, n := range names {
		if n == "subdir/file2.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected subdir/file2.txt in archive, got: %v", names)
	}
}

func TestCompressCleanupOnFailure(t *testing.T) {
	tempDir := t.TempDir()
	log := logger.New("test", nil)
	archiver := NewArchiver(tempDir, log)

	sourceDir := filepath.Join(tempDir, "nonexistent")
	destPath := filepath.Join(tempDir, "backup.tar.gz")

	err := archiver.Compress(nil, sourceDir, destPath)
	if err == nil {
		t.Errorf("expected error for nonexistent source")
	}

	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Errorf("archive should not be created on failure")
	}
}
