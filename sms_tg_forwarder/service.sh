#!/system/bin/sh

# 获取模块目录
MODDIR=${0%/*}
[ -z "$MODDIR" ] || [ "$MODDIR" = "." ] && MODDIR="/data/adb/modules/sms_tg_forwarder"

DATA_DIR=/data/adb/sms_tg_forwarder
CONFIG_FILE="$MODDIR/config.env"
LOG_FILE="$MODDIR/daemon.log"
PID_FILE="$DATA_DIR/daemon.pid"
BINARY="$MODDIR/system/bin/sms-tg-forwarder"

mkdir -p "$DATA_DIR"
mkdir -p "$MODDIR"

# 建立双向日志软链接
touch "$LOG_FILE"
ln -sf "$LOG_FILE" "$DATA_DIR/daemon.log" 2>/dev/null

echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Service starting..." >> "$LOG_FILE"

# 等待开机完成
while [ "$(getprop sys.boot_completed 2>/dev/null)" != "1" ]; do
  sleep 2
done

# 等待用户解锁解密存储 (/data/data)
while [ "$(getprop sys.user.0.ce_available 2>/dev/null)" != "true" ]; do
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

echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Loaded config: APPRISE_URL=$APPRISE_URL, WEBHOOK_URL=$WEBHOOK_URL, TG_BOT=${TELEGRAM_BOT_TOKEN:0:5}***" >> "$LOG_FILE"

if [ -z "$APPRISE_URL" ] && [ -z "$WEBHOOK_URL" ] && { [ -z "$TELEGRAM_BOT_TOKEN" ] || [ -z "$TELEGRAM_CHAT_ID" ]; }; then
  echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] ERROR: Missing APPRISE_URL, WEBHOOK_URL or (TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID) in $CONFIG_FILE" >> "$LOG_FILE"
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
export APPRISE_URL
export APPRISE_URLS
export APPRISE_TITLE
export APPRISE_TYPE
export APPRISE_FORMAT
export APPRISE_USE_PROXY

export WEBHOOK_URL
export WEBHOOK_METHOD
export WEBHOOK_HEADERS
export WEBHOOK_BODY
export WEBHOOK_USE_PROXY

export TELEGRAM_BOT_TOKEN
export TELEGRAM_CHAT_ID
export PROXY_URL
export CA_CERT_DIR
export CA_CERT_FILE
export TLS_INSECURE_SKIP_VERIFY

if [ -n "$DB_PATH" ]; then
  export DB_PATH
fi

if [ -n "$LAST_ID_PATH" ]; then
  export LAST_ID_PATH
fi

echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Launching daemon: $BINARY" >> "$LOG_FILE"
"$BINARY" >> "$LOG_FILE" 2>&1 &
NEW_PID="$!"
echo "$NEW_PID" > "$PID_FILE"
echo "$(date '+%Y-%m-%d %H:%M:%S') [service.sh] Daemon started with PID: $NEW_PID" >> "$LOG_FILE"
