# AGENT.md — MongoDB Google Drive Backup Service

## 1. Project Overview

Build a small, production-ready Go service that periodically creates a backup of a MongoDB database and uploads the backup to Google Drive.

The application will be deployed as a Docker container on **Coolify**.

### Core workflow

```text
Scheduler
   ↓
Create MongoDB backup
   ↓
Compress backup
   ↓
Upload to Google Drive
   ↓
Verify upload
   ↓
Cleanup local temporary files
   ↓
Log result
```

The service should run unattended and be suitable for long-term server operation.

---

## 2. Primary Goals

The application must:

1. Connect to MongoDB using a connection string.
2. Run `mongodump` to create a database backup.
3. Compress the backup into a `.tar.gz` archive.
4. Generate a timestamped backup filename.
5. Upload the archive to Google Drive.
6. Store backups inside a configurable Google Drive folder.
7. Automatically remove temporary local backup files.
8. Run automatically according to a configurable schedule.
9. Log successful and failed backup operations.
10. Exit gracefully when receiving SIGTERM/SIGINT.
11. Work correctly inside Docker.
12. Be deployable through Coolify without requiring manual intervention.

---

# 3. Non-Goals

Keep the first version simple.

Do NOT implement:

* Web dashboard
* User authentication
* Frontend
* Database administration UI
* Complex job queues
* Redis
* Kubernetes
* Microservices
* Custom distributed scheduler
* Custom backup format
* Client-side encryption

These can be added later if required.

---

# 4. Recommended Technology Stack

## Backend

* Go
* Go modules
* Standard Go library where practical

## MongoDB Backup

Use the official MongoDB Database Tools:

```text
mongodump
```

The application should invoke `mongodump` using `os/exec`.

Do not implement the MongoDB BSON backup format manually.

## Compression

Use standard Go libraries where practical:

* `archive/tar`
* `compress/gzip`

## Google Drive

Use the official Google Drive API Go client.

Recommended package:

```text
google.golang.org/api/drive/v3
```

Use Google OAuth2/service-account authentication.

## Container

Use Docker.

Prefer a multi-stage Docker build.

The final runtime image should contain:

* Compiled Go binary
* `mongodump`
* Required CA certificates

---

# 5. Project Structure

Use a simple structure similar to:

```text
mongo-drive-backup/
├── cmd/
│   └── backup/
│       └── main.go
│
├── internal/
│   ├── config/
│   │   └── config.go
│   │
│   ├── backup/
│   │   ├── mongodb.go
│   │   └── archive.go
│   │
│   ├── drive/
│   │   └── drive.go
│   │
│   ├── scheduler/
│   │   └── scheduler.go
│   │
│   └── logger/
│       └── logger.go
│
├── Dockerfile
├── .dockerignore
├── .gitignore
├── go.mod
├── go.sum
├── AGENT.md
├── README.md
└── .env.example
```

Keep packages small and focused.

---

# 6. Configuration

Configuration must come from environment variables.

Never hard-code credentials.

Recommended variables:

```env
APP_ENV=production

MONGODB_URI=mongodb://username:password@mongodb:27017
MONGODB_DATABASE=mydatabase

BACKUP_SCHEDULE=0 2 * * *
BACKUP_TIMEZONE=Asia/Dhaka

GOOGLE_DRIVE_FOLDER_ID=

GOOGLE_SERVICE_ACCOUNT_JSON=

BACKUP_RETENTION_DAYS=30

TEMP_BACKUP_DIR=/tmp/mongodb-backups
```

The exact environment variable names may be changed during implementation, but they must be documented in `.env.example` and `README.md`.

---

# 7. MongoDB Backup

The application should execute something equivalent to:

```bash
mongodump \
  --uri="$MONGODB_URI" \
  --db="$MONGODB_DATABASE" \
  --out="/tmp/backup"
```

Do not expose the MongoDB URI in logs.

The command must:

