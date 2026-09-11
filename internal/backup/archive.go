package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sarwar/mongo-drive-backup/internal/logger"
)

type Archiver struct {
	tempDir string
	log     *logger.Logger
}

func NewArchiver(tempDir string, log *logger.Logger) *Archiver {
	return &Archiver{
		tempDir: tempDir,
		log:     log,
	}
}

func GenerateBackupFilename(database string) string {
	ts := time.Now().UTC().Format("2006-01-02-150405")
	return fmt.Sprintf("%s-%s.tar.gz", ts, database)
}

func (a *Archiver) Compress(ctx context.Context, sourceDir, destPath string) error {
	a.log.Info("archive_started", map[string]interface{}{
		"source": sourceDir,
		"dest":   destPath,
	})

	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return fmt.Errorf("source dir does not exist: %s", sourceDir)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	if err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}

		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		if rel == "." {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tw, file); err != nil {
				return err
			}
		}

		return nil
	}); err != nil {
		return fmt.Errorf("archive walk failed: %w", err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("close gzip writer: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close archive file: %w", err)
	}

	a.log.Info("archive_completed", map[string]interface{}{
		"dest": destPath,
	})

	return nil
}

func (a *Archiver) VerifyArchive(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open archive for verification: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader failed: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	_, err = tr.Next()
	if err != nil && err != io.EOF {
		return fmt.Errorf("tar reader failed: %w", err)
	}

	return nil
}

func MatchesBackupPattern(name string) bool {
	base := strings.TrimSuffix(strings.TrimSuffix(name, ".gz"), ".tar")
	segs := strings.Split(base, "-")
	if len(segs) < 4 {
		return false
	}
	if len(segs[0]) != 4 || len(segs[1]) != 2 || len(segs[2]) != 2 || len(segs[3]) != 6 {
		return false
	}
	return true
}
