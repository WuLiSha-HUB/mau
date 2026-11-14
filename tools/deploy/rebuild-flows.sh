#!/usr/bin/env bash
# 重编全部工具组 Flow（语料未变时一般不需要；用于「宿主版本与 Flows 版本错配」的恢复）
#
# 背景：Flow dll 在编译期把依赖程序集版本写进元数据——宿主升级后 Flows 必须同版本重编，
#       否则加载报 "Could not load file or assembly 'Mau.Runtime, Version=x.y.z'"（全部组失效）。
#
# 用法：sudo bash rebuild-flows.sh [目标目录]
#   默认目标 = /cathome4/mauout/Flows（就地替换运行区，完成后需重启宿主）
set -euo pipefail

REPO=/cathome4/mau
MAU=$REPO/Mau-public/Mau
TARGET="${1:-/cathome4/mauout/Flows}"

if [ ! -x "$MAU" ]; then
    echo "ERR|缺 Mau CLI：$MAU（先跑一次远端 SetUp prepare 生成 Mau-public）" >&2
    exit 1
fi

cd "$REPO"
ok=0
fail=0
for dir in corpus/ch4/*/; do
    name=$(basename "$dir")
    proj=$(find "$dir" -maxdepth 1 -name '*.mauproj' | head -n 1)
    if [ -z "$proj" ]; then
        echo "[跳过] $name （无 .mauproj）"
        continue
    fi
    echo "[$((ok + fail + 1))] $name"
    if "$MAU" proj "$proj" -o "public/src/$name" --build >/tmp/flow-build-$name.log 2>&1; then
        ok=$((ok + 1))
    else
        fail=$((fail + 1))
        echo "      失败：见 /tmp/flow-build-$name.log"
    fi
done

echo "编译完成：成功 $ok 组，失败 $fail 组"
if [ "$fail" -gt 0 ]; then
    exit 1
fi

echo "落位到 $TARGET"
mkdir -p "$TARGET"
cp -a "$REPO/public/app/Flows/." "$TARGET/"
chown -R root:root "$TARGET"
chmod -R u+rwX,go+rX "$TARGET"
echo "OK|Flows 已重建为当前宿主版本——请重启宿主：systemctl restart cathome4"
