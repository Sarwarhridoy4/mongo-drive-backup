package drive

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sarwar/mongo-drive-backup/internal/logger"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type Uploader struct {
	folderID      string
	sharedDriveID string
	creds         []byte
	log           *logger.Logger
}

func NewUploader(folderID string, creds []byte, log *logger.Logger) *Uploader {
	return &Uploader{
		folderID: folderID,
		creds:    creds,
		log:      log,
	}
}

func NewUploaderWithSharedDrive(folderID, sharedDriveID string, creds []byte, log *logger.Logger) *Uploader {
	return &Uploader{
		folderID:      folderID,
		sharedDriveID: sharedDriveID,
		creds:         creds,
		log:           log,
	}
}

func (u *Uploader) Upload(ctx context.Context, path, filename string) (string, int64, error) {
	u.log.Info("drive_upload_started", map[string]interface{}{
		"file":     filename,
		"folderId": u.folderID,
	})

	credsOpt := option.WithCredentialsJSON(u.creds)
	service, err := drive.NewService(ctx, credsOpt)
	if err != nil {
		return "", 0, fmt.Errorf("create drive service: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	fileInfo, err := f.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat file: %w", err)
	}

	driveFile := &drive.File{
		Name:    filename,
		Parents: []string{u.folderID},
	}

	call := service.Files.Create(driveFile).Media(f).Context(ctx)
	if u.sharedDriveID != "" {
		call = call.SupportsAllDrives(true)
	}
	created, err := call.Do()
	if err != nil {
		return "", 0, fmt.Errorf("drive upload failed: %w", err)
	}

	if created.Id == "" {
		return "", 0, fmt.Errorf("drive upload returned empty file id")
	}

	size := fileInfo.Size()
	u.log.Info("drive_upload_completed", map[string]interface{}{
		"file":    filename,
		"driveId": created.Id,
		"size":    fmt.Sprintf("%dMB", size/1024/1024),
	})

	return created.Id, size, nil
}

func (u *Uploader) ListFiles(ctx context.Context) ([]*drive.File, error) {
	credsOpt := option.WithCredentialsJSON(u.creds)
	service, err := drive.NewService(ctx, credsOpt)
	if err != nil {
		return nil, fmt.Errorf("create drive service: %w", err)
	}

	query := fmt.Sprintf("'%s' in parents and trashed = false", u.folderID)
	call := service.Files.List().Q(query).Fields("files(id,name,createdTime,size)")
	if u.sharedDriveID != "" {
		call = call.SupportsAllDrives(true).IncludeItemsFromAllDrives(true)
	}
	var files []*drive.File
	err = call.Pages(ctx, func(page *drive.FileList) error {
		files = append(files, page.Files...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list drive files: %w", err)
	}

	return files, nil
}

func (u *Uploader) DeleteFile(ctx context.Context, fileID string) error {
	credsOpt := option.WithCredentialsJSON(u.creds)
	service, err := drive.NewService(ctx, credsOpt)
	if err != nil {
		return fmt.Errorf("create drive service: %w", err)
	}

	deleteCall := service.Files.Delete(fileID).Context(ctx)
	if u.sharedDriveID != "" {
		deleteCall = deleteCall.SupportsAllDrives(true)
	}
	if err := deleteCall.Do(); err != nil {
		return fmt.Errorf("delete drive file: %w", err)
	}

	return nil
}

type DriveFileInfo struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

func ParseCreatedTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

func CloseWithLog(c io.Closer, name string) {
	if c == nil {
		return
	}
	if err := c.Close(); err != nil {
		fmt.Printf("warning: failed to close %s: %v\n", name, err)
	}
}
