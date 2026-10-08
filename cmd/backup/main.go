package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	driveapi "google.golang.org/api/drive/v3"

	"github.com/sarwar/mongo-drive-backup/internal/backup"
	"github.com/sarwar/mongo-drive-backup/internal/config"
	"github.com/sarwar/mongo-drive-backup/internal/drive"
	"github.com/sarwar/mongo-drive-backup/internal/logger"
	"github.com/sarwar/mongo-drive-backup/internal/restore"
	"github.com/sarwar/mongo-drive-backup/internal/scheduler"
	"github.com/sarwar/mongo-drive-backup/internal/web"

	"github.com/joho/godotenv"
	"github.com/spf13/pflag"
)

func runBackup(ctx context.Context, cfg *config.Config, log *logger.Logger, webSrv *web.Server, uploader drive.DriveUploader, dumper backup.MongoDumper) error {
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

	if err := dumper.Verify(); err != nil {
		return err
	}
	archiver := backup.NewArchiver(tempDir, log)

	dumpDir, err := dumper.Dump(ctx)
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

	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "environment file error: %v\n", err)
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(2)
	}

	log := logger.New(cfg.AppEnv, nil)

	log.Info("service_started", map[string]interface{}{
		"env":      cfg.AppEnv,
		"web_port": cfg.WebPort,
	})

	var webSrv *web.Server
	if cfg.WebPort != "" {
		webSrv = web.NewServer(cfg.WebPort, log)
		if cfg.WebUsername != "" && cfg.WebPassword != "" {
			webSrv.SetBasicAuth(cfg.WebUsername, cfg.WebPassword)
		}
		webSrv.SetConfig(web.Status{
			Environment: cfg.AppEnv,
			MongoURI:    cfg.MongoURI,
			Database:    cfg.MongoDatabase,
			Schedule:    cfg.BackupSchedule,
			Timezone:    cfg.BackupTimezone,
		})
	}

	var uploader drive.DriveUploader
	var oauthHandler *drive.OAuth2Uploader

	if cfg.ServiceAccountJSON != "" {
		if cfg.SharedDriveID != "" {
			uploader = drive.NewUploaderWithSharedDrive(cfg.DriveFolderID, cfg.SharedDriveID, []byte(cfg.ServiceAccountJSON), log)
		} else {
			uploader = drive.NewUploader(cfg.DriveFolderID, []byte(cfg.ServiceAccountJSON), log)
		}
	} else if cfg.OAuthCredentialsFile != "" || cfg.OAuthCredentialsJSON != "" {
		if cfg.OAuthCredentialsJSON != "" {
			if cfg.SharedDriveID != "" {
				oauthHandler = drive.NewOAuth2UploaderWithSharedDriveInline(cfg.DriveFolderID, cfg.SharedDriveID, cfg.OAuthCredentialsJSON, cfg.OAuthTokenJSON, cfg.OAuthTokenFile, log)
			} else {
				oauthHandler = drive.NewOAuth2UploaderWithInline(cfg.DriveFolderID, cfg.OAuthCredentialsJSON, cfg.OAuthTokenJSON, cfg.OAuthTokenFile, log)
			}
		} else {
			if cfg.SharedDriveID != "" {
				oauthHandler = drive.NewOAuth2UploaderWithSharedDrive(cfg.DriveFolderID, cfg.SharedDriveID, cfg.OAuthCredentialsFile, cfg.OAuthTokenFile, log)
			} else {
				oauthHandler = drive.NewOAuth2Uploader(cfg.DriveFolderID, cfg.OAuthCredentialsFile, cfg.OAuthTokenFile, log)
			}
		}
		if webSrv != nil {
			callbackURL := cfg.OAuthCallbackURL
			if callbackURL == "" {
				callbackURL = fmt.Sprintf("http://localhost:%s/oauth2callback", cfg.WebPort)
			}
			uploader = oauthHandler.WithCallbackURL(callbackURL, webSrv.OAuthCodeCh())
			webSrv.SetOAuthHandler(oauthHandler)
		} else {
			uploader = oauthHandler
		}
	} else {
		log.Error("configuration_error", map[string]interface{}{
			"error": "no google drive credentials configured",
		})
		os.Exit(2)
	}

	if webSrv != nil {
		webSrv.SetListBackups(func() ([]*driveapi.File, error) {
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return uploader.ListFiles(c)
		})
		if downloader, ok := uploader.(drive.DriveDownloader); ok {
			restoreService := restore.NewService(downloader, cfg.TempBackupDir, os.Getenv("MONGORESTORE_PATH"), log)
			webSrv.SetRestoreHandler(func(ctx context.Context, request web.RestoreRequest) error {
				return restoreService.Restore(ctx, request.FileID, request.MongoURI, request.Database, request.Confirmation)
			})
		}
	}

	tempDir := cfg.TempBackupDir

	backupJob := func(ctx context.Context) error {
		return runBackup(ctx, cfg, log, webSrv, uploader, backup.NewMongoDumper(cfg.MongoURI, cfg.MongoDatabase, tempDir, log))
	}

	ctx, cancel := context.WithCancel(context.Background())
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signalCh)
	go func() {
		select {
		case sig := <-signalCh:
			log.Info("shutdown_requested", map[string]interface{}{"signal": sig.String()})
			cancel()
		case <-ctx.Done():
		}
	}()

	if webSrv != nil {
		log.Info("web_server_starting", map[string]interface{}{
			"port": cfg.WebPort,
		})
		go func() {
			if err := webSrv.Start(); err != nil {
				log.Error("web_server_failed", map[string]interface{}{
					"port":  cfg.WebPort,
					"error": err.Error(),
					"hint":  "stop the process using this port or set WEB_PORT to another available port",
				})
				cancel()
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
			for {
				select {
				case <-webSrv.StopCh():
					log.Info("service_stop_requested", nil)
					cancel()
					return
				case <-webSrv.RestartCh():
					log.Info("service_restart_requested", nil)
					cancel()
					time.Sleep(500 * time.Millisecond)
					ctx, cancel = context.WithCancel(context.Background())
					go runService(ctx, cfg, log, webSrv, uploader, oauthHandler, once)
				}
			}
		}()
	}

	go func() {
		runService(ctx, cfg, log, webSrv, uploader, oauthHandler, once)
		if once {
			cancel()
		}
	}()

	<-ctx.Done()
	if webSrv != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := webSrv.Shutdown(shutdownCtx); err != nil {
			log.Error("web_server_shutdown_failed", map[string]interface{}{"error": err.Error()})
		} else {
			log.Info("web_server_stopped", nil)
		}
	}
}

