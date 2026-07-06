#!/system/bin/sh

PID_FILE=/data/adb/sms_tg_forwarder/daemon.pid

if [ -f "$PID_FILE" ]; then
  PID=$(cat "$PID_FILE" 2>/dev/null)
  if [ -n "$PID" ]; then
    kill "$PID" 2>/dev/null
  fi
fi
