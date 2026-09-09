package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	driveapi "google.golang.org/api/drive/v3"

	"github.com/sarwar/mongo-drive-backup/internal/backup"
	"github.com/sarwar/mongo-drive-backup/internal/config"
	"github.com/sarwar/mongo-drive-backup/internal/drive"
	"github.com/sarwar/mongo-drive-backup/internal/logger"
	"github.com/sarwar/mongo-drive-backup/internal/scheduler"
	"github.com/sarwar/mongo-drive-backup/internal/web"

	"github.com/joho/godotenv"
	"github.com/spf13/pflag"
)

func runBackup(ctx context.Context, cfg *config.Config, log *logger.Logger, webSrv *web.Server) error {
	log.Info("backup_started", map[string]interface{}{
		"database": cfg.MongoDatabase,
	})

	if webSrv != nil {
		webSrv.UpdateBackupResult("", 0, fmt.Errorf("running"))
	}

	tempDir := cfg.TempBackupDir
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		if webSrv != nil {
			webSrv.UpdateBackupResult("", 0, err)
		}
		return fmt.Errorf("create temp dir: %w", err)
	}

	mongoDumper := backup.NewMongoDumper(cfg.MongoURI, cfg.MongoDatabase, tempDir, log)
	archiver := backup.NewArchiver(tempDir, log)

	var uploader interface {
		Upload(ctx context.Context, path, filename string) (string, int64, error)
	}

	if cfg.ServiceAccountJSON != "" {
		if cfg.SharedDriveID != "" {
			uploader = drive.NewUploaderWithSharedDrive(cfg.DriveFolderID, cfg.SharedDriveID, []byte(cfg.ServiceAccountJSON), log)
		} else {
			uploader = drive.NewUploader(cfg.DriveFolderID, []byte(cfg.ServiceAccountJSON), log)
		}
	} else if cfg.OAuthCredentialsFile != "" || cfg.OAuthCredentialsJSON != "" {
		var baseUploader *drive.OAuth2Uploader
		if cfg.OAuthCredentialsJSON != "" {
			if cfg.SharedDriveID != "" {
				baseUploader = drive.NewOAuth2UploaderWithSharedDriveInline(cfg.DriveFolderID, cfg.SharedDriveID, cfg.OAuthCredentialsJSON, cfg.OAuthTokenJSON, log)
			} else {
				baseUploader = drive.NewOAuth2UploaderWithInline(cfg.DriveFolderID, cfg.OAuthCredentialsJSON, cfg.OAuthTokenJSON, log)
			}
		} else {
			if cfg.SharedDriveID != "" {
				baseUploader = drive.NewOAuth2UploaderWithSharedDrive(cfg.DriveFolderID, cfg.SharedDriveID, cfg.OAuthCredentialsFile, cfg.OAuthTokenFile, log)
			} else {
				baseUploader = drive.NewOAuth2Uploader(cfg.DriveFolderID, cfg.OAuthCredentialsFile, cfg.OAuthTokenFile, log)
			}
		}
		if webSrv != nil {
			callbackURL := fmt.Sprintf("http://localhost:%s/oauth2callback", cfg.WebPort)
			uploader = baseUploader.WithCallbackURL(callbackURL, webSrv.OAuthCodeCh())
		} else {
			uploader = baseUploader
		}
	} else {
		return fmt.Errorf("no google drive credentials configured")
	}

	if webSrv != nil {
		if u, ok := uploader.(interface {
			ListFiles(context.Context) ([]*driveapi.File, error)
		}); ok {
			webSrv.SetListBackups(func() ([]*driveapi.File, error) {
				c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				return u.ListFiles(c)
			})
		}
	}

	dumpDir, err := mongoDumper.Dump(ctx)
	if err != nil {
		if webSrv != nil {
			webSrv.UpdateBackupResult("", 0, err)
		}
		return fmt.Errorf("mongodb dump failed: %w", err)
	}

	filename := backup.GenerateBackupFilename(cfg.MongoDatabase)
	archivePath := filepath.Join(tempDir, filename)

	if err := archiver.Compress(ctx, dumpDir, archivePath); err != nil {
		_ = os.RemoveAll(dumpDir)
		if webSrv != nil {
			webSrv.UpdateBackupResult("", 0, err)
		}
		return fmt.Errorf("archive failed: %w", err)
	}

	if err := archiver.VerifyArchive(archivePath); err != nil {
		_ = os.RemoveAll(dumpDir)
		_ = os.RemoveAll(archivePath)
		if webSrv != nil {
			webSrv.UpdateBackupResult("", 0, err)
		}
		return fmt.Errorf("archive verification failed: %w", err)
	}

	_, size, err := uploader.Upload(ctx, archivePath, filename)
	if err != nil {
		_ = os.RemoveAll(dumpDir)
		_ = os.RemoveAll(archivePath)
		if webSrv != nil {
			webSrv.UpdateBackupResult("", 0, err)
		}
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

	if webSrv != nil {
		webSrv.UpdateBackupResult(filename, size, nil)
	}

	return nil
}