func runService(ctx context.Context, cfg *config.Config, log *logger.Logger, webSrv *web.Server, uploader drive.DriveUploader, oauthHandler *drive.OAuth2Uploader, once bool) {
	if err := ensureOAuthIfNeeded(ctx, cfg, log, webSrv, oauthHandler); err != nil {
		log.Error("oauth_setup_failed", map[string]interface{}{
			"error": err.Error(),
		})
		if webSrv == nil {
			os.Exit(1)
		}
		return
	}

	tempDir := cfg.TempBackupDir

	backupJob := func(ctx context.Context) error {
		return runBackup(ctx, cfg, log, webSrv, uploader, backup.NewMongoDumper(cfg.MongoURI, cfg.MongoDatabase, tempDir, log))
	}

	if once {
		if err := backupJob(ctx); err != nil {
			log.Error("backup_failed", map[string]interface{}{
				"error": err.Error(),
			})
			os.Exit(1)
		}
		return
	}

	sched, err := scheduler.New(cfg.BackupSchedule, cfg.BackupTimezone, backupJob, log)
	if err != nil {
		log.Error("scheduler_init_failed", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	if webSrv != nil {
		webSrv.SetNextRunGetter(func() (time.Time, bool) {
			return sched.NextRun()
		})
		go webSrv.StartTicker(ctx)
	}

	if err := sched.Start(ctx); err != nil {
		log.Error("scheduler_failed", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}
}

func ensureOAuthIfNeeded(ctx context.Context, cfg *config.Config, log *logger.Logger, webSrv *web.Server, baseUploader *drive.OAuth2Uploader) error {
	if cfg.ServiceAccountJSON != "" || baseUploader == nil {
		return nil
	}

	log.Info("oauth_preflight_check", map[string]interface{}{
		"token_file": cfg.OAuthTokenFile,
	})

	if webSrv != nil {
		callbackURL := cfg.OAuthCallbackURL
		if callbackURL == "" {
			callbackURL = fmt.Sprintf("http://localhost:%s/oauth2callback", cfg.WebPort)
		}
		baseUploader.WithCallbackURL(callbackURL, webSrv.OAuthCodeCh())
	}

	if _, err := baseUploader.TokenFromFile(); err == nil {
		log.Info("oauth_token_exists", map[string]interface{}{
			"file": cfg.OAuthTokenFile,
		})
		return nil
	}

	if webSrv != nil {
		url, _, err := baseUploader.GetAuthURL(ctx)
		if err != nil {
			log.Error("oauth_auth_url_failed", map[string]interface{}{
				"error": err.Error(),
			})
			return nil
		}
		webSrv.SetOAuthAuthURL(url)
		log.Info("oauth_authorization_required", map[string]interface{}{
			"action": "use the Authorize Google Drive button in the web UI",
		})
		return nil
	}

	log.Info("oauth_starting_preflight_flow", nil)

	tok, err := baseUploader.GetTokenFromWeb(ctx)
	if err != nil {
		return fmt.Errorf("oauth preflight flow failed: %w", err)
	}

	if err := baseUploader.SaveToken(tok); err != nil {
		return fmt.Errorf("save oauth token: %w", err)
	}

	log.Info("oauth_token_saved", map[string]interface{}{
		"file": cfg.OAuthTokenFile,
	})
	log.Info("oauth_preflight_completed", nil)
	return nil
}
