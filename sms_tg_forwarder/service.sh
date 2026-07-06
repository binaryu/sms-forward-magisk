#!/system/bin/sh

MODDIR=${0%/*}
DATA_DIR=/data/adb/sms_tg_forwarder
CONFIG_FILE="$MODDIR/config.env"
LOG_FILE="$DATA_DIR/daemon.log"
PID_FILE="$DATA_DIR/daemon.pid"
BINARY="$MODDIR/system/bin/sms-tg-forwarder"

mkdir -p "$DATA_DIR"

if [ ! -f "$CONFIG_FILE" ]; then
  if [ -f "$MODDIR/config.env.example" ]; then
    cp "$MODDIR/config.env.example" "$CONFIG_FILE"
  else
    echo "TELEGRAM_BOT_TOKEN=" > "$CONFIG_FILE"
    echo "TELEGRAM_CHAT_ID=" >> "$CONFIG_FILE"
    echo "DB_PATH=" >> "$CONFIG_FILE"
    echo "LAST_ID_PATH=" >> "$CONFIG_FILE"
    echo "PROXY_URL=http://127.0.0.1:7890" >> "$CONFIG_FILE"
    echo "CA_CERT_DIR=/system/etc/security/cacerts" >> "$CONFIG_FILE"
    echo "CA_CERT_FILE=" >> "$CONFIG_FILE"
    echo "TLS_INSECURE_SKIP_VERIFY=false" >> "$CONFIG_FILE"
  fi
fi

chmod 700 "$DATA_DIR"
chmod 600 "$CONFIG_FILE"
chmod 755 "$BINARY"

# shellcheck disable=SC1090
. "$CONFIG_FILE"

if [ -z "$TELEGRAM_BOT_TOKEN" ] || [ -z "$TELEGRAM_CHAT_ID" ]; then
  echo "$(date '+%Y-%m-%d %H:%M:%S') missing TELEGRAM_BOT_TOKEN or TELEGRAM_CHAT_ID in $CONFIG_FILE" >> "$LOG_FILE"
  exit 0
fi

if [ -f "$PID_FILE" ]; then
  OLD_PID=$(cat "$PID_FILE" 2>/dev/null)
  if [ -n "$OLD_PID" ] && kill -0 "$OLD_PID" 2>/dev/null; then
    exit 0
  fi
fi

cd "$DATA_DIR" || exit 1

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

"$BINARY" >> "$LOG_FILE" 2>&1 &
echo "$!" > "$PID_FILE"
