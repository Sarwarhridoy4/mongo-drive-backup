package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/sarwar/mongo-drive-backup/internal/logger"
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

	var service *drive.Service
	var err error

	if len(u.creds) > 0 {
		credsOpt := option.WithCredentialsJSON(u.creds)
		service, err = drive.NewService(ctx, credsOpt)
	} else {
		return "", 0, fmt.Errorf("no drive credentials configured")
	}
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
	var service *drive.Service
	var err error

	if len(u.creds) > 0 {
		credsOpt := option.WithCredentialsJSON(u.creds)
		service, err = drive.NewService(ctx, credsOpt)
	} else {
		return nil, fmt.Errorf("no drive credentials configured")
	}
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
	var service *drive.Service
	var err error

	if len(u.creds) > 0 {
		credsOpt := option.WithCredentialsJSON(u.creds)
		service, err = drive.NewService(ctx, credsOpt)
	} else {
		return fmt.Errorf("no drive credentials configured")
	}
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

type OAuth2Uploader struct {
	folderID      string
	sharedDriveID string
	credsPath     string
	tokenPath     string
	log           *logger.Logger
}

func NewOAuth2Uploader(folderID, credsPath, tokenPath string, log *logger.Logger) *OAuth2Uploader {
	return &OAuth2Uploader{
		folderID:  folderID,
		credsPath: credsPath,
		tokenPath: tokenPath,
		log:       log,
	}
}

func NewOAuth2UploaderWithSharedDrive(folderID, sharedDriveID, credsPath, tokenPath string, log *logger.Logger) *OAuth2Uploader {
	return &OAuth2Uploader{
		folderID:      folderID,
		sharedDriveID: sharedDriveID,
		credsPath:     credsPath,
		tokenPath:     tokenPath,
		log:           log,
	}
}

func (o *OAuth2Uploader) Upload(ctx context.Context, path, filename string) (string, int64, error) {
	o.log.Info("drive_upload_started", map[string]interface{}{
		"file":     filename,
		"folderId": o.folderID,
	})

	service, err := o.newService(ctx)
	if err != nil {
		return "", 0, err
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
		Parents: []string{o.folderID},
	}

	call := service.Files.Create(driveFile).Media(f).Context(ctx)
	if o.sharedDriveID != "" {
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
	o.log.Info("drive_upload_completed", map[string]interface{}{
		"file":    filename,
		"driveId": created.Id,
		"size":    fmt.Sprintf("%dMB", size/1024/1024),
	})

	return created.Id, size, nil
}

func (o *OAuth2Uploader) ListFiles(ctx context.Context) ([]*drive.File, error) {
	service, err := o.newService(ctx)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf("'%s' in parents and trashed = false", o.folderID)
	call := service.Files.List().Q(query).Fields("files(id,name,createdTime,size)")
	if o.sharedDriveID != "" {
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

func (o *OAuth2Uploader) DeleteFile(ctx context.Context, fileID string) error {
	service, err := o.newService(ctx)
	if err != nil {
		return err
	}

	deleteCall := service.Files.Delete(fileID).Context(ctx)
	if o.sharedDriveID != "" {
		deleteCall = deleteCall.SupportsAllDrives(true)
	}
	if err := deleteCall.Do(); err != nil {
		return fmt.Errorf("delete drive file: %w", err)
	}

	return nil
}

func (o *OAuth2Uploader) newService(ctx context.Context) (*drive.Service, error) {
	b, err := os.ReadFile(o.credsPath)
	if err != nil {
		return nil, fmt.Errorf("read oauth credentials file: %w", err)
	}

	config, err := google.ConfigFromJSON(b, drive.DriveFileScope)
	if err != nil {
		return nil, fmt.Errorf("parse oauth credentials: %w", err)
	}

	tok, err := o.tokenFromFile()
	if err != nil {
		return nil, fmt.Errorf("load oauth token: %w", err)
	}

	ts := config.TokenSource(ctx, tok)
	client := oauth2.NewClient(ctx, ts)

	service, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create drive service: %w", err)
	}

	return service, nil
}

func (o *OAuth2Uploader) tokenFromFile() (*oauth2.Token, error) {
	f, err := os.Open(o.tokenPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var tok oauth2.Token
	if err := json.NewDecoder(f).Decode(&tok); err != nil {
		return nil, err
	}

	if tok.RefreshToken == "" {
		return nil, fmt.Errorf("token file missing refresh_token; see setup instructions")
	}

	return &tok, nil
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

func GetDriveScopes() []string {
	return []string{drive.DriveFileScope}
}
