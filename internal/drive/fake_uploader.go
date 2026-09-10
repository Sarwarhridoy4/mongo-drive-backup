package drive

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/api/drive/v3"
)

type FakeUploader struct {
	mu          sync.Mutex
	files       map[string]*drive.File
	uploadCalls []UploadCall
	deleteCalls []string
	listError   error
}

type UploadCall struct {
	Path     string
	Filename string
}

func NewFakeUploader() *FakeUploader {
	return &FakeUploader{
		files:       make(map[string]*drive.File),
		uploadCalls: make([]UploadCall, 0),
		deleteCalls: make([]string, 0),
	}
}

func (f *FakeUploader) Upload(ctx context.Context, path, filename string) (string, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploadCalls = append(f.uploadCalls, UploadCall{Path: path, Filename: filename})

	id := fmt.Sprintf("fake-%d", len(f.uploadCalls))
	file := &drive.File{
		Id:    id,
		Name:  filename,
		Size:  1024,
	}
	f.files[id] = file
	return id, 1024, nil
}

func (f *FakeUploader) ListFiles(ctx context.Context) ([]*drive.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listError != nil {
		return nil, f.listError
	}
	out := make([]*drive.File, 0, len(f.files))
	for _, file := range f.files {
		out = append(out, file)
	}
	return out, nil
}

func (f *FakeUploader) DeleteFile(ctx context.Context, fileID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, fileID)
	delete(f.files, fileID)
	return nil
}

func (f *FakeUploader) SetListError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listError = err
}

func (f *FakeUploader) UploadCalls() []UploadCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]UploadCall, len(f.uploadCalls))
	copy(out, f.uploadCalls)
	return out
}

func (f *FakeUploader) DeleteCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.deleteCalls))
	copy(out, f.deleteCalls)
	return out
}
