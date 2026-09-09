# SMS & Call Forwarder

中文文档 | [English](docs/README_EN.md)

基于 Go 语言开发的原生 Magisk / KernelSU / APatch 模块。
通过 Linux 内核 `inotify` 事件监听安卓底层数据库，无消息时进程完全休眠，**性能强、功耗极低、0 外部依赖**。

## 支持特性

- **监控事件**：短信（实时秒达）、未接来电（带通讯录姓名与响铃时长）
- **极低功耗**：内核级事件唤醒，非轮询，待机 0% CPU 占用，不影响手机深度睡眠
- **安全纯只读**：以只读模式读取数据库，独立游标（`last_msg.txt` / `last_call.txt`）记录进度，绝不修改系统未读状态
- **多 Root 框架兼容**：Magisk、KernelSU（无需 metamodule 直接运行）、APatch，支持管理器一键 Action 状态查看与测试
- **支持的推送通道**：
  - **Bark (iOS)**：只需配置 Key，一行搞定
  - **企业微信群机器人**：填写 Webhook 地址即可
  - **Telegram Bot**：支持 HTTP / SOCKS5 代理
  - **通用 Webhook**：支持钉钉、飞书、Server酱、自定义 HTTP API（GET/POST/PUT）
  - **Apprise API**：支持广播到 150+ 种通知服务

---

## 快速使用

### 1. 安装模块
在 [Releases](https://github.com/ShaunGao/sms-tg-forwarder/releases) 下载 `sms_tg_forwarder-arm64.zip`，在 Magisk 或 KernelSU 管理器中刷入并重启手机。

### 2. 配置通道
编辑 `/data/adb/modules/sms_tg_forwarder/config.env`（修改保存后自动热重载，无需手动重启）：

#### Bark (iOS)
```sh
BARK_KEY=你的Bark_Key
BARK_GROUP=短信转发
```

#### 企业微信群机器人
```sh
WECHAT_WEBHOOK=https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxxx-xxxx
```

#### Telegram Bot
```sh
TELEGRAM_BOT_TOKEN=123456:ABC-DEF
TELEGRAM_CHAT_ID=123456789
PROXY_URL=http://127.0.0.1:7890   # 可选代理
```

#### 钉钉 / 飞书 / 自定义 Webhook
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

## 测试与调试

- **管理器快捷控制**：KernelSU / Magisk 模块列表中点击 **Action (操作)** 按钮即可查看运行状态、配置与最新日志
- **测试短信转发**：`su -c 'touch /data/adb/sms_tg_forwarder/test'`
- **测试来电转发**：`su -c 'touch /data/adb/sms_tg_forwarder/test_call'`
- **查看实时日志**：`su -c 'tail -f /data/adb/sms_tg_forwarder/daemon.log'`

---