func main() {
	var once bool

	pflag.BoolVarP(&once, "once", "o", false, "Run a single backup and exit")
	pflag.Parse()

	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(2)
	}

	log := logger.New(cfg.AppEnv, nil)

	log.Info("service_started", map[string]interface{}{
		"env": cfg.AppEnv,
	})

	var webSrv *web.Server
	if cfg.WebPort != "" {
		webSrv = web.NewServer(cfg.WebPort, log)
		webSrv.SetConfig(web.Status{
			Environment: cfg.AppEnv,
			MongoURI:    cfg.MongoURI,
			Database:    cfg.MongoDatabase,
			Schedule:    cfg.BackupSchedule,
			Timezone:    cfg.BackupTimezone,
		})
	}

	backupJob := func(ctx context.Context) error {
		return runBackup(ctx, cfg, log, webSrv)
	}

	sched, err := scheduler.New(cfg.BackupSchedule, cfg.BackupTimezone, backupJob, log)
	if err != nil {
		log.Error("scheduler_init_failed", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if webSrv != nil {
		go func() {
			if err := webSrv.Start(); err != nil {
				log.Error("web_server_failed", map[string]interface{}{
					"error": err.Error(),
				})
			}
		}()

		go func() {
			for range webSrv.Trigger() {
				go func() {
					if err := backupJob(ctx); err != nil {
						log.Error("web_manual_backup_failed", map[string]interface{}{
							"error": err.Error(),
						})
					}
				}()
			}
		}()

		go func() {
			<-webSrv.StopCh()
			log.Info("service_stop_requested", nil)
			cancel()
		}()
	}

	if err := ensureOAuthIfNeeded(ctx, cfg, log, webSrv); err != nil {
		log.Error("oauth_setup_failed", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

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

func ensureOAuthIfNeeded(ctx context.Context, cfg *config.Config, log *logger.Logger, webSrv *web.Server) error {
	if cfg.ServiceAccountJSON != "" || cfg.OAuthCredentialsFile == "" && cfg.OAuthCredentialsJSON == "" {
		return nil
	}

	log.Info("oauth_preflight_check", map[string]interface{}{
		"token_file": cfg.OAuthTokenFile,
	})

	var baseUploader *drive.OAuth2Uploader
	if cfg.OAuthCredentialsJSON != "" {
		if cfg.SharedDriveID != "" {
			baseUploader = drive.NewOAuth2UploaderWithSharedDriveInline(cfg.DriveFolderID, cfg.SharedDriveID, cfg.OAuthCredentialsJSON, cfg.OAuthTokenJSON, log)
		} else {
			baseUploader = drive.NewOAuth2UploaderWithInline(cfg.DriveFolderID, cfg.OAuthCredentialsJSON, cfg.OAuthTokenJSON, log)
		}
	} else {
		if cfg.SharedDriveID != "" {
			baseUploader = drive.NewOAuth2UploaderWithSharedDrive(cfg.DriveFolderID, cfg.SharedDriveID, cfg.OAuthCredentialsFile, cfg.OAuthTokenFile, log)
		} else {
			baseUploader = drive.NewOAuth2Uploader(cfg.DriveFolderID, cfg.OAuthCredentialsFile, cfg.OAuthTokenFile, log)
		}
	}

	if webSrv != nil {
		callbackURL := fmt.Sprintf("http://localhost:%s/oauth2callback", cfg.WebPort)
		baseUploader.WithCallbackURL(callbackURL, webSrv.OAuthCodeCh())
	}

	if _, err := baseUploader.TokenFromFile(); err == nil {
		log.Info("oauth_token_exists", map[string]interface{}{
			"file": cfg.OAuthTokenFile,
		})
		return nil
	}

	log.Info("oauth_starting_preflight_flow", nil)

	if _, err := baseUploader.GetTokenFromWeb(ctx); err != nil {
		return fmt.Errorf("oauth preflight flow failed: %w", err)
	}

	log.Info("oauth_preflight_completed", nil)
	return nil
}
