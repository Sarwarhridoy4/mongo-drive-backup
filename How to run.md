# How to Run

This repository is a Go service that periodically creates a MongoDB dump, compresses it, verifies the archive, uploads the archive to a Google Drive folder, and optionally serves a small web dashboard when `WEB_PORT` is set.

## 1. Prerequisites

- Go 1.23+
- Docker
- `mongodump` available in the runtime environment
- A MongoDB instance reachable through `MONGODB_URI`
- A Google Cloud service account with the Google Drive API enabled
- A Google Drive folder shared with the service account

For a step-by-step guide to create the Google service account and JSON credentials, see [How to get Google Credentials.md](How%20to%20get%20Google%20Credentials.md).

## 2. Copy the example environment file

```bash
cp .env.example .env
```

Then edit `.env` and fill the required variables:

```env
APP_ENV=production
MONGODB_URI=mongodb://username:password@mongodb:27017
MONGODB_DATABASE=mydatabase
BACKUP_SCHEDULE=0 2 * * *
BACKUP_TIMEZONE=Asia/Dhaka
GOOGLE_DRIVE_FOLDER_ID=your-folder-id
GOOGLE_SHARED_DRIVE_ID=your-shared-drive-id
GOOGLE_SERVICE_ACCOUNT_JSON={"type":"service_account", ...}
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
- `GOOGLE_SERVICE_ACCOUNT_JSON`

## 3. Install dependencies

```bash
go mod download
```

## 4. Run locally

The application reads environment variables from the process environment and starts the scheduler defined by `BACKUP_SCHEDULE`.

> **Note:** Local `go run` requires `mongodump` to be installed on your machine. If you don't have it, use the Docker commands in section 5 instead.

### Run a single backup and exit

```bash
go run ./cmd/backup --once
```

### Run the scheduler normally

```bash
go run ./cmd/backup
```

### Enable the built-in web UI

Set `WEB_PORT` and then start the service:

```bash
WEB_PORT=8080 go run ./cmd/backup
```

Then visit:

```text
http://localhost:8080
```

The dashboard reads the configured environment, current schedule, and the latest backup result.

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

The Dockerfile exposes `WEB_PORT` through an `ARG` and `EXPOSE` declaration.

## 6. Production deployment notes

- Keep the Google service account JSON out of source control.
- Use environment secrets in Coolify or another deployment platform.
- If you want the UI available in Coolify, expose the same port via `WEB_PORT`.
- The service writes temporary dump and archive files in `TEMP_BACKUP_DIR`; they are cleaned after the archive completes and the file is uploaded.

## 7. Troubleshooting

- Verify that `mongodump` is installed in the runtime image or host.
- Confirm that the service account email has edit permission for the Google Drive folder.
- Ensure `BACKUP_TIMEZONE` is a valid Go/ZoneInfo timezone such as `UTC` or `Asia/Dhaka`.
- Watch the logs for `mongodb_dump_failed`, `drive_upload_failed`, or scheduler errors.
