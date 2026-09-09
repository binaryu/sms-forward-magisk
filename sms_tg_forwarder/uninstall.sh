#!/system/bin/sh

PID_FILE=/data/adb/sms_tg_forwarder/daemon.pid

# 停止后台守护进程
if [ -f "$PID_FILE" ]; then
  PID=$(cat "$PID_FILE" 2>/dev/null)
  if [ -n "$PID" ]; then
    kill "$PID" 2>/dev/null
    sleep 1
    kill -9 "$PID" 2>/dev/null
  fi
  rm -f "$PID_FILE" 2>/dev/null
fi

pkill -f sms-tg-forwarder 2>/dev/null

# 清理在 KernelSU 下创建的软链接
rm -f /data/adb/ksu/bin/sms-tg-forwarder 2>/dev/null

# 清理测试触发文件
rm -f /data/adb/sms_tg_forwarder/test /data/adb/sms_tg_forwarder/test_call 2>/dev/null
