package backup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/sarwar/mongo-drive-backup/internal/logger"
)

type MongoDumper struct {
	uri      string
	database string
	outDir   string
	log      *logger.Logger
}

func NewMongoDumper(uri, database, outDir string, log *logger.Logger) *MongoDumper {
	return &MongoDumper{
		uri:      uri,
		database: database,
		outDir:   outDir,
		log:      log,
	}
}

func (m *MongoDumper) Dump(ctx context.Context) (string, error) {
	ts := time.Now().UTC().Format("2006-01-02-150405")
	dumpDir := filepath.Join(m.outDir, fmt.Sprintf("mongodb-%s-%s", m.database, ts))

	if err := os.MkdirAll(dumpDir, 0755); err != nil {
		return "", fmt.Errorf("create dump dir: %w", err)
	}

	cmd := exec.CommandContext(ctx, "mongodump",
		"--uri="+m.uri,
		"--db="+m.database,
		"--out="+dumpDir,
	)

	m.log.Info("mongodb_dump_started", map[string]interface{}{
		"database": m.database,
		"out":      dumpDir,
	})

	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(dumpDir)
		m.log.Error("mongodb_dump_failed", map[string]interface{}{
			"error": string(output),
		})
		return "", fmt.Errorf("mongodump failed: %w: %s", err, string(output))
	}

	m.log.Info("mongodb_dump_completed", map[string]interface{}{
		"database": m.database,
		"out":      dumpDir,
	})

	return dumpDir, nil
}
