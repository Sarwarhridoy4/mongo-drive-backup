# How to Get Google Credentials

This project uploads backups to Google Drive using a Google Cloud service account. The easiest way to create the required credentials is:

## 1. Create or select a Google Cloud project

1. Open the Google Cloud Console: <https://console.cloud.google.com/>.
2. Create a new project or select an existing one.
3. Give the project a clear name such as `mongodb-drive-backup`.

## 2. Enable the Google Drive API

1. In the Google Cloud Console, go to **APIs & Services → Library**.
2. Search for **Google Drive API**.
3. Click **Enable**.

## 3. Create a service account

1. Go to **IAM & Admin → Service Accounts**.
2. Click **Create Service Account**.
3. Enter a name such as `mongodb-drive-backup-sa`.
4. Add a description, then click **Create and continue**.
5. Do not assign any extra roles unless your deployment needs them.
6. Click **Done**.

## 4. Create a JSON key

1. Open the newly created service account.
2. Go to the **Keys** tab.
3. Click **Add Key → Create New Key**.
4. Choose **JSON** and click **Create**.
5. The browser downloads a file like `your-project-xxxx.json`.

That downloaded JSON file is the credentials you need for the environment variable:

```env
GOOGLE_SERVICE_ACCOUNT_JSON={"type":"service_account", ...}
```

In practice, paste the entire JSON object contents into the `GOOGLE_SERVICE_ACCOUNT_JSON` variable, or load the file content into your deployment platform as a secret.

## 5. Share the target Google Drive folder

1. Create or open the Google Drive folder that should receive the backup files.
2. In Google Drive, click **Share**.
3. Paste the service account email shown in the JSON file, usually in the `client_email` field.
4. Give the service account **Editor** access.
5. Copy the folder ID from the Google Drive folder URL.

The folder ID is the value you put in:

```env
GOOGLE_DRIVE_FOLDER_ID=your-folder-id
```

## 6. Use the credentials in this repository

Once the JSON file is downloaded, use it in your environment or deployment platform:

```bash
export GOOGLE_SERVICE_ACCOUNT_JSON='{"type":"service_account",...}'
```

or configure a secret in your platform such as Coolify, Docker, or GitHub Actions using the downloaded JSON value.

## 7. Important security notes

- Never commit the downloaded JSON file to Git.
- Never print or log the credentials.
- Keep the `GOOGLE_SERVICE_ACCOUNT_JSON` value inside a secret manager or environment secret store.
- Only share the Drive folder with the service account email, not with your personal Google account.
