# SMS Telegram Forwarder Magisk 模块

[English README](../README.md) | 中文文档

这是一个把安卓手机新收到的短信转发到 Telegram 的 Magisk 模块。

它不是一个普通 APK，而是一个运行在 Magisk 环境里的后台服务。模块启动后，会监听安卓系统短信数据库 `mmssms.db`，只读取新收到的短信，然后通过 Telegram Bot API 发送到你指定的 Telegram 聊天里。

适合的场景：

- 备用安卓手机插着 SIM 卡，用来收验证码或通知短信
- 手机已经 root，并安装了 Magisk
- 只想把新短信转发到 Telegram
- 不想安装和维护功能很重的安卓转发 App

## 和 SmsForwarder / SMS Forward 相比有什么优势

[SmsForwarder](https://github.com/pppscn/SmsForwarder) 是一个功能很完整的安卓 App。它支持短信、来电、App 通知、多种转发通道、规则配置、远程控制和自动化。

本项目的目标不是替代 SmsForwarder 的所有功能，而是把一件事做简单：在有 root 的备用机上，把新收到的短信转发到 Telegram。

相对 SmsForwarder 或类似 SMS Forward 工具，本项目的优势是：

- **更轻量**：只做短信到 Telegram，不需要配置大量规则和通道。
- **Magisk 原生启动**：通过 Magisk 的 `service.sh` 开机启动，不依赖前台 App 界面。
- **内核级别事件监听**：通过 Linux 的 `fsnotify`/inotify 文件事件监听短信数据库和 WAL 文件变化。它不是轮询短信，也不是靠一个复杂 App 常驻前台反复检查。
- **极低功耗**：后台进程绝大多数时间都在睡眠，只有短信数据库发生变化时才被唤醒处理，非常适合长期待机的备用机。
- **直接监听短信数据库**：读取系统短信数据库，不依赖通知读取权限或复杂的 App 保活。
- **配置透明**：所有配置都在一个文本文件里，路径是 `/data/adb/modules/sms_tg_forwarder/config.env`。
- **适合 root 备用机**：以 Magisk zip 形式安装，二进制是 arm64 静态 Go 程序。
- **支持代理和证书配置**：可以使用本地 HTTP 代理，例如 `http://127.0.0.1:7890`，也支持安卓系统证书目录和自定义 CA 文件。

也要注意这些限制：

- 必须有 root 和 Magisk。
- 只转发收到的短信，不转发发出的短信。
- 只支持 Telegram，不支持邮件、钉钉、企业微信、飞书、Bark、Webhook 等通道。
- 没有图形界面和规则编辑器。
- 首次运行默认不会转发旧短信，只转发后续新增短信。

## 工作原理

1. 手机开机后，Magisk 执行模块里的 `service.sh`。
2. `service.sh` 读取 `/data/adb/modules/sms_tg_forwarder/config.env`。
3. 后台程序从 `/data/adb/sms_tg_forwarder` 目录启动。
4. 程序监听短信数据库：
   - `/data/user_de/0/com.android.providers.telephony/databases/mmssms.db`
   - `/data/user_de/0/com.android.providers.telephony/databases/mmssms.db-wal`
5. 数据库变化后，程序查询 `sms` 表里 `_id` 更新、并且 `type = 1` 的短信。
6. 每条短信会发送到 Telegram，格式如下：

```text
Sender: 发送方号码或名称
Content: 短信内容
```

## 小白使用步骤

### 第一步：准备 Telegram Bot

1. 打开 Telegram。
2. 搜索 `@BotFather`。
3. 发送 `/newbot`。
4. 按提示设置机器人名称。
5. BotFather 会给你一个 token，大概长这样：

```text
123456789:AAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

这个 token 不能公开。别人拿到 token 后就可以控制你的机器人。

### 第二步：获取 Telegram Chat ID

如果你想把短信发到自己的 Telegram 私聊：

1. 打开刚创建的机器人。
2. 给机器人发一句话，比如 `hello`。
3. 在浏览器打开下面这个地址，把 `<TOKEN>` 换成你的 bot token：

```text
https://api.telegram.org/bot<TOKEN>/getUpdates
```

4. 在返回内容里找 `chat":{"id":...}`，其中的数字就是 `TELEGRAM_CHAT_ID`。

如果你想发到群组：

1. 把机器人加入群组。
2. 在群里发一条消息。
3. 再打开上面的 `getUpdates` 地址。
4. 找到群组对应的 `chat.id`。群组 ID 通常是负数。

### 第三步：安装 Magisk 模块

如果仓库里已经有打包好的 zip，可以直接使用：

```text
dist/sms_tg_forwarder-magisk-arm64.zip
```

在手机上操作：

1. 打开 Magisk。
2. 进入“模块”。
3. 选择“从本地安装”。
4. 选择 `sms_tg_forwarder-magisk-arm64.zip`。
5. 安装完成后重启手机。

### 第四步：填写配置

重启后，编辑这个文件：

```text
/data/adb/modules/sms_tg_forwarder/config.env
```

最少需要填写这两项：

```sh
TELEGRAM_BOT_TOKEN=你的BotToken
TELEGRAM_CHAT_ID=你的ChatID
```

完整示例：

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

如果你的网络访问 Telegram 需要代理，请确保手机本机有代理服务在 `127.0.0.1:7890` 监听。

当前程序创建 HTTP 客户端时总是会使用 `PROXY_URL`。默认配置假设本机代理监听在 `127.0.0.1:7890`。如果你的手机可以直连 Telegram，并且不想使用代理，需要修改源码后重新编译。

### 第五步：重启服务

最简单的方式是再次重启手机。

如果你会用 adb，也可以停止当前进程：

```sh
adb shell su -c 'kill $(cat /data/adb/sms_tg_forwarder/daemon.pid)'
```

然后重启手机，让 Magisk 重新启动服务。

### 第六步：测试

给这台手机发一条新短信。

如果配置正确，Telegram 里会收到类似下面的消息：

```text
Sender: 10086
Content: Your verification code is 123456
```

注意：第一次运行时，程序会记录当前短信数据库里的最新 `_id`。也就是说，安装前已经存在的旧短信不会立刻转发，只有之后新收到的短信才会转发。

## 配置说明

安装后的配置文件路径：

```text
/data/adb/modules/sms_tg_forwarder/config.env
```

配置项：

| 配置项 | 是否必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `TELEGRAM_BOT_TOKEN` | 是 | 空 | BotFather 给你的 Telegram bot token。 |
| `TELEGRAM_CHAT_ID` | 是 | 空 | 接收短信的 Telegram 私聊、群组或频道 ID。 |
| `DB_PATH` | 否 | `/data/user_de/0/com.android.providers.telephony/databases/mmssms.db` | 安卓短信数据库路径。 |
| `LAST_ID_PATH` | 否 | `/data/adb/sms_tg_forwarder/last_id.txt` | 记录最后处理短信 `_id` 的文件。留空即可。 |
| `PROXY_URL` | 否 | `http://127.0.0.1:7890` | 访问 Telegram 使用的 HTTP 代理。 |
| `CA_CERT_DIR` | 否 | `/system/etc/security/cacerts` | 安卓系统根证书目录。 |
| `CA_CERT_FILE` | 否 | 空 | 自定义 PEM CA 文件。代理做 HTTPS 解密时可能需要。 |
| `TLS_INSECURE_SKIP_VERIFY` | 否 | `false` | 仅用于临时调试。正常不要打开。 |

## 查看日志

查看最近日志：

```sh
adb shell su -c 'tail -n 100 /data/adb/sms_tg_forwarder/daemon.log'
```

实时查看日志：

```sh
adb shell su -c 'tail -f /data/adb/sms_tg_forwarder/daemon.log'
```

查看进程：

```sh
adb shell su -c 'cat /data/adb/sms_tg_forwarder/daemon.pid && ps -A | grep sms-tg-forwarder'
```

停止进程：

```sh
adb shell su -c 'kill $(cat /data/adb/sms_tg_forwarder/daemon.pid)'
```

## 常见问题

### 日志提示缺少 TELEGRAM_BOT_TOKEN 或 TELEGRAM_CHAT_ID

编辑：

```text
/data/adb/modules/sms_tg_forwarder/config.env
```

确认这两项已经填写：

```sh
TELEGRAM_BOT_TOKEN=你的BotToken
TELEGRAM_CHAT_ID=你的ChatID
```

然后重启手机。

### Telegram 没收到消息

按顺序检查：

1. 手机是否已经 root，并且 Magisk 模块启用。
2. `config.env` 里 token 和 chat id 是否正确。
3. 手机是否能通过 `PROXY_URL` 访问 Telegram。
4. 是否真的收到了新短信，而不是安装前的旧短信。
5. 日志里是否有错误：

```sh
adb shell su -c 'tail -n 100 /data/adb/sms_tg_forwarder/daemon.log'
```

### 出现 `tls: failed to verify certificate`

先确认配置里有：

```sh
CA_CERT_DIR=/system/etc/security/cacerts
TLS_INSECURE_SKIP_VERIFY=false
```

如果你的代理会做 HTTPS 解密，需要导出代理的 CA 证书，然后配置：

```sh
CA_CERT_FILE=/path/to/proxy-ca.pem
```

临时调试可以这样设置：

```sh
TLS_INSECURE_SKIP_VERIFY=true
```

调试完成后建议改回 `false`。

### 为什么旧短信没有转发

这是正常行为。

首次运行时，程序会把：

```text
/data/adb/sms_tg_forwarder/last_id.txt
```

初始化为当前短信数据库的最大 `_id`，避免把旧短信一次性全部发出去。

如果确实要重新处理旧短信，需要先停止进程，然后把这个文件里的数字改小。比如改成 `0`，下次触发处理时可能会转发所有收件短信。删除这个文件不会重发旧短信，因为程序会重新把它初始化为当前最新的 `_id`。

## 自己编译

编译 arm64 二进制：

```sh
make arm64 GO=/usr/local/go/bin/go
```

输出：

```text
dist/sms-tg-forwarder-linux-arm64
```

打包 Magisk 模块：

```sh
make magisk GO=/usr/local/go/bin/go
```

输出：

```text
dist/sms_tg_forwarder-magisk-arm64.zip
```

## 目录结构

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

当前默认由 Magisk 的 `service.sh` 启动服务。`sms.rc` 会被保留并打进 zip，但默认启动流程不依赖它。
