package config

import (
	"os"
	"testing"
)

func TestLoad_ValidEnv(t *testing.T) {
	os.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	os.Setenv("MONGODB_DATABASE", "testdb")
	os.Setenv("GOOGLE_DRIVE_FOLDER_ID", "folder123")
	os.Setenv("GOOGLE_SERVICE_ACCOUNT_JSON", "{}")
	os.Setenv("BACKUP_SCHEDULE", "0 * * * *")
	os.Setenv("BACKUP_TIMEZONE", "UTC")
	os.Setenv("TEMP_BACKUP_DIR", "/tmp/test-backups")
	os.Setenv("BACKUP_RETENTION_DAYS", "15")
	os.Setenv("RUN_BACKUP_ON_START", "true")
	os.Setenv("APP_ENV", "test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if cfg.MongoURI != "mongodb://localhost:27017" {
		t.Errorf("expected MONGODB_URI to be set")
	}
	if cfg.MongoDatabase != "testdb" {
		t.Errorf("expected MONGODB_DATABASE to be set")
	}
	if cfg.DriveFolderID != "folder123" {
		t.Errorf("expected GOOGLE_DRIVE_FOLDER_ID to be set")
	}
	if cfg.BackupSchedule != "0 * * * *" {
		t.Errorf("expected schedule to be 0 * * * *")
	}
	if cfg.BackupTimezone != "UTC" {
		t.Errorf("expected timezone to be UTC")
	}
	if cfg.RetentionDays != 15 {
		t.Errorf("expected retention days to be 15")
	}
	if cfg.RunOnStart != true {
		t.Errorf("expected run on start to be true")
	}
}

func TestValidate_MissingVars(t *testing.T) {
	os.Unsetenv("MONGODB_URI")
	os.Unsetenv("MONGODB_DATABASE")
	os.Unsetenv("GOOGLE_DRIVE_FOLDER_ID")
	os.Unsetenv("GOOGLE_SERVICE_ACCOUNT_JSON")
	os.Setenv("BACKUP_SCHEDULE", "0 * * * *")
	os.Setenv("BACKUP_TIMEZONE", "UTC")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error for missing vars")
	}
}
