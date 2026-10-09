package restore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sarwar/mongo-drive-backup/internal/drive"
	"github.com/sarwar/mongo-drive-backup/internal/logger"
)

type Service struct {
	downloader   drive.DriveDownloader
	tempDir      string
	mongorestore string
	log          *logger.Logger
}

func NewService(downloader drive.DriveDownloader, tempDir, mongorestore string, log *logger.Logger) *Service {
	if mongorestore == "" {
		mongorestore = "mongorestore"
	}
	return &Service{downloader: downloader, tempDir: tempDir, mongorestore: mongorestore, log: log}
}

func (s *Service) Restore(ctx context.Context, fileID, mongoURI, database, confirmation string) error {
	if fileID == "" || mongoURI == "" || database == "" {
		return fmt.Errorf("backup, MongoDB URL, and database are required")
	}
	if confirmation != "RESTORE" {
		return fmt.Errorf("type RESTORE to confirm the destructive operation")
	}
	if strings.ContainsAny(database, `/\\`) || database == "." || database == ".." {
		return fmt.Errorf("invalid database name")
	}
	if _, err := exec.LookPath(s.mongorestore); err != nil {
		return fmt.Errorf("mongorestore not found at %q: %w", s.mongorestore, err)
	}
	if err := os.MkdirAll(s.tempDir, 0700); err != nil {
		return fmt.Errorf("create restore directory: %w", err)
	}
	workDir, err := os.MkdirTemp(s.tempDir, "restore-")
	if err != nil {
		return fmt.Errorf("create restore workspace: %w", err)
	}
	defer os.RemoveAll(workDir)

	archivePath := filepath.Join(workDir, "backup.tar.gz")
	s.log.Info("restore_download_started", map[string]interface{}{"backup_id": fileID})
	if err := s.downloader.Download(ctx, fileID, archivePath); err != nil {
		return fmt.Errorf("download backup: %w", err)
	}
	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("downloaded backup is unavailable: %w", err)
	}
	if !archiveInfo.Mode().IsRegular() || archiveInfo.Size() == 0 {
		return fmt.Errorf("downloaded backup is empty")
	}
	s.log.Info("restore_download_completed", map[string]interface{}{"backup_id": fileID, "size": archiveInfo.Size()})
	dumpDir := filepath.Join(workDir, "dump")
	if err := extractArchive(archivePath, dumpDir); err != nil {
		return err
	}
	databaseDir, flatDump, err := findDatabaseDir(dumpDir, database)
	if err != nil {
		return err
	}

	// --drop removes each matching collection before restoring it.
	args := []string{"--uri=" + mongoURI, "--drop"}
	if flatDump {
		args = append(args, "--db="+database)
	}
	args = append(args, databaseDir)
	s.log.Info("mongodb_restore_started", map[string]interface{}{"database": database, "backup_id": fileID})
	cmd := exec.CommandContext(ctx, s.mongorestore, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.log.Error("mongodb_restore_failed", map[string]interface{}{"database": database, "error": strings.TrimSpace(string(output))})
		return fmt.Errorf("mongorestore failed: %w", err)
	}
	s.log.Info("mongodb_restore_completed", map[string]interface{}{"database": database, "backup_id": fileID})
	return nil
}

func findDatabaseDir(dumpDir, database string) (string, bool, error) {
	direct := filepath.Join(dumpDir, database)
	if info, err := os.Stat(direct); err == nil {
		if info.IsDir() {
			return direct, false, nil
		}
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("inspect backup database %q: %w", database, err)
	}
	if hasDumpFiles(dumpDir) {
		return dumpDir, true, nil
	}

	var matches []string
	err := filepath.WalkDir(dumpDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dumpDir || !entry.IsDir() || entry.Name() != database {
			return nil
		}
		matches = append(matches, path)
		return filepath.SkipDir
	})
	if err != nil {
		return "", false, fmt.Errorf("inspect backup contents: %w", err)
	}
	if len(matches) == 1 {
		return matches[0], false, nil
	}
	if len(matches) > 1 {
		return "", false, fmt.Errorf("backup contains multiple directories for database %q", database)
	}
	return "", false, fmt.Errorf("backup does not contain database %q", database)
}

func hasDumpFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".bson") || entry.Name() == "metadata.json") {
			return true
		}
	}
	return false
}

func extractArchive(archivePath, destination string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open restore archive: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read restore archive: %w", err)
	}
	defer gz.Close()
	tarReader := tar.NewReader(gz)
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read restore archive entry: %w", err)
		}
		cleanName := filepath.Clean(header.Name)
		if cleanName == "." || filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe archive path: %q", header.Name)
		}
		target := filepath.Join(destination, cleanName)
		if header.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if !header.FileInfo().Mode().IsRegular() {
			return fmt.Errorf("unsupported archive entry: %q", header.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(tarReader, header.Size))
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
