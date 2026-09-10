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
	credsJSON     string
	tokenPath     string
	tokenJSON     string
	callbackURL   string
	oauthCodeCh   <-chan string
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

func NewOAuth2UploaderWithInline(folderID, credsJSON, tokenJSON, tokenPath string, log *logger.Logger) *OAuth2Uploader {
	return &OAuth2Uploader{
		folderID:  folderID,
		credsJSON: credsJSON,
		tokenJSON: tokenJSON,
		tokenPath: tokenPath,
		log:       log,
	}
}

func NewOAuth2UploaderWithSharedDriveInline(folderID, sharedDriveID, credsJSON, tokenJSON, tokenPath string, log *logger.Logger) *OAuth2Uploader {
	return &OAuth2Uploader{
		folderID:      folderID,
		sharedDriveID: sharedDriveID,
		credsJSON:     credsJSON,
		tokenJSON:     tokenJSON,
		tokenPath:     tokenPath,
		log:           log,
	}
}

func (o *OAuth2Uploader) WithCallbackURL(url string, ch <-chan string) *OAuth2Uploader {
	o.callbackURL = url
	o.oauthCodeCh = ch
	return o
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
	var config *oauth2.Config
	var err error

	if o.credsJSON != "" {
		config, err = google.ConfigFromJSON([]byte(o.credsJSON), drive.DriveFileScope)
	} else {
		b, err := os.ReadFile(o.credsPath)
		if err != nil {
			return nil, fmt.Errorf("read oauth credentials file: %w", err)
		}
		config, err = google.ConfigFromJSON(b, drive.DriveFileScope)
	}
	if err != nil {
		return nil, fmt.Errorf("parse oauth credentials: %w", err)
	}

	if o.callbackURL != "" {
		config.RedirectURL = o.callbackURL
	}

	tok, err := o.tokenFromData()
	if err != nil {
		o.log.Info("oauth_starting_browser_flow", map[string]interface{}{
			"reason": err.Error(),
		})
		tok, err = o.WaitForToken(ctx, config)
		if err != nil {
			return nil, fmt.Errorf("oauth web flow failed: %w", err)
		}
		if err := o.SaveToken(tok); err != nil {
			return nil, fmt.Errorf("save oauth token: %w", err)
		}
		o.log.Info("oauth_token_saved", map[string]interface{}{
			"file": o.tokenPath,
		})
	}

	ts := config.TokenSource(ctx, tok)
	client := oauth2.NewClient(ctx, ts)

	service, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create drive service: %w", err)
	}

	return service, nil
}

func (o *OAuth2Uploader) tokenFromData() (*oauth2.Token, error) {
	if o.tokenJSON != "" {
		var tok oauth2.Token
		if err := json.Unmarshal([]byte(o.tokenJSON), &tok); err != nil {
			return nil, err
		}
		if tok.RefreshToken != "" {
			return &tok, nil
		}
	}

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

func (o *OAuth2Uploader) SaveToken(tok *oauth2.Token) error {
	if o.tokenJSON != "" {
		b, err := json.Marshal(tok)
		if err != nil {
			return err
		}
		o.tokenJSON = string(b)
	}

	if o.tokenPath != "" {
		f, err := os.Create(o.tokenPath)
		if err != nil {
			return err
		}
		defer f.Close()

		return json.NewEncoder(f).Encode(tok)
	}

	return nil
}

func (o *OAuth2Uploader) HasValidToken() bool {
	_, err := o.tokenFromData()
	return err == nil
}

func (o *OAuth2Uploader) TokenFromFile() (*oauth2.Token, error) {
	return o.tokenFromData()
}

func (o *OAuth2Uploader) GetTokenFromWeb(ctx context.Context) (*oauth2.Token, error) {
	config, err := o.parseOAuthConfig()
	if err != nil {
		return nil, err
	}
	if o.callbackURL != "" {
		config.RedirectURL = o.callbackURL
	}
	return o.WaitForToken(ctx, config)
}

func (o *OAuth2Uploader) GetAuthURL(ctx context.Context) (string, *oauth2.Config, error) {
	config, err := o.parseOAuthConfig()
	if err != nil {
		return "", nil, err
	}
	if o.callbackURL != "" {
		config.RedirectURL = o.callbackURL
	}
	url := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
	o.log.Info("oauth_auth_url_ready", map[string]interface{}{
		"url": url,
	})
	return url, config, nil
}

func (o *OAuth2Uploader) parseOAuthConfig() (*oauth2.Config, error) {
	if o.credsJSON != "" {
		return google.ConfigFromJSON([]byte(o.credsJSON), drive.DriveFileScope)
	}
	b, err := os.ReadFile(o.credsPath)
	if err != nil {
		return nil, fmt.Errorf("read oauth credentials file: %w", err)
	}
	config, err := google.ConfigFromJSON(b, drive.DriveFileScope)
	if err != nil {
		return nil, fmt.Errorf("parse oauth credentials: %w", err)
	}
	return config, nil
}

func (o *OAuth2Uploader) WaitForToken(ctx context.Context, config *oauth2.Config) (*oauth2.Token, error) {
	var code string
	if o.oauthCodeCh != nil {
		o.log.Info("oauth_waiting_for_browser_callback", nil)
		select {
		case code = <-o.oauthCodeCh:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	} else {
		authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
		fmt.Printf("Authorize this app at:\n%s\n\nAfter approval, paste the authorization code here:\n", authURL)
		if _, err := fmt.Scan(&code); err != nil {
			return nil, fmt.Errorf("read authorization code: %w", err)
		}
	}

	tok, err := config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code for token: %w", err)
	}

	return tok, nil
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
