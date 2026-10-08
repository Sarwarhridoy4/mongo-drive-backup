# How to Run

This repository is a Go service that periodically creates a MongoDB dump, compresses it, verifies the archive, uploads the archive to a Google Drive folder, and optionally serves a small web dashboard when `WEB_PORT` is set. The built-in dashboard uses a WebSocket (`/ws`) stream for live status, logs, and backup metadata instead of repeatedly calling the REST endpoints with a timer.

## 1. Prerequisites

- Go 1.26+
- Docker
- A MongoDB instance reachable through `MONGODB_URI`
- Google Drive destination folder access:
  - **Option A:** personal Gmail account using OAuth 2.0, or
  - **Option B:** Google Workspace Shared Drive with a service account

For step-by-step Google credential setup, see [How to get Google Credentials.md](How%20to%20get%20Google%20Credentials.md).

## 2. Copy the example environment file

```bash
cp .env.example .env
```

Then edit `.env` and fill the required variables.

For OAuth (personal Gmail):

```env
APP_ENV=production
MONGODB_URI=mongodb://username:password@mongodb:27017
MONGODB_DATABASE=mydatabase
BACKUP_SCHEDULE=0 2 * * *
BACKUP_TIMEZONE=Asia/Dhaka
GOOGLE_DRIVE_FOLDER_ID=your-folder-id
GOOGLE_OAUTH_CREDENTIALS_FILE=./secrets/oauth-credentials.json
GOOGLE_OAUTH_TOKEN_FILE=./token.json
BACKUP_RETENTION_DAYS=30
TEMP_BACKUP_DIR=/tmp/mongodb-backups
RUN_BACKUP_ON_START=false
WEB_PORT=
MONGODUMP_PATH=
MONGORESTORE_PATH=
GOOGLE_OAUTH_CALLBACK_URL=
WEB_USERNAME=
WEB_PASSWORD=
```

For service account + Shared Drive:

```env
APP_ENV=production
MONGODB_URI=mongodb://username:password@mongodb:27017
MONGODB_DATABASE=mydatabase
BACKUP_SCHEDULE=0 2 * * *
BACKUP_TIMEZONE=Asia/Dhaka
GOOGLE_DRIVE_FOLDER_ID=folder-id-inside-shared-drive
GOOGLE_SHARED_DRIVE_ID=your-shared-drive-id
GOOGLE_APPLICATION_CREDENTIALS=./secrets/google-service-account.json
BACKUP_RETENTION_DAYS=30
TEMP_BACKUP_DIR=/tmp/mongodb-backups
RUN_BACKUP_ON_START=false
WEB_PORT=
MONGODUMP_PATH=
```

The service validates the required variables:

- `MONGODB_URI`
- `MONGODB_DATABASE`
- `GOOGLE_DRIVE_FOLDER_ID`
- One of:
  - `GOOGLE_OAUTH_CREDENTIALS_FILE`
  - `GOOGLE_APPLICATION_CREDENTIALS`
  - `GOOGLE_SERVICE_ACCOUNT_JSON`

## 3. Install dependencies

```bash
go mod download
```

## 4. Run locally

The application reads environment variables from the process environment and starts the scheduler defined by `BACKUP_SCHEDULE`.

> **Note:** Local `go run` requires `mongodump` to be installed on your machine. If you don't have it, use the Docker commands in section 5 instead.

### OAuth authorization

If you are using OAuth 2.0 and `token.json` does not exist yet:

- With web UI enabled (`WEB_PORT` set): open the dashboard and click **Authorize Google Drive**
- Without web UI: the app prints the authorization URL in the logs; complete the flow in a browser

After approval, the token is saved to `token.json` automatically.

### Run a single backup and exit

```bash
./run.sh --once
```

`--once` exits automatically after the backup completes. The wrapper forwards Ctrl+C/SIGTERM to
the service and removes its temporary binary.

### Run the scheduler normally

```bash
./run.sh
```

You can also run `go run ./cmd/backup` directly; the application handles SIGINT and SIGTERM and
gracefully shuts down the scheduler and web listener.

### Enable the built-in web UI

Set `WEB_PORT` and then start the service:

```bash
WEB_PORT=8080 go run ./cmd/backup
```

Then visit:

```text
http://localhost:8080
```

When `WEB_USERNAME` and `WEB_PASSWORD` are configured, the dashboard first shows a sign-in page.
The current local credentials are stored only in `.env`; never commit them. Use
`http://127.0.0.1:8080` if a browser proxy or cached localhost state causes connection retries.

The dashboard is a live WebSocket page that pushes the current `status`, `logs`, and `backups` snapshots over `/ws`. It receives new events without polling or manual reloads.

The dashboard shows:

- current configuration
- last backup status and progress
- backups currently in Google Drive
- recent log history
- manual backup trigger
- stop service button
- restore backup control; it requires selecting a backup, entering a MongoDB URL and database,
  and typing `RESTORE`
- OAuth authorize control that becomes disabled automatically when a valid OAuth refresh token already exists

The restore flow is destructive. It downloads the selected Drive archive to a private temporary
directory and runs `mongorestore --drop`, replacing matching collections in the target database.
It rejects unsafe archive paths and does not log or persist the MongoDB URL.

## 5. Docker build and run

### Build image

```bash
docker build -t mongo-drive-backup .
```

### Run once in a container

```bash
docker run --rm --env-file .env mongo-drive-backup --once
```

### Run with the web UI

```bash
docker run --rm --env-file .env -p 8080:8080 mongo-drive-backup
```

The Dockerfile uses a multi-stage build, runs as a non-root user, installs `mongodump` and
`mongorestore`, exposes port `8080`, and provides a public `/healthz` health check. Set
`WEB_PORT` to another value only when publishing and configuring the matching container port.

## 6. Production deployment notes

- Keep Google credentials out of source control.
- Use environment secrets in Coolify or another deployment platform.
- If you want the UI available in Coolify, expose the same port via `WEB_PORT`.
- Configure both `WEB_USERNAME` and `WEB_PASSWORD` for any publicly reachable dashboard.
- Use the dashboard **Stop Service** button to release the listener cleanly.
- The service writes temporary dump and archive files in `TEMP_BACKUP_DIR`; they are cleaned after the archive completes and the file is uploaded.
- The Docker image includes a healthcheck on `/healthz` for Coolify.

## 7. Troubleshooting

- Verify that `mongodump` is available inside the container or on the host.
- Confirm that the Google account or service account has edit permission for the Google Drive folder.
- For service accounts, use a Shared Drive; normal My Drive folders will fail with `storageQuotaExceeded`.
- For OAuth, make sure `token.json` was generated and is readable. If not, use the **Authorize Google Drive** button in the web UI, or check the logs for the authorization URL.
- If port `8080` is already in use, inspect it with `lsof -nP -iTCP:8080 -sTCP:LISTEN` and stop the old service or choose another port.
- If the OAuth callback shows `localhost` in the browser, set `GOOGLE_OAUTH_CALLBACK_URL` to your public Coolify URL, and add that same URL to your authorized redirect URIs in Google Cloud Console.
- Ensure `BACKUP_TIMEZONE` is a valid Go/ZoneInfo timezone such as `UTC` or `Asia/Dhaka`.
- Watch the logs for `mongodb_dump_failed`, `drive_upload_failed`, or scheduler errors.
