# SMS Telegram Forwarder Magisk Module

English | [中文文档](docs/zh-CN.md)

This project is a small Magisk module that forwards new inbound SMS messages from an Android phone to Telegram.

It installs a native Go daemon as a Magisk boot service. The daemon watches Android's telephony SQLite database, reads only new rows from the `sms` table where `type = 1`, and sends each message to a Telegram chat through the Telegram Bot API.

The module is designed for a spare rooted Android phone that receives verification codes, bank alerts, carrier messages, or other SMS messages and needs to relay them to Telegram with as little Android app machinery as possible.

## Why This Project Exists

[SmsForwarder](https://github.com/pppscn/SmsForwarder) is a full Android app with a large feature set: SMS, calls, app notifications, many forwarding channels, rule engines, remote control, automation, and a UI.

This module is intentionally narrower. It is useful when you only need one job:

- receive SMS on a rooted Android phone
- forward new inbound messages to Telegram
- keep the setup easy to inspect and maintain

Compared with SmsForwarder or similar SMS forwarding apps, this module's advantages are:

- **Magisk-native startup**: runs from Magisk `service.sh` after boot, without relying on a foreground Android app UI.
- **Small scope**: Telegram-only SMS forwarding, which means fewer features to configure and fewer moving parts to debug.
- **Kernel-backed event monitoring**: uses Linux file change events through `fsnotify`/inotify to watch `mmssms.db` and its WAL file. It does not need an Android UI service constantly checking for messages.
- **Extremely low power use**: the daemon sleeps most of the time and wakes only when the SMS database changes, so it is well suited for a spare phone that may sit idle for long periods.
- **Direct database monitoring**: reads the system SMS database directly instead of depending on Android notification access or a complex app permission flow.
- **Plain text configuration**: all runtime options live in `/data/adb/modules/sms_tg_forwarder/config.env`.
- **Root-friendly deployment**: packaged as a Magisk zip and built as a static arm64 Go binary.
- **Proxy and CA support**: works with a local HTTP proxy such as `http://127.0.0.1:7890`, Android system CAs, and optional custom CA files.

Tradeoffs:

- It requires root and Magisk.
- It only forwards inbound SMS to Telegram.
- It does not forward calls, app notifications, emails, webhooks, or other channels.
- It does not provide a visual rule editor or remote-control features.
- On first run, it starts from the current newest SMS and does not forward old messages by default.

## How It Works

1. Magisk starts `sms_tg_forwarder/service.sh` after boot.
2. `service.sh` loads `/data/adb/modules/sms_tg_forwarder/config.env`.
3. The Go daemon starts from `/data/adb/sms_tg_forwarder`.
4. The daemon watches:
   - `/data/user_de/0/com.android.providers.telephony/databases/mmssms.db`
   - `/data/user_de/0/com.android.providers.telephony/databases/mmssms.db-wal`
5. When the database changes, it queries new inbound SMS rows after the last processed `_id`.
6. Each message is sent to Telegram as:

```text
Sender: <phone number or sender id>
Content: <message body>
```

## Beginner Setup

### 1. Prepare a Telegram bot

1. Open Telegram and search for `@BotFather`.
2. Send `/newbot`.
3. Follow the prompts and copy the bot token. It looks like:

```text
123456789:AAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

Keep this token private. Anyone with the token can control your bot.

### 2. Get your Telegram chat ID

For a personal chat:

1. Open a chat with your new bot.
2. Send any message to it, for example `hello`.
3. Open this URL in a browser, replacing `<TOKEN>` with your bot token:

```text
https://api.telegram.org/bot<TOKEN>/getUpdates
```

4. Find `chat":{"id":...}` in the response. That number is your `TELEGRAM_CHAT_ID`.

For a group, add the bot to the group, send a message in the group, then call the same `getUpdates` URL. Group chat IDs are usually negative numbers.

### 3. Install the Magisk module

Use the packaged zip if it already exists:

```text
dist/sms_tg_forwarder-magisk-arm64.zip
```

Install it in Magisk:

1. Open Magisk.
2. Go to **Modules**.
3. Choose **Install from storage**.
4. Select `sms_tg_forwarder-magisk-arm64.zip`.
5. Reboot the phone.

### 4. Edit the config file

After reboot, edit:

```text
/data/adb/modules/sms_tg_forwarder/config.env
```

Minimum config:

```sh
TELEGRAM_BOT_TOKEN=123456789:AAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
TELEGRAM_CHAT_ID=123456789

DB_PATH=/data/user_de/0/com.android.providers.telephony/databases/mmssms.db
LAST_ID_PATH=

PROXY_URL=http://127.0.0.1:7890

CA_CERT_DIR=/system/etc/security/cacerts
CA_CERT_FILE=
TLS_INSECURE_SKIP_VERIFY=false
```

The current daemon always builds its HTTP client with `PROXY_URL`. The default assumes a local proxy is listening at `127.0.0.1:7890`. If your phone can reach Telegram directly and you do not want to use a proxy, adjust the source before building.

### 5. Restart the service

The simplest way is to reboot the phone again.

You can also stop the current daemon and let Magisk start it on the next boot:

```sh
adb shell su -c 'kill $(cat /data/adb/sms_tg_forwarder/daemon.pid)'
```

### 6. Test it

Send a new SMS to the phone. If everything is configured correctly, the Telegram chat should receive a message with the sender and content.

The first run initializes `last_id.txt` to the newest existing SMS. This prevents old messages from being forwarded in bulk. Only messages received after the daemon starts will be forwarded.

## Configuration Reference

Active config file after installation:

```text
/data/adb/modules/sms_tg_forwarder/config.env
```

Available options:

| Option | Required | Default | Description |
| --- | --- | --- | --- |
| `TELEGRAM_BOT_TOKEN` | Yes | empty | Telegram bot token from BotFather. |
| `TELEGRAM_CHAT_ID` | Yes | empty | Target private chat, group, or channel chat ID. |
| `DB_PATH` | No | `/data/user_de/0/com.android.providers.telephony/databases/mmssms.db` | Android telephony SMS database path. |
| `LAST_ID_PATH` | No | `/data/adb/sms_tg_forwarder/last_id.txt` from `service.sh` runtime directory | File storing the last processed SMS `_id`. Leave empty for the default. |
| `PROXY_URL` | No | `http://127.0.0.1:7890` | HTTP proxy used to reach Telegram. |
| `CA_CERT_DIR` | No | `/system/etc/security/cacerts` | Android system root CA directory. |
| `CA_CERT_FILE` | No | empty | Optional PEM CA file, useful when a proxy performs TLS interception. |
| `TLS_INSECURE_SKIP_VERIFY` | No | `false` | Debug fallback only. Keep `false` for normal use. |

Do not commit or share a real bot token. If it leaks, revoke it in BotFather immediately.

## Logs and Troubleshooting

View recent logs:

```sh
adb shell su -c 'tail -n 100 /data/adb/sms_tg_forwarder/daemon.log'
```

Follow logs live:

```sh
adb shell su -c 'tail -f /data/adb/sms_tg_forwarder/daemon.log'
```

Check whether the daemon is running:

```sh
adb shell su -c 'cat /data/adb/sms_tg_forwarder/daemon.pid && ps -A | grep sms-tg-forwarder'
```

Stop the daemon:

```sh
adb shell su -c 'kill $(cat /data/adb/sms_tg_forwarder/daemon.pid)'
```

### `missing TELEGRAM_BOT_TOKEN or TELEGRAM_CHAT_ID`

Edit:

```text
/data/adb/modules/sms_tg_forwarder/config.env
```

Make sure both values are filled in, then reboot.

### `tls: failed to verify certificate`

Keep the default Android CA directory:

```sh
CA_CERT_DIR=/system/etc/security/cacerts
TLS_INSECURE_SKIP_VERIFY=false
```

If your local proxy intercepts HTTPS, export the proxy CA certificate as PEM and set:

```sh
CA_CERT_FILE=/path/to/proxy-ca.pem
```

Only for temporary debugging:

```sh
TLS_INSECURE_SKIP_VERIFY=true
```

### Old messages are not forwarded

This is expected. On first run, the daemon initializes:

```text
/data/adb/sms_tg_forwarder/last_id.txt
```

to the current `MAX(_id)` in the SMS database. To reprocess older messages, stop the daemon and edit that file to a smaller number. Setting it to `0` can forward every inbound SMS row the next time processing is triggered. Deleting the file does not reprocess old messages; the daemon will initialize it to the newest current `_id` again.

## Build

Build the arm64 binary:

```sh
make arm64 GO=/usr/local/go/bin/go
```

Output:

```text
dist/sms-tg-forwarder-linux-arm64
```

Build the Magisk module zip:

```sh
make magisk GO=/usr/local/go/bin/go
```

Output:

```text
dist/sms_tg_forwarder-magisk-arm64.zip
```

The build uses:

- `GOOS=linux`
- `GOARCH=arm64`
- `CGO_ENABLED=0`
- `modernc.org/sqlite`
- `github.com/fsnotify/fsnotify`

## Project Layout

```text
sms_tg_forwarder/
├── config.env
├── config.env.example
├── module.prop
├── service.sh
├── uninstall.sh
├── src/
│   ├── main.go
│   ├── go.mod
│   └── go.sum
└── system/
    ├── bin/
    │   └── sms-tg-forwarder
    └── etc/
        └── init/
            └── sms.rc
```

`sms.rc` is preserved in the module zip, but the default startup path is Magisk's `service.sh`. If you want to use the init rc file instead, make sure its service path matches the installed binary path.
