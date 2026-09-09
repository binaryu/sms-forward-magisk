#!/system/bin/sh

# 获取模块目录
MODDIR=${0%/*}
[ -z "$MODDIR" ] || [ "$MODDIR" = "." ] && MODDIR="/data/adb/modules/sms_tg_forwarder"

DATA_DIR=/data/adb/sms_tg_forwarder
CONFIG_FILE="$MODDIR/config.env"
[ ! -f "$CONFIG_FILE" ] && [ -f "$DATA_DIR/config.env" ] && CONFIG_FILE="$DATA_DIR/config.env"
LOG_FILE="$MODDIR/daemon.log"
PID_FILE="$DATA_DIR/daemon.pid"
BINARY="$MODDIR/system/bin/sms-tg-forwarder"

mkdir -p "$DATA_DIR"
mkdir -p "$MODDIR"

# 建立双向日志软链接
touch "$LOG_FILE"
ln -sf "$LOG_FILE" "$DATA_DIR/daemon.log" 2>/dev/null

echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Service starting..." >> "$LOG_FILE"

# 环境检测与软链接保障
if [ "$KSU" = "true" ]; then
  echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Environment: KernelSU (KSU_VER: ${KSU_VER:-unknown}, KSU_KERNEL_VER_CODE: ${KSU_KERNEL_VER_CODE:-unknown})" >> "$LOG_FILE"
  if [ -d "/data/adb/ksu/bin" ]; then
    ln -sf "$BINARY" "/data/adb/ksu/bin/sms-tg-forwarder" 2>/dev/null
  fi
elif [ -n "$APATCH" ]; then
  echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Environment: APatch" >> "$LOG_FILE"
elif [ -n "$MAGISK_VER" ]; then
  echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Environment: Magisk (MAGISK_VER: $MAGISK_VER)" >> "$LOG_FILE"
else
  echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Environment: Generic Root" >> "$LOG_FILE"
fi

# 扩展 PATH 环境变量以包含 KernelSU 与 Magisk 工具
export PATH="/data/adb/ksu/bin:/data/adb/ap/bin:/data/adb/magisk:/system/bin:/system/xbin:$PATH"

# 等待开机完成
while [ "$(getprop sys.boot_completed 2>/dev/null)" != "1" ]; do
  sleep 2
done

# 等待用户解锁解密存储 (/data/data)，并增加短信与通话数据库目录探测兜底
while [ "$(getprop sys.user.0.ce_available 2>/dev/null)" != "true" ]; do
  [ -d "/data/data/com.android.providers.telephony/databases" ] && break
  [ -d "/data/user/0/com.android.providers.telephony/databases" ] && break
  [ -d "/data/data/com.android.providers.contacts/databases" ] && break
  sleep 2
done

if [ ! -f "$CONFIG_FILE" ]; then
  if [ -f "$MODDIR/config.env.example" ]; then
    cp "$MODDIR/config.env.example" "$CONFIG_FILE"
    echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Generated config.env from template" >> "$LOG_FILE"
  fi
fi

chmod 755 "$BINARY" 2>/dev/null
chmod 600 "$CONFIG_FILE" 2>/dev/null

# 读取配置（兼容 DOS 格式 \r）
if [ -f "$CONFIG_FILE" ]; then
  eval "$(tr -d '\r' < "$CONFIG_FILE" | grep -v '^[[:space:]]*#' | grep '=')"
fi

echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Loaded config: BARK=${BARK_KEY:0:4}***, WECHAT=${WECHAT_WEBHOOK:0:30}***, APPRISE=$APPRISE_URL, WEBHOOK=$WEBHOOK_URL, TG_BOT=${TELEGRAM_BOT_TOKEN:0:5}***" >> "$LOG_FILE"

# 检查是否至少配置了一个通道
if [ -z "$BARK_KEY" ] && [ -z "$WECHAT_WEBHOOK" ] && [ -z "$WX_WEBHOOK" ] && [ -z "$APPRISE_URL" ] && [ -z "$WEBHOOK_URL" ] && { [ -z "$TELEGRAM_BOT_TOKEN" ] || [ -z "$TELEGRAM_CHAT_ID" ]; }; then
  echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] ERROR: No notification channel configured in $CONFIG_FILE" >> "$LOG_FILE"
  exit 0
fi

if [ -f "$PID_FILE" ]; then
  OLD_PID=$(cat "$PID_FILE" 2>/dev/null)
  if [ -n "$OLD_PID" ] && kill -0 "$OLD_PID" 2>/dev/null; then
    echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Daemon is already running (PID: $OLD_PID)" >> "$LOG_FILE"
    exit 0
  fi
fi

cd "$DATA_DIR" || cd "$MODDIR" || exit 1

TZ_PROP=$(getprop persist.sys.timezone 2>/dev/null)
[ -z "$TZ_PROP" ] && TZ_PROP="Asia/Shanghai"
export TZ="$TZ_PROP"

export CONFIG_FILE="$CONFIG_FILE"

# Bark
export BARK_KEY
export BARK_SERVER
export BARK_GROUP
export BARK_SOUND
export BARK_ICON
export BARK_USE_PROXY

# 企业微信
export WECHAT_WEBHOOK
export WECHAT_USE_PROXY

# Apprise
export APPRISE_URL
export APPRISE_URLS
export APPRISE_TITLE
export APPRISE_TYPE
export APPRISE_FORMAT
export APPRISE_USE_PROXY

# 通用 Webhook
export WEBHOOK_URL
export WEBHOOK_METHOD
export WEBHOOK_HEADERS
export WEBHOOK_BODY
export WEBHOOK_USE_PROXY

# Telegram
export TELEGRAM_BOT_TOKEN
export TELEGRAM_CHAT_ID

# 通话记录 (未接来电)
export ENABLE_CALL_FORWARD
export CALL_DB_PATH
export CALL_FORWARD_TYPES
export LAST_CALL_PATH
export LAST_CALL_ID_PATH

# 网络与底层
export PROXY_URL
export DNS_SERVER
export CA_CERT_DIR
export CA_CERT_FILE
export TLS_INSECURE_SKIP_VERIFY
export DB_PATH
export LAST_MSG_PATH
export LAST_ID_PATH

echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Launching daemon: $BINARY" >> "$LOG_FILE"
"$BINARY" >> "$LOG_FILE" 2>&1 &
NEW_PID="$!"
echo "$NEW_PID" > "$PID_FILE"
echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Daemon started with PID: $NEW_PID" >> "$LOG_FILE"
