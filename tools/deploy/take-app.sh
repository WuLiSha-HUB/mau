#!/usr/bin/env bash
# 外部投放区取包 → 产物区落位（root 执行；不重启、不碰运行区）
#
# 用法：bash take-app.sh <包名|包路径> [--dry-run]
#   包 = /srv/inbox/<名>.tar.gz  或  /srv/inbox/<名>/（已解包目录）
#
# 职责边界：
#   · 只做「校验 + 搬运」：/srv/inbox → /cathome4/mau/public/app
#   · 不重启、不碰运行区——切到运行区仍走 restart-incr（由宿主会话发起）
#   · 校验不过一律拒绝，不改任何现有文件
#
# 为什么校验这么严：
#   ubuntu 只被允许写 /srv/inbox，它改不到本仓库的 HEAD。
#   「包 commit 必须等于仓库 HEAD」这条闸一旦成立，攻击者无法伪造能通过校验的包——
#   要伪造就得先改源码 HEAD，而那需要仓库写权限，已经不在 ubuntu 手里。
set -euo pipefail

INBOX=/srv/inbox
PUB=/cathome4/mau/public
REPO=/cathome4/mau
APP="$PUB/app"
PREV="$PUB/app.prev"
NEW="$PUB/app.new"

DRY=0
ARG=""
for a in "$@"; do
    case "$a" in
        --dry-run) DRY=1 ;;
        *) ARG="$a" ;;
    esac
done

if [ -z "$ARG" ]; then
    echo "用法：bash take-app.sh <包名|包路径> [--dry-run]" >&2
    echo "示例：bash take-app.sh 20261116-app.tar.gz" >&2
    exit 2
fi

# [1] 定位包
SRC="$ARG"
if [ ! -e "$SRC" ]; then
    SRC="$INBOX/$ARG"
fi
if [ ! -e "$SRC" ]; then
    echo "ERR|包不存在：$ARG（已试绝对路径与 $INBOX/）" >&2
    exit 1
fi

rm -rf "$NEW"
mkdir -p "$NEW"
if [ -d "$SRC" ]; then
    cp -a "$SRC/." "$NEW/"
    echo "[1/6] 源为已解包目录：$SRC"
else
    tar -xzf "$SRC" -C "$NEW"
    echo "[1/6] 解包：$SRC"
fi

# [2] 结构闸
if [ ! -f "$NEW/buildinfo.txt" ]; then
    echo "ERR|包内缺 buildinfo.txt——不是 push-app 产物" >&2
    rm -rf "$NEW"
    exit 1
fi
if [ ! -f "$NEW/CatHome4" ]; then
    echo "ERR|包内缺可执行 CatHome4——不是 linux-x64 publish 输出" >&2
    rm -rf "$NEW"
    exit 1
fi
echo "[2/6] 结构闸通过"

# [3] 指纹闸
PKG_COMMIT=$(sed -n 's/^commit=//p' "$NEW/buildinfo.txt" | head -n1 | tr -d '\r')
PKG_DIRTY=$(sed -n 's/^dirty=//p' "$NEW/buildinfo.txt" | head -n1 | tr -d '\r')
PKG_VER=$(sed -n 's/^version=//p' "$NEW/buildinfo.txt" | head -n1 | tr -d '\r')

if [ "${#PKG_COMMIT}" -ne 40 ]; then
    echo "ERR|buildinfo.commit 非 40 位：'$PKG_COMMIT'" >&2
    rm -rf "$NEW"
    exit 1
fi
if [ "$PKG_DIRTY" != "false" ]; then
    echo "ERR|buildinfo.dirty='$PKG_DIRTY'——产物基于未提交源码，无法核验" >&2
    rm -rf "$NEW"
    exit 1
fi
HEAD=$(git -C "$REPO" rev-parse HEAD)
if [ "$PKG_COMMIT" != "$HEAD" ]; then
    echo "ERR|指纹不符——包与仓库 HEAD 不同源" >&2
    echo "     包 commit：$PKG_COMMIT" >&2
    echo "     仓 HEAD ：$HEAD" >&2
    echo "     处置：在仓库 git pull 到与产物同源的提交后再取包，或回开发机按最新源码重推" >&2
    rm -rf "$NEW"
    exit 1
fi
echo "[3/6] 指纹通过：commit=${PKG_COMMIT:0:8} dirty=false version=$PKG_VER"

# [4] Flows 继承（包内未带则沿用现有）
if [ ! -d "$NEW/Flows" ] && [ -d "$APP/Flows" ]; then
    cp -a "$APP/Flows" "$NEW/Flows"
    echo "[4/6] 沿用现有 Flows：$(ls "$NEW/Flows" | wc -l) 个文件"
else
    echo "[4/6] Flows 随包带上"
fi

# [5] 权限归一 + 落位
chown -R root:root "$NEW"
chmod -R u+rwX,go+rX "$NEW"
if [ -f "$NEW/CatHome4" ]; then
    chmod 0755 "$NEW/CatHome4"
fi

if [ "$DRY" = "1" ]; then
    echo "[5/6] --dry-run：校验全过，未落位（暂存已清）"
    rm -rf "$NEW"
    exit 0
fi

rm -rf "$PREV"
if [ -d "$APP" ]; then
    mv "$APP" "$PREV"
fi
mv "$NEW" "$APP"
echo "[5/6] 已落位：$APP（上一代留存 $PREV）"

# [6] 归档源包（_done 只保留最新一份，更早的当场清掉）
DONE="$INBOX/_done"
mkdir -p "$DONE"
STAMP=$(date +%Y%m%d_%H%M%S)
BASE=$(basename "$SRC")
mv "$SRC" "$DONE/${STAMP}-${BASE}"
echo "[6/6] 源包已归档：$DONE/${STAMP}-${BASE}"

KEEP=1
if [ "$(ls -1 "$DONE" | wc -l)" -gt "$KEEP" ]; then
    ls -1t "$DONE" | tail -n +2 | while IFS= read -r old; do
        rm -rf -- "$DONE/$old"
        echo "      已清理旧归档：$old"
    done
fi

echo "OK|产物区已更新 → 下一步 restart-incr"
