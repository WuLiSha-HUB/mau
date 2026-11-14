#!/usr/bin/env bash
# linux 侧部署应用（incr 语义：搬运 + 原子切换 + 探活 + 回滚）——由本地经 SSH 推送产物后调用
#
# 用法：sudo bash apply.sh [包路径]
#   包 = 本地 `dotnet publish -r linux-x64` 产物打包的 tar.gz（内容为 publish 目录下全部文件）
#
# 设计要点：
#   · 不在 linux 内编译——产物由本地交叉 publish 产出，linux 只做落位与切换（耗时 ~10 s）
#   · Flows 保留——包内未带 Flows 时沿用现有目录（语料未变更的常规更新）；带 Flows 时以包内为准
#   · 先切换后探活，探活失败整目录回滚——运行区永不留半成品
set -euo pipefail

TAR="${1:-/tmp/ch4-linux.tar.gz}"
NEW=/cathome4/mauout.new
CUR=/cathome4/mauout
OLD=/cathome4/mauout.old
UNIT=cathome4
PROBE="http://127.0.0.1:8080/api/v1/auth/status"

echo "[1/7] 清理暂存区"
rm -rf "$NEW"
mkdir -p "$NEW"

echo "[2/7] 解包 $TAR"
if [ ! -f "$TAR" ]; then
    echo "ERR|包不存在：$TAR" >&2
    exit 1
fi
tar -xzf "$TAR" -C "$NEW"

echo "[3/7] Flows 继承（包内未带则沿用现有）"
if [ ! -d "$NEW/Flows" ] && [ -d "$CUR/Flows" ]; then
    cp -a "$CUR/Flows" "$NEW/Flows"
    echo "      沿用现有 Flows：$(ls "$NEW/Flows" | wc -l) 个文件"
fi

echo "[4/7] 权限归一（root 运行——CatHome4 自身行为按最高权限）"
chown -R root:root "$NEW"
chmod -R u+rwX,go+rX "$NEW"
# Windows 侧 tar 不保留 Unix 权限位——apphost 执行位在此显式补回
if [ -f "$NEW/CatHome4" ]; then
    chmod 0755 "$NEW/CatHome4"
fi

echo "[5/7] 完整性闸"
if [ ! -x "$NEW/CatHome4" ]; then
    echo "ERR|产物缺可执行 CatHome4——包内容不是 linux-x64 publish 输出" >&2
    exit 1
fi
if [ ! -d "$NEW/Flows" ]; then
    echo "ERR|产物缺 Flows 目录——语料未随包且现有目录也不存在" >&2
    exit 1
fi
echo "      闸门通过：$(du -sh "$NEW" | cut -f1)"

echo "[6/7] 原子切换"
systemctl stop "$UNIT"
rm -rf "$OLD"
mv "$CUR" "$OLD"
mv "$NEW" "$CUR"
systemctl start "$UNIT"

echo "[7/7] 探活"
OK=0
for i in $(seq 1 15); do
    sleep 1
    if curl -sf -m 2 "$PROBE" >/dev/null 2>&1; then
        OK=1
        break
    fi
done
if [ "$OK" = "1" ]; then
    rm -rf "$OLD"
    echo "OK|部署完成（探活 ${i}s）"
    exit 0
fi

echo "ERR|探活失败，回滚" >&2
systemctl stop "$UNIT"
rm -rf "$CUR"
mv "$OLD" "$CUR"
systemctl start "$UNIT"
echo "     已回滚到上一版本；日志：journalctl -u $UNIT -n 50 --no-pager" >&2
exit 1
