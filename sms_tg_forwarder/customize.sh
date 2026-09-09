SKIPUNZIP=0

ui_print "- 正在安装 SMS Forwarder 模块..."

# 设置可执行文件及脚本权限
set_perm "$MODPATH/system/bin/sms-tg-forwarder" 0 0 0755
set_perm "$MODPATH/service.sh" 0 0 0755
set_perm "$MODPATH/uninstall.sh" 0 0 0755

# 每次重新刷入时重置游标，强制对齐最新短信，绝不回放旧消息
rm -f /data/adb/sms_tg_forwarder/last_id.txt 2>/dev/null

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
ui_print "- 已自动对齐最新短信起点，绝不重发历史旧短信！"