1. Start successfully.
2. Capture stdout/stderr.
3. Detect non-zero exit codes.
4. Return a useful error.
5. Stop the backup process if the application is shutting down.

Prefer passing credentials through the MongoDB URI rather than printing credentials in command arguments or logs.

---

# 8. Backup Naming

Backups must have predictable timestamp-based names.

Example:

```text
2026-09-08-020000-mydb.tar.gz
```

Recommended timestamp format:

```text
2006-01-02-150405
```

Use UTC or the configured backup timezone consistently.

The filename should contain:

* database name
* date
* time

---

# 9. Compression

After `mongodump` completes:

```text
MongoDB
   ↓
mongodump directory
   ↓
tar
   ↓
gzip
   ↓
YYYY-MM-DD-HHMMSS-database.tar.gz
```

The archive should preserve the MongoDB dump directory structure.

If compression fails:

* Mark the backup as failed.
* Do not upload an incomplete archive.
* Delete temporary files.
* Log the error.

---

# 10. Google Drive Upload

Use the Google Drive API.

The application should upload the backup into:

```text
GOOGLE_DRIVE_FOLDER_ID
```

The uploaded file should use the generated backup filename.

Example:

```text
2026-09-08-020000-himulingua.tar.gz
```

The upload should:

1. Authenticate.
2. Create the Drive file metadata.
3. Upload the archive.
4. Verify that Google Drive returned a valid file ID.
5. Log the successful upload.

Example log:

```text
backup completed
file=2026-09-08-020000-himulingua.tar.gz
drive_file_id=abc123
size=184MB
duration=42s
```

Never log:

* Service account private key
* OAuth credentials
* MongoDB password
* Access tokens
* Refresh tokens

---

# 11. Google Service Account

Prefer a Google Cloud service account for server-to-server backups.

The service account should have only the permissions required to upload backups.

Recommended approach:

1. Create a Google Cloud project.
2. Enable Google Drive API.
3. Create a service account.
4. Create/download its JSON credentials.
5. Share the target Google Drive folder with the service account email.
6. Give the service account appropriate access to that folder.
7. Store the credential securely in Coolify.

Do not commit the service-account JSON file to Git.

---

# 12. Credential Handling

Credentials must never be committed to Git.

The following must be ignored:

```gitignore
.env
*.json
credentials/
secrets/
```

If using:

```env
GOOGLE_SERVICE_ACCOUNT_JSON
```

support either:

### Option A — JSON stored directly in environment variable

```env
GOOGLE_SERVICE_ACCOUNT_JSON={"type":"service_account",...}
```

### Option B — JSON file mounted into the container

Example:

```env
GOOGLE_APPLICATION_CREDENTIALS=/run/secrets/google-service-account.json
```

Prefer the method that integrates most cleanly with Coolify secrets.

Never print the credential.

---

# 13. Scheduler

The service must run continuously.

Example:

```text
Application starts
       ↓
Validate configuration
       ↓
Run scheduler
       ↓
Wait for scheduled time
       ↓
Run backup
       ↓
Wait for next scheduled time
```

The schedule should be configurable.

Example:

```env
BACKUP_SCHEDULE=0 2 * * *
```

This means:

```text
Every day at 02:00
```

The scheduler should not spawn unlimited concurrent backup jobs.

There must be at most:

```text
1 backup job running
```

at any time.

If a scheduled backup is still running when the next schedule occurs, skip the new execution and log:

```text
backup already running; skipping scheduled execution
```

---

# 14. Cron Library

A lightweight cron library may be used.

Preferred:

```text
github.com/robfig/cron/v3
```

Use timezone-aware scheduling.

Example:

```env
BACKUP_TIMEZONE=Asia/Dhaka
```

Do not assume the server timezone.

---

# 15. Startup Behavior

When the application starts:

1. Load environment variables.
2. Validate required configuration.
3. Initialize logging.
4. Initialize Google Drive client.
5. Verify MongoDB configuration.
6. Start scheduler.
7. Log that the service is ready.

