# SMS Forwarder (Apprise / Webhook / Telegram) Magisk Module

English | [中文文档](docs/zh-CN.md)

This project is a small, ultra-low-power Magisk module that forwards new inbound SMS messages from an Android phone to **Apprise API**, **Custom Webhooks**, and/or **Telegram**.

It installs a native Go daemon as a Magisk boot service. The daemon watches Android's telephony SQLite database (`mmssms.db`) via Linux kernel `inotify`, reads only new rows from the `sms` table where `type = 1`, and dispatches messages to any configured destination.

## Why This Project Exists

- **Extremely low power consumption**: Kernel-backed `inotify` event monitoring. The daemon sleeps completely when idle, allowing the device to enter Deep Sleep.
- **No background killer issues**: Runs as a Magisk native service, completely independent of Android app lifecycle, battery savers, or notification permissions.
- **Direct database access**: Directly reads the SQLite database, never misses SMS even if notifications are silenced or the screen is locked.
- **Multiple Forwarding Channels**:
  - **Apprise API**: Fan out to 150+ notification services (WeChat, Bark, DingTalk, Email, Pushover, Discord, etc.).
  - **Custom Webhooks**: Support GET/POST/PUT with custom headers, JSON templates, and safe string escaping.
  - **Telegram Bot API**: Direct native Telegram bot delivery with proxy support.

## Quick Start

1. Build or download the flashable zip: `dist/sms_tg_forwarder-magisk-arm64.zip`.
2. Install the zip in Magisk / KernelSU / APatch and reboot.
3. Edit `/data/adb/modules/sms_tg_forwarder/config.env`.
4. Restart the service or reboot:
```sh
su -c 'kill -9 $(cat /data/adb/sms_tg_forwarder/daemon.pid)'
```

See [docs/zh-CN.md](docs/zh-CN.md) for full configuration details and troubleshooting guides.
