#!/usr/bin/env bash
# linux-see-window 安装脚本（幂等，可重复执行）
# 用法：sudo bash /cathome4/mau/tools/linux-see-window/deploy/install.sh
set -euo pipefail

APP_DIR=/cathome4/mau/tools/linux-see-window
UNIT_SRC="${APP_DIR}/deploy/linux-see-window.service"
UNIT_DST=/etc/systemd/system/linux-see-window.service
RUN_USER=cathome

echo "[1/6] 检查运行用户 ${RUN_USER}"
if ! id "${RUN_USER}" >/dev/null 2>&1; then
    useradd --system --home-dir /cathome4 --shell /usr/sbin/nologin "${RUN_USER}"
    echo "      已创建系统用户 ${RUN_USER}"
else
    echo "      已存在，跳过"
fi

echo "[2/6] 检查 node 运行时"
if [ ! -x /usr/bin/node ]; then
    echo "      ERR：/usr/bin/node 不存在（先 apt install nodejs）" >&2
    exit 1
fi
echo "      $(/usr/bin/node --version)"

echo "[3/6] 检查应用目录与文件"
# 运行用户须能读到整棵目录——scp / 手工拷贝常留下 700 或别的组，先归一
chgrp -R "${RUN_USER}" "${APP_DIR}"
chmod -R g+rX "${APP_DIR}"
for f in server.js web/index.html deploy/linux-see-window.service; do
    if [ ! -r "${APP_DIR}/${f}" ]; then
        echo "      ERR：缺 ${APP_DIR}/${f}" >&2
        exit 1
    fi
done
echo "      文件齐备"

echo "[4/6] 安装 systemd unit"
install -m 0644 -o root -g root "${UNIT_SRC}" "${UNIT_DST}"

echo "[5/6] 重载并启用服务"
systemctl daemon-reload
systemctl enable linux-see-window.service
systemctl restart linux-see-window.service

echo "[6/6] 状态核验"
sleep 1
systemctl is-active linux-see-window.service
ss -tlnp | grep -E ':7900' || echo "      WARN：7900 未监听，检查 journalctl -u linux-see-window"

echo "完成。访问：http://<tailnet-ip>:7900/"