Example:

```text
INFO backup service starting
INFO configuration loaded
INFO google drive client initialized
INFO scheduler started schedule="0 2 * * *" timezone="Asia/Dhaka"
INFO backup service ready
```

Do not automatically run a backup on startup unless explicitly configured.

Optional variable:

```env
RUN_BACKUP_ON_START=false
```

---

# 16. Manual Backup

The application should support a manual one-time backup mode.

Example:

```bash
./backup --once
```

This should:

1. Execute one backup.
2. Upload it.
3. Exit with status `0` on success.
4. Exit with non-zero status on failure.

This is useful for testing and troubleshooting.

---

# 17. Health / Monitoring

The first version does not require a web server.

However, the application must provide useful logs so Coolify/container logs can be used for monitoring.

Recommended log events:

```text
service_started
scheduler_started
backup_started
mongodb_dump_started
mongodb_dump_completed
archive_started
archive_completed
drive_upload_started
drive_upload_completed
backup_completed
backup_failed
cleanup_completed
service_shutdown
```

Use structured logging where practical.

JSON logs are preferred for production.

Example:

```json
{
  "level": "info",
  "event": "backup_completed",
  "file": "2026-09-08-020000-mydb.tar.gz",
  "duration_seconds": 42
}
```

---

# 18. Error Handling

Every backup operation should follow:

```text
START
 ↓
mongodump
 ↓
verify dump
 ↓
compress
 ↓
verify archive
 ↓
upload
 ↓
verify Drive file ID
 ↓
cleanup
 ↓
SUCCESS
```

If any step fails:

```text
FAIL
 ↓
log error
 ↓
cleanup temporary files
 ↓
continue scheduler
```

A failed backup must not crash the entire scheduler.

The service should remain alive and attempt the next scheduled backup.

---

# 19. Cleanup

Temporary files must always be cleaned up.

Use:

```go
defer os.RemoveAll(tempDirectory)
```

or an equivalent cleanup strategy.

Cleanup must happen on:

* Successful backup
* Failed backup
* Upload failure
* Compression failure
* MongoDB dump failure

Do not allow `/tmp` to fill up over time.

---

# 20. Retention

Optional Google Drive retention should be supported.

Configuration:

```env
BACKUP_RETENTION_DAYS=30
```

If enabled, the service may delete old backup files from the configured Google Drive folder.

Only delete files that:

1. Exist inside the configured backup folder.
2. Match the application's backup filename pattern.
3. Are older than the configured retention period.

Never delete unrelated files.

If retention is not implemented in the first version, keep the variable documented as future functionality rather than implementing unsafe deletion logic.

---

# 21. Docker

Use a multi-stage Dockerfile.

Conceptually:

```text
Go builder
    ↓
compile static Go binary
    ↓
runtime image
    ↓
install MongoDB Database Tools
    ↓
copy Go binary
    ↓
run service
```

The container must include:

```bash
mongodump
```

Verify during image build:

```bash
mongodump --version
```

The container should run as a non-root user where practical.

---

# 22. Docker Entrypoint

The container should start the Go application directly.

Example:

```dockerfile
CMD ["/app/backup"]
```

Do not use:

```bash
tail -f /dev/null
```

or any fake long-running process.

The Go application itself must remain alive while the scheduler is active.

---

# 23. Coolify Deployment

The project must be compatible with Coolify.

Deployment flow:

```text
GitHub
   ↓
Coolify
   ↓
Docker build
   ↓
Container
   ↓
Scheduler
   ↓
MongoDB backup
   ↓
Google Drive
```

Recommended Coolify configuration:

### Build

Use the repository's:

```text
Dockerfile
```

### Persistent storage

The application does not require persistent storage for backups because backups are uploaded to Google Drive.

Temporary backup files should live inside:

```text
/tmp
```

or another temporary directory.

Do not persist backup archives on the container unless explicitly required.

---

# 24. Coolify Environment Variables

Configure secrets through:

