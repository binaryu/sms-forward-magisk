SKIPUNZIP=0

ui_print "- 正在安装 SMS Forwarder 模块..."

# 停止可能正在运行的旧进程（防止热更新时旧进程常驻后台继续自循环）
if [ -f /data/adb/sms_tg_forwarder/daemon.pid ]; then
  OLD_PID=$(cat /data/adb/sms_tg_forwarder/daemon.pid 2>/dev/null)
  if [ -n "$OLD_PID" ]; then
    kill -9 "$OLD_PID" 2>/dev/null
  fi
  rm -f /data/adb/sms_tg_forwarder/daemon.pid 2>/dev/null
fi
pkill -9 -f sms-tg-forwarder 2>/dev/null

# 架构检查：仅支持 64 位 ARM (arm64)
if [ "$ARCH" != "arm64" ]; then
  abort "! 不支持的 CPU 架构: $ARCH，本模块目前仅编译了 arm64 二进制文件！"
fi

# 环境识别 (KernelSU vs APatch vs Magisk)
# 注意：KernelSU 中 MAGISK_VER_CODE 永远被伪装为 25200，必须优先检查 KSU 环境变量！
if [ "$KSU" = "true" ]; then
  ui_print "- 检测到运行环境: KernelSU"
  ui_print "  • KernelSU 版本: ${KSU_VER:-未知} (内核代号: ${KSU_KERNEL_VER_CODE:-未知})"
  ui_print "  • ksud UAPI 版本: ${KSU_UAPI_VER:-未知}"

  # KernelSU 特性处理：即使未安装 metamodule (如 meta-overlayfs)，也为 /data/adb/ksu/bin 建立软链接以提供全局命令
  if [ -d "/data/adb/ksu/bin" ]; then
    ln -sf "$MODPATH/system/bin/sms-tg-forwarder" "/data/adb/ksu/bin/sms-tg-forwarder" 2>/dev/null
  fi

  if [ ! -d "/data/adb/metamodule" ] && [ ! -d "/data/adb/modules/meta-overlayfs" ]; then
    ui_print "- [提示] 未检测到 metamodule (挂载模块)"
    ui_print "  SMS Forwarder 后台服务由 service.sh 独立运行，无需 metamodule 即可正常转发！"
    ui_print "  已在 /data/adb/ksu/bin 创建软链接，root 终端直接输入 sms-tg-forwarder 可用。"
  else
    ui_print "- [提示] 检测到 metamodule，/system/bin/sms-tg-forwarder 挂载有效"
  fi
elif [ -n "$APATCH" ]; then
  ui_print "- 检测到运行环境: APatch"
elif [ -n "$MAGISK_VER" ]; then
  ui_print "- 检测到运行环境: Magisk ($MAGISK_VER)"
else
  ui_print "- 检测到运行环境: 通用 Root 环境"
fi

# 设置可执行文件及脚本权限
set_perm "$MODPATH/system/bin/sms-tg-forwarder" 0 0 0755
set_perm "$MODPATH/service.sh" 0 0 0755
set_perm "$MODPATH/uninstall.sh" 0 0 0755
[ -f "$MODPATH/action.sh" ] && set_perm "$MODPATH/action.sh" 0 0 0755

# 每次重新刷入时重置游标，强制对齐最新短信与未接来电，绝不回放旧消息
rm -f /data/adb/sms_tg_forwarder/last_msg.txt /data/adb/sms_tg_forwarder/last_call.txt 2>/dev/null
rm -f /data/adb/sms_tg_forwarder/last_id.txt /data/adb/sms_tg_forwarder/last_call_id.txt 2>/dev/null
rm -f /data/adb/sms_tg_forwarder/test /data/adb/sms_tg_forwarder/test_call 2>/dev/null

# 如果已有老配置则保留，否则从模板创建
if [ -f "/data/adb/modules/sms_tg_forwarder/config.env" ]; then
  ui_print "- 保留已有的配置文件 config.env"
  cp -af "/data/adb/modules/sms_tg_forwarder/config.env" "$MODPATH/config.env"
  # 如果老配置里写死了失效的 user_de 路径，将其清空以便触发智能自动适配
  sed -i 's|^DB_PATH=/data/user_de.*|DB_PATH=|g' "$MODPATH/config.env" 2>/dev/null
elif [ ! -f "$MODPATH/config.env" ]; then
  ui_print "- 初始化配置文件 config.env"
  cp -af "$MODPATH/config.env.example" "$MODPATH/config.env"
fi
set_perm "$MODPATH/config.env" 0 0 0600

ui_print "- 安装完成！"
ui_print "- 已自动对齐最新短信与未接来电起点，绝不重发历史旧记录！"
