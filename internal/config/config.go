package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv             string
	MongoURI           string
	MongoDatabase      string
	BackupSchedule     string
	BackupTimezone     string
	DriveFolderID      string
	ServiceAccountJSON string
	RetentionDays      int
	TempBackupDir      string
	RunOnStart         bool
}

func Load() (*Config, error) {
	cfg := &Config{
		AppEnv:             getEnvOrDefault("APP_ENV", "production"),
		MongoURI:           os.Getenv("MONGODB_URI"),
		MongoDatabase:      os.Getenv("MONGODB_DATABASE"),
		BackupSchedule:     getEnvOrDefault("BACKUP_SCHEDULE", "0 2 * * *"),
		BackupTimezone:     getEnvOrDefault("BACKUP_TIMEZONE", "UTC"),
		DriveFolderID:      os.Getenv("GOOGLE_DRIVE_FOLDER_ID"),
		ServiceAccountJSON: os.Getenv("GOOGLE_SERVICE_ACCOUNT_JSON"),
		TempBackupDir:      getEnvOrDefault("TEMP_BACKUP_DIR", "/tmp/mongodb-backups"),
		RunOnStart:         getEnvBool("RUN_BACKUP_ON_START", false),
	}

	if v := os.Getenv("BACKUP_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid BACKUP_RETENTION_DAYS: %w", err)
		}
		cfg.RetentionDays = n
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	var missing []string

	if strings.TrimSpace(c.MongoURI) == "" {
		missing = append(missing, "MONGODB_URI")
	}
	if strings.TrimSpace(c.MongoDatabase) == "" {
		missing = append(missing, "MONGODB_DATABASE")
	}
	if strings.TrimSpace(c.DriveFolderID) == "" {
		missing = append(missing, "GOOGLE_DRIVE_FOLDER_ID")
	}
	if strings.TrimSpace(c.ServiceAccountJSON) == "" {
		missing = append(missing, "GOOGLE_SERVICE_ACCOUNT_JSON")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	if _, err := time.LoadLocation(c.BackupTimezone); err != nil {
		return fmt.Errorf("invalid BACKUP_TIMEZONE: %w", err)
	}

	return nil
}

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}