```text
Coolify → Environment Variables
```

Do not commit production credentials to Git.

Example:

```env
APP_ENV=production

MONGODB_URI=...
MONGODB_DATABASE=...

BACKUP_SCHEDULE=0 2 * * *
BACKUP_TIMEZONE=Asia/Dhaka

GOOGLE_DRIVE_FOLDER_ID=...

GOOGLE_SERVICE_ACCOUNT_JSON=...

BACKUP_RETENTION_DAYS=30
```

Use Coolify's secret/environment-variable functionality for sensitive values.

---

# 25. MongoDB Connectivity

The application must support MongoDB hosted outside the container.

Examples:

```text
MongoDB Atlas
```

or:

```text
MongoDB on another VPS
```

or:

```text
MongoDB running as another Coolify service
```

Do not assume MongoDB is running inside the same container.

The MongoDB URI must be configurable.

Example:

```env
MONGODB_URI=mongodb+srv://...
```

---

# 26. Security Requirements

The application handles database backups, so security is important.

Requirements:

* Never log database credentials.
* Never log Google credentials.
* Never commit secrets.
* Use HTTPS/TLS MongoDB connections where available.
* Use least-privilege Google Drive access.
* Restrict the Google service account to the backup folder where practical.
* Delete local backup files after upload.
* Do not expose backup files through an HTTP endpoint.
* Do not expose MongoDB credentials through command output.
* Do not include credentials in error messages.

---

# 27. Graceful Shutdown

Handle:

```text
SIGTERM
SIGINT
```

On shutdown:

1. Stop accepting new scheduled jobs.
2. Stop the scheduler.
3. Allow a currently running backup to finish if practical.
4. Cancel long-running operations after a reasonable timeout.
5. Remove temporary files.
6. Exit cleanly.

Use Go contexts:

```go
context.WithCancel(...)
```

and propagate the context through:

```text
backup
 → mongodump
 → archive
 → Google Drive upload
```

---

# 28. Testing

Implement unit tests for:

* Configuration validation
* Backup filename generation
* Archive creation
* Archive validation
* Scheduler configuration
* Retention filename matching
* Cleanup behavior

Integration testing should cover:

```text
MongoDB
   ↓
mongodump
   ↓
archive
   ↓
Google Drive
```

Do not require production credentials in unit tests.

Use mocks/interfaces for Google Drive where appropriate.

---

# 29. CLI

Recommended commands:

```bash
backup
backup --once
backup --version
backup --help
```

Example:

```bash
./backup --once
```

Expected behavior:

```text
Starting one-time backup...

MongoDB dump: OK
Archive: OK
Google Drive upload: OK
Cleanup: OK

Backup completed successfully.
```

---

# 30. Exit Codes

Use standard exit behavior.

```text
0 = success
1 = general failure
2 = invalid configuration
```

For scheduled mode, an individual backup failure should NOT terminate the service.

For:

```bash
backup --once
```

a backup failure should return a non-zero exit code.

---

# 31. README Requirements

Create a `README.md` containing:

1. Project description
2. Architecture
3. Requirements
4. Environment variables
5. Local development instructions
6. Google Cloud setup
7. Google Drive folder setup
8. MongoDB configuration
9. Running locally
10. Running a manual backup
11. Docker usage
12. Coolify deployment
13. Troubleshooting
14. Security notes

---

# 32. Local Development

Local development should work with:

```bash
go mod download
go run ./cmd/backup --once
```

Provide:

```text
.env.example
```

Never provide real credentials.

Developers should be able to test the archive logic independently of Google Drive.

---

# 33. Implementation Order

Implement in this order.

### Phase 1 — Project

* Initialize Go module.
* Create project structure.
* Add configuration loader.
* Add logging.
* Add CLI.

### Phase 2 — MongoDB

* Implement `mongodump` execution.
* Capture output.
* Handle errors.
* Implement cleanup.

### Phase 3 — Archive

