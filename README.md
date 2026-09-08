# MongoDB Google Drive Backup Service

Production-ready Go service that periodically backs up a MongoDB database and uploads it to Google Drive.

## Architecture

```text
Scheduler -> MongoDB dump -> Compress -> Google Drive upload -> Verify -> Cleanup -> Log
```

## Requirements

- Go 1.23+
- Docker
- MongoDB instance
- Google Cloud service account with Drive API access

## Environment Variables

| Variable                      | Description               | Default                |
| ----------------------------- | ------------------------- | ---------------------- |
| `APP_ENV`                     | Environment               | `production`           |
| `MONGODB_URI`                 | MongoDB connection string | Required               |
| `MONGODB_DATABASE`            | Database name to backup   | Required               |
| `BACKUP_SCHEDULE`             | Cron schedule             | `0 2 * * *`            |
| `BACKUP_TIMEZONE`             | Schedule timezone         | `UTC`                  |
| `GOOGLE_DRIVE_FOLDER_ID`      | Target Drive folder       | Required               |
| `GOOGLE_SERVICE_ACCOUNT_JSON` | Service account JSON      | Required               |
| `BACKUP_RETENTION_DAYS`       | Retention in days         | `30`                   |
| `TEMP_BACKUP_DIR`             | Temp directory            | `/tmp/mongodb-backups` |
| `RUN_BACKUP_ON_START`         | Run backup on startup     | `false`                |
| `WEB_PORT`                    | Web UI port, e.g. `8080`  | Optional               |

## Local Development

```bash
cp .env.example .env
# Edit .env with your values

go mod download
go run ./cmd/backup --once
```

For a complete step-by-step run guide, see [How to run.md](How%20to%20run.md).

## Google Cloud Setup

1. Create a Google Cloud project.
2. Enable the Google Drive API.
3. Create a service account.
4. Download the service account JSON key.
5. Share the target Google Drive folder with the service account email.

## Google Drive Folder Setup

Create a folder in Google Drive and share it with the service account email. Copy the folder ID from the URL and set it as `GOOGLE_DRIVE_FOLDER_ID`.

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
- last backup status
- last uploaded file and size
- manual backup trigger

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

## Troubleshooting

- Verify `mongodump` is available inside the container: `docker run --rm <image> mongodump --version`
- Check container logs in Coolify.
- Ensure the Google service account has access to the Drive folder.
- Do not commit `.env` or JSON credentials to Git.

## Security Notes

- Never log credentials.
- Never commit production secrets.
- Use Coolify secrets for sensitive values.
- Local backup files are deleted after upload.
