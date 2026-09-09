# SMS & Call Forwarder

[中文文档](../README.md) | English

A lightweight native module for Magisk, KernelSU, and APatch built with Go.
Uses Linux kernel `inotify` to monitor Android databases with **high performance, ultra-low power consumption, and zero external dependencies**.

## Features

- **Monitored Events**: Inbound SMS messages (real-time) and missed/rejected calls (with contact name & ring duration).
- **Ultra-low Power**: Kernel-event-driven, not a polling loop. 0% CPU usage while idle, allows full Deep Sleep.
- **Strictly Read-Only**: Opens databases in `mode=ro`, tracks progress with external cursor files (`last_msg.txt` / `last_call.txt`), never modifies system databases.
- **Universal Root Support**: Magisk, KernelSU (runs without metamodules), and APatch. Supports the manager Action button.
- **Supported Notification Channels**:
  - **Bark (iOS)**: One-line setup with your key.
  - **WeChat Work (企业微信群机器人)**: Direct webhook forwarding.
  - **Telegram Bot**: Native support with optional HTTP/SOCKS5 proxy.
  - **Generic Webhooks**: DingTalk, Feishu, ServerChan, or custom HTTP APIs (GET/POST/PUT).
  - **Apprise API**: Fan out to 150+ notification services.

---

## Quick Start

### 1. Installation
Download `sms_tg_forwarder-arm64.zip` from [Releases](https://github.com/ShaunGao/sms-tg-forwarder/releases), flash in Magisk or KernelSU Manager, and reboot.

### 2. Configuration
Edit `/data/adb/modules/sms_tg_forwarder/config.env` (automatically hot-reloaded upon saving):

#### Bark (iOS)
```sh
BARK_KEY=your_bark_key
BARK_GROUP=SMS_Forwarder
```

#### WeChat Work (企业微信群机器人)
```sh
WECHAT_WEBHOOK=https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxxx-xxxx
```

#### Telegram Bot
```sh
TELEGRAM_BOT_TOKEN=123456:ABC-DEF
TELEGRAM_CHAT_ID=123456789
PROXY_URL=http://127.0.0.1:7890   # Optional proxy
```

#### Custom Webhook (DingTalk, Feishu, etc.)
```sh
WEBHOOK_URL=https://oapi.dingtalk.com/robot/send?access_token=xxxx
WEBHOOK_METHOD=POST
WEBHOOK_BODY={"msgtype":"text","text":{"content":"【{{type}}】来自: {{from}}\n内容: {{body}}\n时间: {{time}}"}}
```

#### Apprise API
```sh
APPRISE_URL=http://192.168.1.100:8000/notify/sms
```

---

## Testing & Troubleshooting

- **Manager Action**: Tap the **Action** button on the module in KernelSU / Magisk to view status and recent logs.
- **Test SMS forward**: `su -c 'touch /data/adb/sms_tg_forwarder/test'`
- **Test call forward**: `su -c 'touch /data/adb/sms_tg_forwarder/test_call'`
- **Live logs**: `su -c 'tail -f /data/adb/sms_tg_forwarder/daemon.log'`

---