* Implement tar creation.
* Implement gzip compression.
* Generate timestamped filename.
* Verify archive.

### Phase 4 — Google Drive

* Implement Google authentication.
* Implement Drive client.
* Implement folder upload.
* Verify upload.

### Phase 5 — Scheduler

* Add cron scheduler.
* Add timezone support.
* Prevent concurrent backups.
* Add graceful shutdown.

### Phase 6 — Docker

* Create multi-stage Dockerfile.
* Install MongoDB Database Tools.
* Verify `mongodump`.
* Run as non-root where practical.

### Phase 7 — Production

* Create `.env.example`.
* Create README.
* Add tests.
* Test manual backup.
* Deploy to Coolify.

---

# 34. Definition of Done

The project is considered complete when:

* [ ] `go test ./...` passes.
* [ ] `go build ./...` passes.
* [ ] `mongodump` works inside the Docker container.
* [ ] MongoDB backup can be generated.
* [ ] Backup is compressed successfully.
* [ ] Backup uploads to Google Drive.
* [ ] Uploaded file appears in the configured Drive folder.
* [ ] Local temporary files are deleted.
* [ ] Scheduler runs according to configuration.
* [ ] Two backup jobs cannot run simultaneously.
* [ ] SIGTERM is handled gracefully.
* [ ] Credentials are not logged.
* [ ] No production secrets exist in Git.
* [ ] Docker image builds successfully.
* [ ] Container runs successfully on Coolify.
* [ ] Manual `--once` backup works.
* [ ] README documents the complete setup.

---

# 35. Coding Principles

Keep the code simple.

Prefer:

```text
small functions
clear interfaces
explicit errors
context propagation
structured logs
configuration through environment variables
```

Avoid:

```text
unnecessary abstractions
over-engineering
global mutable state
hard-coded configuration
hidden retries
silent failures
```

Use idiomatic Go.

Run:

```bash
gofmt -w .
go vet ./...
go test ./...
```

before considering a feature complete.

---

# 36. Reliability Requirements

A backup system must fail safely.

Important rule:

> Never report a backup as successful unless the MongoDB dump completed, the archive was successfully created, and Google Drive confirmed the upload.

If Google Drive upload fails:

```text
backup = FAILED
```

If MongoDB dump fails:

```text
backup = FAILED
```

If cleanup fails after a successful upload:

```text
backup = SUCCESS_WITH_CLEANUP_WARNING
```

The original uploaded backup should not be deleted merely because local cleanup failed.

---

# 37. Future Extensions

Keep the architecture extensible for future features:

```text
MongoDB
PostgreSQL
MySQL
     ↓
Backup providers
     ↓
Archive
     ↓
Storage providers
     ├── Google Drive
     ├── S3
     ├── Backblaze B2
     └── Cloudflare R2
```

Possible future features:

* Email/Telegram notifications
* Backup encryption
* Multiple MongoDB databases
* Multiple backup schedules
* S3-compatible storage
* Backup integrity checks
* Remote backup verification
* Web health endpoint
* Prometheus metrics
* Backup history
* Automatic restore testing

Do not implement these unless explicitly requested.

---

# 38. Agent Instructions

When modifying this project:

1. Read `AGENT.md` first.
2. Preserve the simple architecture.
3. Do not introduce unnecessary dependencies.
4. Never hard-code secrets.
5. Never commit credentials.
6. Never expose credentials in logs.
7. Test changes locally.
8. Run `gofmt`.
9. Run `go vet ./...`.
10. Run `go test ./...`.
11. Verify Docker build.
12. Verify `mongodump` exists in the runtime image.
13. Update `README.md` when configuration or deployment behavior changes.
14. Keep changes focused and production-safe.

Before adding a new dependency, determine whether the Go standard library can solve the problem.

Before changing the backup workflow, preserve the invariant:

```text
MongoDB dump
    ↓
valid archive
    ↓
successful Google Drive upload
    ↓
cleanup
```

The primary objective is **reliable automated backups**, not feature count.
