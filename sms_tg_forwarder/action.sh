#!/system/bin/sh

MODDIR=${0%/*}
[ -z "$MODDIR" ] || [ "$MODDIR" = "." ] && MODDIR="/data/adb/modules/sms_tg_forwarder"
DATA_DIR=/data/adb/sms_tg_forwarder
CONFIG_FILE="$MODDIR/config.env"
[ ! -f "$CONFIG_FILE" ] && [ -f "$DATA_DIR/config.env" ] && CONFIG_FILE="$DATA_DIR/config.env"
PID_FILE="$DATA_DIR/daemon.pid"
LOG_FILE="$MODDIR/daemon.log"
BINARY="$MODDIR/system/bin/sms-tg-forwarder"

echo "=========================================="
echo "    SMS & Call Forwarder 状态与管理工具"
echo "=========================================="

# 1. 环境检测
if [ "$KSU" = "true" ]; then
  echo "[环境] KernelSU (版本: ${KSU_VER:-未知}, 内核版本: ${KSU_KERNEL_VER_CODE:-未知})"
elif [ -n "$APATCH" ]; then
  echo "[环境] APatch"
elif [ -n "$MAGISK_VER" ]; then
  echo "[环境] Magisk (版本: $MAGISK_VER)"
else
  echo "[环境] 通用 Root / Linux 运行环境"
fi

# 2. 守护进程运行状态
RUNNING=false
if [ -f "$PID_FILE" ]; then
  PID=$(cat "$PID_FILE" 2>/dev/null)
  if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
    RUNNING=true
    echo "[状态] 守护进程运行中 (PID: $PID)"
  fi
fi

if [ "$RUNNING" != "true" ]; then
  echo "[状态] 守护进程未在运行！"
  echo "[提示] 可执行以下命令启动: sh $MODDIR/service.sh"
fi

# 3. 配置摘要
echo "------------------------------------------"
echo "[配置] 文件路径: $CONFIG_FILE"
if [ -f "$CONFIG_FILE" ]; then
  eval "$(tr -d '\r' < "$CONFIG_FILE" | grep -v '^[[:space:]]*#' | grep -E '^(BARK_KEY|WECHAT_WEBHOOK|WX_WEBHOOK|APPRISE_URL|WEBHOOK_URL|TELEGRAM_BOT_TOKEN|TELEGRAM_CHAT_ID|ENABLE_CALL_FORWARD|PROXY_URL)=')"
  [ -n "$BARK_KEY" ] && echo "  - Bark 推送: 已配置 (Key: ${BARK_KEY:0:4}***)" || echo "  - Bark 推送: 未配置"
  [ -n "$WECHAT_WEBHOOK" ] || [ -n "$WX_WEBHOOK" ] && echo "  - 企业微信机器人: 已配置" || echo "  - 企业微信机器人: 未配置"
  [ -n "$APPRISE_URL" ] && echo "  - Apprise API: 已配置" || echo "  - Apprise API: 未配置"
  [ -n "$WEBHOOK_URL" ] && echo "  - 通用 Webhook: 已配置" || echo "  - 通用 Webhook: 未配置"
  if [ -n "$TELEGRAM_BOT_TOKEN" ] && [ -n "$TELEGRAM_CHAT_ID" ]; then
    echo "  - Telegram Bot: 已配置 (Chat ID: $TELEGRAM_CHAT_ID)"
  else
    echo "  - Telegram Bot: 未配置"
  fi
  [ "$ENABLE_CALL_FORWARD" != "false" ] && echo "  - 未接来电监听: 已启用" || echo "  - 未接来电监听: 已禁用"
  [ -n "$PROXY_URL" ] && echo "  - 代理服务: $PROXY_URL"
else
  echo "  ! 配置文件不存在"
fi

# 4. 游标状态 (对齐统一)
echo "------------------------------------------"
MSG_ID=""
[ -f "$DATA_DIR/last_msg.txt" ] && MSG_ID=$(cat "$DATA_DIR/last_msg.txt" 2>/dev/null)
[ -z "$MSG_ID" ] && [ -f "$DATA_DIR/last_id.txt" ] && MSG_ID=$(cat "$DATA_DIR/last_id.txt" 2>/dev/null)
echo "[游标] 短信最新已对齐 ID (last_msg): ${MSG_ID:-0}"

CALL_ID=""
[ -f "$DATA_DIR/last_call.txt" ] && CALL_ID=$(cat "$DATA_DIR/last_call.txt" 2>/dev/null)
[ -z "$CALL_ID" ] && [ -f "$DATA_DIR/last_call_id.txt" ] && CALL_ID=$(cat "$DATA_DIR/last_call_id.txt" 2>/dev/null)
echo "[游标] 通话最新已对齐 ID (last_call): ${CALL_ID:-0}"

# 5. 最新日志
echo "------------------------------------------"
echo "[最新日志 (末尾 15 行)]:"
if [ -f "$LOG_FILE" ]; then
  tail -n 15 "$LOG_FILE" 2>/dev/null
elif [ -f "$DATA_DIR/daemon.log" ]; then
  tail -n 15 "$DATA_DIR/daemon.log" 2>/dev/null
else
  echo "  (暂无日志)"
fi

echo "=========================================="
echo "[测试快捷命令]:"
echo "  • 测试短信转发: touch /data/adb/sms_tg_forwarder/test"
echo "  • 测试来电转发: touch /data/adb/sms_tg_forwarder/test_call"
echo "=========================================="
