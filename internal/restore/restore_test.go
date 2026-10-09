package restore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindDatabaseDirSupportsWrappedArchives(t *testing.T) {
	dumpDir := t.TempDir()
	databaseDir := filepath.Join(dumpDir, "2026-10-09-030000-himulingua_test", "himulingua_test")
	if err := os.MkdirAll(databaseDir, 0700); err != nil {
		t.Fatal(err)
	}

	got, flat, err := findDatabaseDir(dumpDir, "himulingua_test")
	if err != nil {
		t.Fatalf("findDatabaseDir returned error: %v", err)
	}
	if flat {
		t.Fatal("wrapped dump should not be treated as flat")
	}
	if got != databaseDir {
		t.Fatalf("expected %q, got %q", databaseDir, got)
	}
}

func TestFindDatabaseDirRejectsMissingDatabase(t *testing.T) {
	_, _, err := findDatabaseDir(t.TempDir(), "himulingua_test")
	if err == nil {
		t.Fatal("expected missing database error")
	}
}

func TestFindDatabaseDirRejectsAmbiguousDatabase(t *testing.T) {
	dumpDir := t.TempDir()
	for _, wrapper := range []string{"first", "second"} {
		if err := os.MkdirAll(filepath.Join(dumpDir, wrapper, "himulingua_test"), 0700); err != nil {
			t.Fatal(err)
		}
	}

	_, _, err := findDatabaseDir(dumpDir, "himulingua_test")
	if err == nil {
		t.Fatal("expected ambiguous database error")
	}
}

func TestFindDatabaseDirSupportsFlatDump(t *testing.T) {
	dumpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dumpDir, "users.bson"), []byte("dump"), 0600); err != nil {
		t.Fatal(err)
	}

	got, flat, err := findDatabaseDir(dumpDir, "himulingua_test")
	if err != nil {
		t.Fatalf("findDatabaseDir returned error: %v", err)
	}
	if got != dumpDir || !flat {
		t.Fatalf("expected flat dump %q, %q, got %q, %v", dumpDir, dumpDir, got, flat)
	}
}
