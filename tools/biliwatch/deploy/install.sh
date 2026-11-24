#!/usr/bin/env bash
# biliwatch 安装脚本（幂等，可重复执行）
# 用法：sudo bash /cathome4/mau/tools/biliwatch/deploy/install.sh
set -euo pipefail

APP_DIR=/cathome4/mau/tools/biliwatch
UNIT_SRC="${APP_DIR}/deploy/biliwatch.service"
UNIT_DST=/etc/systemd/system/biliwatch.service
RUN_USER=cathome
MIN_NODE_MAJOR=22
MIN_NODE_MINOR=18

echo "[1/6] 检查运行用户 ${RUN_USER}"
if ! id "${RUN_USER}" >/dev/null 2>&1; then
    useradd --system --home-dir /cathome4 --shell /usr/sbin/nologin "${RUN_USER}"
    echo "      已创建系统用户 ${RUN_USER}"
else
    echo "      已存在，跳过"
fi

echo "[2/6] 检查 node 运行时与版本"
if [ ! -x /usr/bin/node ]; then
    echo "      ERR：/usr/bin/node 不存在（先 apt install nodejs）" >&2
    exit 1
fi
NODE_VER="$(/usr/bin/node --version)"
echo "      ${NODE_VER}"
# 类型擦除（直跑 .ts）需 Node >= 22.18——低版本会在启动前报 unknown file extension
MAJOR="$(echo "${NODE_VER#v}" | cut -d. -f1)"
MINOR="$(echo "${NODE_VER#v}" | cut -d. -f2)"
if [ "${MAJOR}" -lt "${MIN_NODE_MAJOR}" ] || { [ "${MAJOR}" -eq "${MIN_NODE_MAJOR}" ] && [ "${MINOR}" -lt "${MIN_NODE_MINOR}" ]; }; then
    echo "      ERR：需 Node >= ${MIN_NODE_MAJOR}.${MIN_NODE_MINOR}（类型擦除默认启用），当前 ${NODE_VER}" >&2
    exit 1
fi
echo "      版本满足（>= ${MIN_NODE_MAJOR}.${MIN_NODE_MINOR}）"

echo "[3/6] 检查应用目录与文件"
chgrp -R "${RUN_USER}" "${APP_DIR}"
chmod -R g+rX "${APP_DIR}"
for f in server.ts web/index.html deploy/biliwatch.service; do
    if [ ! -r "${APP_DIR}/${f}" ]; then
        echo "      ERR：缺 ${APP_DIR}/${f}" >&2
        exit 1
    fi
done
echo "      文件齐备"

echo "[4/6] 准备数据目录（唯一可写路径）"
install -d -o "${RUN_USER}" -g "${RUN_USER}" -m 0750 "${APP_DIR}/data"
echo "      ${APP_DIR}/data"

echo "[5/6] 安装 systemd unit 并启用"
install -m 0644 -o root -g root "${UNIT_SRC}" "${UNIT_DST}"
systemctl daemon-reload
systemctl enable biliwatch.service
systemctl restart biliwatch.service

echo "[6/6] 状态核验"
sleep 1
systemctl is-active biliwatch.service
ss -tlnp | grep -E ':7901' || echo "      WARN：7901 未监听，检查 journalctl -u biliwatch"
curl -s http://127.0.0.1:7901/api/ping || echo ""
echo "完成。配置页：http://<tailnet-ip>:7901/"
echo "提醒：本服务把消息发给 CH4 通道播报端点（默认 http://127.0.0.1:8080/api/v1/channels/publish），"
echo "      需在服务器上的 CH4 控制台建一条通道并取其 Key，填入 7901 页面的「通道 Key」。"
