# MongoDB Google Drive Backup Service

Production-ready Go service that periodically backs up a MongoDB database and uploads it to Google Drive.

## Architecture

```text
Scheduler -> MongoDB dump -> Compress -> Google Drive upload -> Verify -> Cleanup -> Log
```

## Requirements

- Docker
- MongoDB instance
- Google Drive destination folder
- Either:
  - Google Cloud service account with Drive API access **and** a Shared Drive, or
  - Personal Gmail account using OAuth 2.0

## Dashboard and realtime features

The built-in web dashboard is served when `WEB_PORT` is configured. It uses a WebSocket (`/ws`) stream for push-driven updates so the page receives `status`, `logs`, and `backups` payloads automatically. This avoids the need for browser fetch polling or manual refresh loops.

The OAuth authorize button is also now wired to the token status endpoint. If a valid refresh token is already available in `GOOGLE_OAUTH_TOKEN_FILE` or `GOOGLE_OAUTH_TOKEN_JSON`, the button is disabled automatically and the page reflects that the token is already authorized.

## Environment Variables

| Variable                         | Description                          | Default                |
| -------------------------------- | ------------------------------------ | ---------------------- |
| `APP_ENV`                        | Environment                          | `production`           |
| `MONGODB_URI`                    | MongoDB connection string            | Required               |
| `MONGODB_DATABASE`               | Database name to backup              | Required               |
| `BACKUP_SCHEDULE`                | Cron schedule                        | `0 2 * * *`            |
| `BACKUP_TIMEZONE`                | Schedule timezone                    | `UTC`                  |
| `GOOGLE_DRIVE_FOLDER_ID`         | Target Drive folder ID               | Required               |
| `GOOGLE_SHARED_DRIVE_ID`         | Shared Drive ID                      | Optional               |
| `GOOGLE_OAUTH_CREDENTIALS_FILE`  | OAuth client credentials JSON path   | Optional               |
| `GOOGLE_OAUTH_CREDENTIALS_JSON`  | OAuth client credentials JSON inline | Optional               |
| `GOOGLE_OAUTH_TOKEN_FILE`        | OAuth token file path                | `./token.json`         |
| `GOOGLE_OAUTH_TOKEN_JSON`        | OAuth token JSON inline              | Optional               |
| `GOOGLE_OAUTH_CALLBACK_URL`      | Public OAuth callback URL            | Optional               |
| `GOOGLE_APPLICATION_CREDENTIALS` | Service account JSON file path       | Optional               |
| `GOOGLE_SERVICE_ACCOUNT_JSON`    | Service account JSON inline          | Optional               |
| `BACKUP_RETENTION_DAYS`          | Retention in days                    | `30`                   |
| `TEMP_BACKUP_DIR`                | Temp directory                       | `/tmp/mongodb-backups` |
| `RUN_BACKUP_ON_START`            | Run backup on startup                | `false`                |
| `WEB_PORT`                       | Web UI port, e.g. `8080`             | Optional               |
| `MONGODUMP_PATH`                 | Full path to `mongodump`             | Optional               |

## Authentication

This app supports two authentication modes.

### Option A — OAuth 2.0 (recommended for personal Gmail)

Use this if you want to upload to a normal Google Drive folder in your personal/consumer account.

1. Create OAuth 2.0 credentials in Google Cloud Console.
2. Download the client secrets JSON.
3. For local development, place it at a path like `./secrets/oauth-credentials.json`.
4. For Coolify/deployments without file mounts, paste the JSON into an environment variable.
5. Set one of:

   ```env
   # File path (local development)
   GOOGLE_OAUTH_CREDENTIALS_FILE=./secrets/oauth-credentials.json
   GOOGLE_OAUTH_TOKEN_FILE=./token.json

   # Inline JSON (Coolify / deployments)
   GOOGLE_OAUTH_CREDENTIALS_JSON={"installed":{...}}
   GOOGLE_OAUTH_TOKEN_JSON={"access_token":"...","refresh_token":"...","token_type":"Bearer"}
   ```

   Or use both: file path for credentials, inline JSON for token.

   For Coolify or production, also set:

   ```env
   GOOGLE_OAUTH_CALLBACK_URL=https://your-domain.com/oauth2callback
   ```

   Make sure this URL is also added to your authorized redirect URIs in Google Cloud Console.

