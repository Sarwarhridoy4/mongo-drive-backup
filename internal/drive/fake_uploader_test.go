package drive

import (
	"context"
	"fmt"
	"testing"
)

func TestFakeUploader_Upload(t *testing.T) {
	fake := NewFakeUploader()
	ctx := context.Background()

	id, size, err := fake.Upload(ctx, "/tmp/test.zip", "test.zip")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == "" {
		t.Errorf("expected non-empty id")
	}
	if size != 1024 {
		t.Errorf("expected size 1024, got %d", size)
	}

	calls := fake.UploadCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 upload call, got %d", len(calls))
	}
	if calls[0].Path != "/tmp/test.zip" || calls[0].Filename != "test.zip" {
		t.Errorf("unexpected upload call: %+v", calls[0])
	}
}

func TestFakeUploader_ListFiles(t *testing.T) {
	fake := NewFakeUploader()
	ctx := context.Background()

	_, _, _ = fake.Upload(ctx, "/tmp/a.zip", "a.zip")
	_, _, _ = fake.Upload(ctx, "/tmp/b.zip", "b.zip")

	files, err := fake.ListFiles(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
}

func TestFakeUploader_DeleteFile(t *testing.T) {
	fake := NewFakeUploader()
	ctx := context.Background()

	id, _, _ := fake.Upload(ctx, "/tmp/test.zip", "test.zip")
	if err := fake.DeleteFile(ctx, id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	files, _ := fake.ListFiles(ctx)
	if len(files) != 0 {
		t.Errorf("expected 0 files after delete, got %d", len(files))
	}

	calls := fake.DeleteCalls()
	if len(calls) != 1 || calls[0] != id {
		t.Errorf("unexpected delete calls: %v", calls)
	}
}

func TestFakeUploader_ListFiles_Error(t *testing.T) {
	fake := NewFakeUploader()
	fake.SetListError(fmt.Errorf("list failed"))

	_, err := fake.ListFiles(context.Background())
	if err == nil {
		t.Fatalf("expected error from ListFiles")
	}
}
