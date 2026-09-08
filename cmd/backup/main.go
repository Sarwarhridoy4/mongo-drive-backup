package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sarwar/mongo-drive-backup/internal/backup"
	"github.com/sarwar/mongo-drive-backup/internal/config"
	"github.com/sarwar/mongo-drive-backup/internal/drive"
	"github.com/sarwar/mongo-drive-backup/internal/logger"
	"github.com/sarwar/mongo-drive-backup/internal/scheduler"

	"github.com/spf13/pflag"
)

func runBackup(ctx context.Context, cfg *config.Config, log *logger.Logger) error {
	log.Info("backup_started", map[string]interface{}{
		"database": cfg.MongoDatabase,
	})

	tempDir := cfg.TempBackupDir
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}

	mongoDumper := backup.NewMongoDumper(cfg.MongoURI, cfg.MongoDatabase, tempDir, log)
	archiver := backup.NewArchiver(tempDir, log)
	uploader := drive.NewUploader(cfg.DriveFolderID, []byte(cfg.ServiceAccountJSON), log)

	dumpDir, err := mongoDumper.Dump(ctx)
	if err != nil {
		return fmt.Errorf("mongodb dump failed: %w", err)
	}

	filename := backup.GenerateBackupFilename(cfg.MongoDatabase)
	archivePath := filepath.Join(tempDir, filename)

	if err := archiver.Compress(ctx, dumpDir, archivePath); err != nil {
		_ = os.RemoveAll(dumpDir)
		return fmt.Errorf("archive failed: %w", err)
	}

	if err := archiver.VerifyArchive(archivePath); err != nil {
		_ = os.RemoveAll(dumpDir)
		_ = os.RemoveAll(archivePath)
		return fmt.Errorf("archive verification failed: %w", err)
	}

	_, size, err := uploader.Upload(ctx, archivePath, filename)
	if err != nil {
		_ = os.RemoveAll(dumpDir)
		_ = os.RemoveAll(archivePath)
		return fmt.Errorf("drive upload failed: %w", err)
	}

	_ = os.RemoveAll(dumpDir)
	_ = os.RemoveAll(archivePath)

	log.Info("cleanup_completed", map[string]interface{}{
		"dumpDir": dumpDir,
		"archive": archivePath,
	})

	log.Info("backup_completed", map[string]interface{}{
		"file":     filename,
		"size":     fmt.Sprintf("%dMB", size/1024/1024),
		"database": cfg.MongoDatabase,
	})

	return nil
}

func main() {
	var once bool

	pflag.BoolVarP(&once, "once", "o", false, "Run a single backup and exit")
	pflag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(2)
	}

	log := logger.New(cfg.AppEnv, nil)

	log.Info("service_started", map[string]interface{}{
		"env": cfg.AppEnv,
	})

	sched, err := scheduler.New(cfg.BackupSchedule, cfg.BackupTimezone, func(ctx context.Context) error {
		return runBackup(ctx, cfg, log)
	}, log)
	if err != nil {
		log.Error("scheduler_init_failed", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if once {
		if err := sched.RunOnce(ctx); err != nil {
			log.Error("backup_failed", map[string]interface{}{
				"error": err.Error(),
			})
			os.Exit(1)
		}
		return
	}

	if err := sched.Start(ctx); err != nil {
		log.Error("scheduler_failed", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}
}
