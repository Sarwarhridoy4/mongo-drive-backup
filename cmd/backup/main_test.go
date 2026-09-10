package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	driveapi "google.golang.org/api/drive/v3"

	"github.com/sarwar/mongo-drive-backup/internal/config"
	"github.com/sarwar/mongo-drive-backup/internal/logger"
	"github.com/sarwar/mongo-drive-backup/internal/web"
)

type fakeUploader struct {
	uploaded bool
}

func (f *fakeUploader) Upload(ctx context.Context, path, filename string) (string, int64, error) {
	f.uploaded = true
	return "fake-id", 1024, nil
}

func (f *fakeUploader) ListFiles(ctx context.Context) ([]*driveapi.File, error) {
	return nil, nil
}

func (f *fakeUploader) DeleteFile(ctx context.Context, fileID string) error {
	return nil
}

type fakeDumper struct {
	dumpDir string
	err     error
}

func (f *fakeDumper) Verify() error {
	return nil
}

func (f *fakeDumper) Dump(ctx context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.dumpDir == "" {
		f.dumpDir = filepath.Join(os.TempDir(), "fake-dump")
	}
	if err := os.MkdirAll(f.dumpDir, 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(f.dumpDir, "test.ns"), []byte("data"), 0644); err != nil {
		return "", err
	}
	return f.dumpDir, nil
}

func TestRunBackup_Success(t *testing.T) {
	tempDir := t.TempDir()
	log := logger.New("test", nil)
	cfg := &config.Config{
		AppEnv:         "test",
		MongoURI:       "mongodb://localhost:27017",
		MongoDatabase:  "testdb",
		TempBackupDir:  tempDir,
		BackupSchedule: "0 2 * * *",
		BackupTimezone: "UTC",
		DriveFolderID:  "folder1",
	}

	webSrv := web.NewServer("0", log)
	uploader := &fakeUploader{}
	dumper := &fakeDumper{dumpDir: filepath.Join(tempDir, "dump")}

	err := runBackup(context.Background(), cfg, log, webSrv, uploader, dumper)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !uploader.uploaded {
		t.Errorf("expected uploader to be called")
	}
}

func TestRunBackup_DumpFailure(t *testing.T) {
	tempDir := t.TempDir()
	log := logger.New("test", nil)
	cfg := &config.Config{
		AppEnv:         "test",
		MongoURI:       "mongodb://localhost:27017",
		MongoDatabase:  "testdb",
		TempBackupDir:  tempDir,
		BackupSchedule: "0 2 * * *",
		BackupTimezone: "UTC",
		DriveFolderID:  "folder1",
	}

	webSrv := web.NewServer("0", log)
	uploader := &fakeUploader{}
	dumper := &fakeDumper{err: fmt.Errorf("dump failed")}

	err := runBackup(context.Background(), cfg, log, webSrv, uploader, dumper)
	if err == nil {
		t.Fatalf("expected error on dump failure")
	}
}