6. Start the app and authorize it:
   - With web UI (`WEB_PORT` set): open the dashboard and click **Authorize Google Drive**
   - Without web UI: the app prints the authorization URL in the logs; complete the flow in a browser

   After approval, the token is saved to `token.json` automatically.

### Option B — Service account + Shared Drive (for Workspace)

Use this if you have Google Workspace and a Shared Drive.

1. Create a service account and download the JSON.
2. Create a Shared Drive.
3. Add the service account email as **Content manager**.
4. Set:
   ```env
   GOOGLE_APPLICATION_CREDENTIALS=./secrets/google-service-account.json
   GOOGLE_SHARED_DRIVE_ID=your-shared-drive-id
   GOOGLE_DRIVE_FOLDER_ID=folder-id-inside-shared-drive
   ```

## Local Development

```bash
cp .env.example .env
# Edit .env with your values

go mod download
go run ./cmd/backup --once
```

## Testing

```bash
go test ./...
```

With coverage:

```bash
go test ./... -race -coverprofile=coverage.out
go tool cover -func=coverage.out
```

> Local `go run` requires `mongodump` to be installed on your machine. If you don't have it, use Docker instead:
>
> ```bash
> docker build -t mongo-drive-backup .
> docker run --rm --env-file .env mongo-drive-backup --once
> ```

For a complete step-by-step run guide, see [How to run.md](How%20to%20run.md).

For Google credential setup, see [How to get Google Credentials.md](How%20to%20get%20Google%20Credentials.md).

## Google Drive Folder Setup

1. Open Google Drive.
2. For OAuth: create/open the target folder in your normal Drive and copy its ID from the URL.
3. For Shared Drive: create a Shared Drive, add your service account, then use a folder inside it.

## Running Locally

```bash
go run ./cmd/backup
```

## Web UI

Set `WEB_PORT` to enable the built-in dashboard:

```bash
WEB_PORT=8080 go run ./cmd/backup
```

Then open `http://localhost:8080`.

The dashboard shows:

- current configuration
- last backup status and progress
- last uploaded file and size
- backups currently in Google Drive
- recent log history
- manual backup trigger
- stop service button

The dashboard is refreshed over a WebSocket at `/ws` instead of repeatedly calling `/api/status`, `/api/logs`, and `/api/backups` via `fetch` or `setInterval`. That keeps log lines and status cards push-driven and avoids continuous polling.

In Coolify, expose the same `WEB_PORT` as a public port if you want to access the dashboard.

## Manual Backup

```bash
go run ./cmd/backup --once
```

## Docker

```bash
docker build -t mongo-drive-backup .
docker run --rm mongo-drive-backup --once
```

## Coolify Deployment

1. Push this repository to GitHub.
2. In Coolify, create a new application from the repository.
3. Set the environment variables in Coolify.
4. Deploy.

The Dockerfile includes:

- Single-stage self-contained build — no Go or `mongodump` required on the host
- `mongodump` installed via Alpine packages
- Healthcheck on `/healthz`
- Web UI with manual **Authorize Google Drive** button for OAuth
- Non-root runtime user

## Troubleshooting

- Verify `mongodump` is available inside the container: `docker run --rm <image> mongodump --version`
- Check container logs in Coolify.
- For service account uploads, ensure you are using a Shared Drive.
- For OAuth, make sure `token.json` was generated and is readable. If not, use the **Authorize Google Drive** button in the web UI, or check the logs for the authorization URL.
- Ensure `BACKUP_TIMEZONE` is a valid Go/ZoneInfo timezone such as `UTC` or `Asia/Dhaka`.
- Watch the logs for `mongodb_dump_failed`, `drive_upload_failed`, or scheduler errors.

## Security Notes

- Never log credentials.
- Never commit production secrets.
- Use Coolify secrets for sensitive values.
- Local backup files are deleted after upload.
- For OAuth, keep `token.json` out of source control.
