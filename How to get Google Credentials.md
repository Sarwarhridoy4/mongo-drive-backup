# How to Get Google Credentials

This project supports two ways to authenticate with Google Drive.

- **Option A:** OAuth 2.0 for personal Gmail accounts
- **Option B:** Service account + Shared Drive for Google Workspace

Choose the option that matches your setup.

---

## Option A — OAuth 2.0 for personal Gmail

Use this if you want to upload to a normal Google Drive folder in your personal/consumer `@gmail.com` account.

### 1. Create or select a Google Cloud project

1. Open the Google Cloud Console: <https://console.cloud.google.com/>.
2. Create a new project or select an existing one.
3. Give the project a clear name such as `mongodb-drive-backup`.

### 2. Enable the Google Drive API

1. In the Google Cloud Console, go to **APIs & Services → Library**.
2. Search for **Google Drive API**.
3. Click **Enable**.

### 3. Configure the OAuth consent screen

1. Go to **APIs & Services → OAuth consent screen**.
2. Choose **External** unless you are using Google Workspace.
3. Fill in the required app info:
   - App name
   - User support email
   - Developer contact email
4. Click **Save and Continue** through the summary steps.
5. Under **Scopes**, add:
   - `.../auth/drive.file`

### 4. Create OAuth 2.0 client credentials

1. Go to **APIs & Services → Credentials**.
2. Click **Create Credentials → OAuth client ID**.
3. Application type: **Desktop app**.
4. Click **Create**.
5. Download the JSON file.
6. Rename it to `oauth-credentials.json` and place it in your secrets directory, for example:
   - `./secrets/oauth-credentials.json`

This file should look like:

```json
{
  "installed": {
    "client_id": "...",
    "project_id": "...",
    "auth_uri": "https://accounts.google.com/o/oauth2/auth",
    "token_uri": "https://oauth2.googleapis.com/token",
    "auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
    "client_secret": "...",
    "redirect_uris": ["http://localhost"]
  }
}
```

### 5. Authorize the app and generate a token

Set these environment variables:

```env
GOOGLE_OAUTH_CREDENTIALS_FILE=./secrets/oauth-credentials.json
GOOGLE_OAUTH_TOKEN_FILE=/tmp/token.json
GOOGLE_DRIVE_FOLDER_ID=your-folder-id
```

Then run the app once:

```bash
./run.sh --once
```

The first run performs the OAuth flow, generates `token.json`, and then proceeds with the backup. Future runs reuse the refresh token automatically.

### 6. Important security notes for OAuth

- Never commit `oauth-credentials.json` or `token.json` to Git.
- `token.json` contains a long-lived refresh token; treat it like a password.
- Use `.gitignore` to exclude secret files.
- If the token stops working, delete `token.json` and re-authorize by running the app again.
- In Docker/Coolify, the default token path is `/tmp/token.json`. If you need persistence, mount a volume to `/tmp` or set `GOOGLE_OAUTH_TOKEN_FILE` to a writable path.

---

## Option B — Service account + Shared Drive for Google Workspace

Use this if you have Google Workspace and want fully unattended server operation.

### 1. Create or select a Google Cloud project

1. Open the Google Cloud Console: <https://console.cloud.google.com/>.
2. Create a new project or select an existing one.
3. Give the project a clear name such as `mongodb-drive-backup`.

### 2. Enable the Google Drive API

1. In the Google Cloud Console, go to **APIs & Services → Library**.
2. Search for **Google Drive API**.
3. Click **Enable**.

### 3. Create a service account

1. Go to **IAM & Admin → Service Accounts**.
2. Click **Create Service Account**.
3. Enter a name such as `mongodb-drive-backup-sa`.
4. Add a description, then click **Create and continue**.
5. Do not assign any extra roles unless your deployment needs them.
6. Click **Done**.

### 4. Create a JSON key

1. Open the newly created service account.
2. Go to the **Keys** tab.
3. Click **Add Key → Create New Key**.
4. Choose **JSON** and click **Create**.
5. The browser downloads a file like `your-project-xxxx.json`.

Place it in your secrets directory, for example:

- `./secrets/google-service-account.json`

Then set:

```env
GOOGLE_APPLICATION_CREDENTIALS=./secrets/google-service-account.json
```

Or inline:

```env
GOOGLE_SERVICE_ACCOUNT_JSON={"type":"service_account", ...}
```

### 5. Create a Shared Drive

1. In Google Drive, go to **Shared drives → New**.
2. Name it, for example `HimuLingua Backups`.
3. Copy the Shared Drive ID from the URL.
4. Copy the ID of the target folder inside the Shared Drive.

### 6. Add the service account to the Shared Drive

1. Open the Shared Drive.
2. Click **Manage members**.
3. Add the service account email from the JSON `client_email` field.
4. Grant it **Content manager** or **Contributor**.
5. Copy the target folder ID from the URL and set:

```env
GOOGLE_SHARED_DRIVE_ID=your-shared-drive-id
GOOGLE_DRIVE_FOLDER_ID=folder-id-inside-shared-drive
```

### 7. Use the credentials in this repository

Once the JSON file is downloaded, use it in your environment or deployment platform:

```bash
export GOOGLE_APPLICATION_CREDENTIALS=./secrets/google-service-account.json
```

or configure a secret in your platform such as Coolify, Docker, or GitHub Actions using the downloaded JSON value.

---

## Common security notes for both options

- Never commit the downloaded JSON files to Git.
- Never print or log the credentials.
- Keep credential files inside a secret manager or environment secret store.
- Only grant the minimum required Drive permissions.
