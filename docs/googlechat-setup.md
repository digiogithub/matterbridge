# Google Chat Bridge Setup Guide

This guide explains how to set up and configure the Google Chat bridge for Matterbridge.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Google Cloud Console Setup](#google-cloud-console-setup)
- [Google Chat API Configuration](#google-chat-api-configuration)
- [Service Account Setup](#service-account-setup)
- [Matterbridge Configuration](#matterbridge-configuration)
  - [Option 1: HTTP Webhook](#option-1-http-webhook)
  - [Option 2: Pub/Sub](#option-2-pubsub)
- [Testing the Connection](#testing-the-connection)
- [Troubleshooting](#troubleshooting)

## Prerequisites

- Google Workspace account with admin access
- Access to [Google Cloud Console](https://console.cloud.google.com/)
- Matterbridge compiled with Google Chat support (without `-tags nogooglechat`)

## Google Cloud Console Setup

### 1. Create a Google Cloud Project

1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Click **Select a project** → **New Project**
3. Enter a project name (e.g., "Matterbridge Chat Bot")
4. Click **Create**

### 2. Enable Google Chat API

1. In your project, go to **APIs & Services** → **Library**
2. Search for "Google Chat API"
3. Click on **Google Chat API**
4. Click **Enable**

## Google Chat API Configuration

### 1. Configure OAuth Consent Screen

1. Go to **APIs & Services** → **OAuth consent screen**
2. Choose **Internal** (for Google Workspace domain) or **External**
3. Fill in the required information:
   - App name: "Matterbridge Bot"
   - User support email: Your email
   - Developer contact: Your email
4. Click **Save and Continue**
5. Add scopes → Click **Add or Remove Scopes**
6. Search and add: `https://www.googleapis.com/auth/chat.bot`
7. Click **Update** → **Save and Continue**

### 2. Configure the Chat App

1. Go to **APIs & Services** → **Google Chat API** → **Configuration**
2. Fill in these required fields:
   - **App name**: Matterbridge Bot
   - **Avatar URL**: (Optional) Your bot's avatar image URL
   - **Description**: Chat bridge for Matterbridge
3. Under **Interactive features**:
   - **Enable Interactive features**: Check this
   - **Functionality**: Choose "Receive 1:1 messages" and "Join spaces and group conversations"
4. Under **Connection settings**, choose one:
   - **Option A: HTTP endpoint** (for webhook setup)
     - App URL: `https://your-server.com:4242/` (your public endpoint)
   - **Option B: Cloud Pub/Sub** (for Pub/Sub setup)
     - Topic name: `projects/YOUR_PROJECT_ID/topics/chat-events`
5. Under **Visibility**:
   - Make available to: Choose your domain or specific users
6. Click **Save**

## Service Account Setup

### 1. Create Service Account

1. Go to **IAM & Admin** → **Service Accounts**
2. Click **Create Service Account**
3. Service account details:
   - Name: `matterbridge-chat-bot`
   - ID: Auto-generated or customize
   - Description: "Service account for Matterbridge Google Chat integration"
4. Click **Create and Continue**
5. Grant role: **Chat Bot** (or basic Editor)
6. Click **Continue** → **Done**

### 2. Create and Download Credentials

1. Click on the newly created service account
2. Go to **Keys** tab
3. Click **Add Key** → **Create new key**
4. Choose **JSON** format
5. Click **Create**
6. Save the downloaded JSON file securely (e.g., `/etc/matterbridge/google-chat-credentials.json`)

### 3. Enable Domain-Wide Delegation (if needed)

If you need the bot to impersonate users:

1. In the service account details, click **Show Domain-Wide Delegation**
2. Check **Enable Google Workspace Domain-wide Delegation**
3. Product name: "Matterbridge Bot"
4. Save the Client ID
5. Go to [Google Workspace Admin Console](https://admin.google.com/)
6. Navigate to **Security** → **API Controls** → **Domain-wide Delegation**
7. Click **Add new**
8. Client ID: Paste the service account Client ID
9. OAuth Scopes: `https://www.googleapis.com/auth/chat.bot`
10. Click **Authorize**

## Matterbridge Configuration

### Option 1: HTTP Webhook

This is the simpler option but requires a publicly accessible endpoint.

**Configuration in `matterbridge.toml`:**

```toml
[googlechat]
    [googlechat.work]
    # Path to service account credentials JSON file
    CredentialsFile = "/etc/matterbridge/google-chat-credentials.json"
    
    # HTTP webhook endpoint (must be publicly accessible)
    WebhookBindAddress = "0.0.0.0:4242"
    
    # Optional: Show user typing notifications
    ShowUserTyping = false
    
    # Optional: Prefix messages with username
    PrefixMessagesWithNick = true

[[gateway]]
    name = "gateway1"
    enable = true
    
    [[gateway.inout]]
    account = "googlechat.work"
    channel = "spaces/AAAAAAAAAAA"  # Your Google Chat space ID
    
    [[gateway.inout]]
    account = "slack.myteam"
    channel = "general"
```

**Important Notes:**
- The `WebhookBindAddress` endpoint **must be publicly accessible** via HTTPS
- Use a reverse proxy (nginx/caddy) with SSL certificate
- Update the Chat App configuration in Google Cloud Console with your public URL

### Option 2: Pub/Sub

This is more robust and recommended for production use.

#### Setup Pub/Sub

1. **Create Pub/Sub Topic:**
   ```bash
   gcloud pubsub topics create chat-events --project=YOUR_PROJECT_ID
   ```

2. **Create Subscription:**
   ```bash
   gcloud pubsub subscriptions create chat-events-sub \
     --topic=chat-events \
     --project=YOUR_PROJECT_ID
   ```

3. **Grant Permissions:**
   ```bash
   gcloud pubsub topics add-iam-policy-binding chat-events \
     --member=serviceAccount:chat-api-push@system.gserviceaccount.com \
     --role=roles/pubsub.publisher \
     --project=YOUR_PROJECT_ID
   ```

**Configuration in `matterbridge.toml`:**

```toml
[googlechat]
    [googlechat.work]
    # Path to service account credentials JSON file
    CredentialsFile = "/etc/matterbridge/google-chat-credentials.json"
    
    # Pub/Sub configuration
    PubSubProject = "YOUR_PROJECT_ID"
    PubSubSubscription = "chat-events-sub"
    
    # Optional settings
    PrefixMessagesWithNick = true
    ShowUserTyping = false

[[gateway]]
    name = "gateway1"
    enable = true
    
    [[gateway.inout]]
    account = "googlechat.work"
    channel = "spaces/AAAAAAAAAAA"  # Your Google Chat space ID
    
    [[gateway.inout]]
    account = "slack.myteam"
    channel = "general"
```

## Testing the Connection

### 1. Find Your Space ID

1. Open Google Chat in your browser
2. Navigate to the space you want to bridge
3. Look at the URL: `https://chat.google.com/room/AAAAAAAAAAA`
4. The space ID is the part after `/room/` (format: `spaces/AAAAAAAAAAA`)

### 2. Invite the Bot to Your Space

1. In Google Chat, open the space
2. Click the space name → **View members**
3. Click **Add people & apps**
4. Search for your bot name (e.g., "Matterbridge Bot")
5. Click **Add**

### 3. Start Matterbridge

```bash
./matterbridge -conf matterbridge.toml -debug
```

### 4. Send Test Messages

1. Send a message in Google Chat space
2. Check if it appears in the bridged platform (e.g., Slack)
3. Send a message from the other platform
4. Check if it appears in Google Chat

## Troubleshooting

### Bot Not Receiving Messages

**Check:**
- Service account credentials are valid
- Google Chat API is enabled
- Bot is properly added to the space
- For webhook: Endpoint is publicly accessible via HTTPS
- For Pub/Sub: Subscription is created and has proper permissions

**Debug:**
```bash
# Check matterbridge logs
./matterbridge -conf matterbridge.toml -debug

# For Pub/Sub, verify messages are being published:
gcloud pubsub subscriptions pull chat-events-sub \
  --limit=5 \
  --project=YOUR_PROJECT_ID
```

### Permission Errors

**Error:** `403 Forbidden` or `Permission denied`

**Solution:**
- Verify service account has "Chat Bot" role
- Check OAuth scopes include `https://www.googleapis.com/auth/chat.bot`
- For domain-wide delegation, ensure admin has authorized the Client ID

### Messages Not Sending

**Check:**
- Space ID is correct (format: `spaces/AAAAAAAAAAA`)
- Bot is a member of the space
- Message format is valid

### Webhook SSL Certificate Issues

If using self-signed certificates for testing:

**Not recommended for production**, but for testing:
```toml
[googlechat.work]
SkipTLSVerify = true  # Only for testing!
```

## Additional Configuration Options

```toml
[googlechat.work]
# Required
CredentialsFile = "/path/to/credentials.json"

# Message receiving (choose one)
WebhookBindAddress = "0.0.0.0:4242"       # HTTP webhook
# OR
PubSubProject = "YOUR_PROJECT_ID"          # Pub/Sub
PubSubSubscription = "chat-events-sub"     # Pub/Sub

# Optional settings
PrefixMessagesWithNick = true              # Add username prefix
ShowUserTyping = false                     # Show typing indicators
MessageClipped = " <clipped>"              # Suffix for clipped messages
EditDisable = false                        # Disable message editing
EditSuffix = " (edited)"                   # Suffix for edited messages
```

## Support and Resources

- [Google Chat API Documentation](https://developers.google.com/workspace/chat)
- [Matterbridge Wiki](https://github.com/42wim/matterbridge/wiki)
- [Matterbridge Issues](https://github.com/42wim/matterbridge/issues)

## Security Best Practices

1. **Protect Credentials**: Never commit credentials JSON to version control
2. **Use HTTPS**: Always use HTTPS for webhook endpoints
3. **Minimal Permissions**: Grant only necessary roles to service account
4. **Rotate Keys**: Periodically rotate service account keys
5. **Firewall Rules**: Restrict webhook endpoint access to Google IP ranges
6. **Monitor Logs**: Regularly check logs for suspicious activity
