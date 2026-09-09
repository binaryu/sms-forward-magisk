# SMS Forwarder (Apprise / Webhook / Telegram) Magisk 模块

[English README](../README.md) | 中文文档

这是一个把安卓手机新收到的短信转发到 **Apprise API**、**通用 Webhook** 或 **Telegram** 的极简、低功耗 Magisk 模块。

它不是一个普通 APK，而是一个运行在 Magisk 环境里的原生后台服务（Go 编译的静态 ELF 二进制）。模块启动后，通过 Linux 内核的 `inotify` 监听安卓系统短信数据库 `mmssms.db`，平时挂起无 CPU 消耗，只有收到新短信时才被唤醒并即时转发。

适合的场景：

- 备用安卓手机插着 SIM 卡，用来收验证码或通知短信
- 手机已经 root，并安装了 Magisk 或 KernelSU / APatch
- 追求极致省电、无需前台 App、不怕系统杀后台
- 想推送到自建的 **Apprise**、**通用 Webhook**（钉钉/企微/飞书/Bark/Server酱/Discord/自建API 等），或直接推送到 **Telegram**

## 核心优势

- **极低功耗**：内核级别事件监听（inotify），无需轮询，无短信时休眠，不阻止手机进入 Deep Sleep。
- **免 App 保活**：运行在 Magisk 底层，不受安卓系统电池优化、无障碍被杀、后台清理的影响。
- **直接监听底层短信数据库**：直接读取 `mmssms.db` 中新写入的 `type = 1` 短信，无需亮屏、不依赖通知栏。
- **多通道同时支持**：
  - **Apprise API**（支持预设 Key 模式与无状态模式）
  - **通用 Webhook**（支持 GET/POST/PUT，自定义 Headers、自定义 Body 模板、安全字符转义）
  - **Telegram Bot API**
- **配置极其简单**：修改单个文本文件 `/data/adb/modules/sms_tg_forwarder/config.env` 即可。

---

## 快速上手

### 第一步：安装 Magisk 模块

1. 从 Release 或本地编译生成的 `dist/sms_tg_forwarder-magisk-arm64.zip`。
2. 打开手机上的 Magisk / KernelSU。
3. 点击“模块” -> “从本地安装” -> 选择该 zip 文件。
4. 安装完成后重启手机。

### 第二步：编辑配置文件

重启后，使用 Root 文件管理器（或 `adb shell su`）打开：

```text
/data/adb/modules/sms_tg_forwarder/config.env
```

#### 场景 1：推送到 Apprise（推荐）

```sh
APPRISE_URL=http://192.168.1.100:8000/notify/sms
APPRISE_TITLE=收到来自 {{from}} 的短信
APPRISE_USE_PROXY=false
```

#### 场景 2：推送到通用 Webhook（如钉钉、飞书、企业微信、Bark等）

- **钉钉机器人**：
  ```sh
  WEBHOOK_URL=https://oapi.dingtalk.com/robot/send?access_token=YOUR_TOKEN
  WEBHOOK_METHOD=POST
  WEBHOOK_BODY={"msgtype":"text","text":{"content":"【短信】发信人: {{from}}\n内容: {{body}}"}}
  ```
- **飞书机器人**：
  ```sh
  WEBHOOK_URL=https://open.feishu.cn/open-apis/bot/v2/hook/YOUR_KEY
  WEBHOOK_METHOD=POST
  WEBHOOK_BODY={"msg_type":"text","content":{"text":"【短信】发信人: {{from}}\n内容: {{body}}"}}
  ```
- **Bark（GET 模式）**：
  ```sh
  WEBHOOK_URL=https://api.day.app/YOUR_BARK_KEY/短信来自{{from}}/{{body}}
  WEBHOOK_METHOD=GET
  ```

#### 场景 3：直接推送到 Telegram

```sh
TELEGRAM_BOT_TOKEN=123456789:AAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
TELEGRAM_CHAT_ID=123456789
PROXY_URL=http://127.0.0.1:7890
```

> 提示：上述三种通道可以任选其一，也可以配置多个**同时推送**！

### 第三步：生效配置

修改完 `config.env` 后：
- 重启手机；
- 或者通过 root shell 杀掉旧进程让其自动拉起：
  ```sh
  su -c 'kill -9 $(cat /data/adb/sms_tg_forwarder/daemon.pid)'
  ```

---

## Webhook 占位符支持

在 `WEBHOOK_URL` 和 `WEBHOOK_BODY` 中可以使用以下变量：

| 变量占位符 | 说明 |
| --- | --- |
| `{{from}}` 或 `[from]` | 发件人号码或名称（JSON 中会自动转义特殊字符，URL 中自动 URL Encode） |
| `{{body}}` 或 `[msg]` 或 `{{content}}` | 短信正文内容（自动安全转义双引号、换行等字符） |
| `{{timestamp}}` | 当前时间戳（毫秒） |
| `{{time}}` | 格式化当前时间（如 `2026-03-09 16:00:00`） |

---

## 查看日志与排错

- 查看最近日志：
  ```sh
  su -c 'tail -n 50 /data/adb/sms_tg_forwarder/daemon.log'
  ```
- 实时追踪日志：
  ```sh
  su -c 'tail -f /data/adb/sms_tg_forwarder/daemon.log'
  ```
- 检查守护进程状态：
  ```sh
  su -c 'ps -A | grep sms-tg-forwarder'
  ```
