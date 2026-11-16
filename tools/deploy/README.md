# tools/deploy —— Linux 远程部署（本地交叉构建 + SSH 推送 + incr 落位）

> 定位：解决「Linux 上跑一次 `SetUp prepare` 要 8 分 25 秒」的问题。产物改在**本地构建**，Linux 侧只做搬运与切换（约 10 秒）。

---

## 一、为什么改

`SetUp prepare` 在 2 核 4 GB 上全链 8 分 25 秒（Windows 约 35 秒），大头是组翻译 18 组串行（每组起一次 dotnet 编译）+ 双 sln 测试。而且 `prepare` 会自增仓库版本、需要在远端跑全链测试——远端做这些既慢又没必要。

**判据：构建用开发机的算力，运行机只接收产物。** .NET 框架依赖产物里除 apphost 外全是平台无关的 dll，Windows 上 `dotnet publish -r linux-x64 --self-contained false` 即可产出 Linux 可执行（已实测）。

## 一点五、两条链的定位（别混用）

| 链 | 入口 | 落点 | 何时用 |
|:--|:--|:--|:--|
| **日常更新** | `tools/deploy/push-app.ps1`（A368）→ `/srv/inbox` → `take-app.sh` | 包落**外部投放区**，root 校验指纹后覆盖**产物区** `<远端仓库>/public/app`，再由 `restart-incr` 原子切到运行区 | 改了宿主程序集 / 语料 / 前端之后的标准路径 |
| **首次部署 / 纯运维** | 本文 §二 手链 + `apply.sh` | 直接停服切**运行区** | 目标机还没有产物区（首次）、或远端没有可用猫会话派 `restart-incr` 时 |

🔴 **判据：能用 push-app 就别手拼链。** 手链直接切运行区会把**产物区留在旧版**——之后谁跑一次 `restart-incr`，运行区就被旧产物覆盖（静默倒退）。走 push-app 时两区同源，incr 才是「只搬运、不引入代际差」。

## 二、链路

```
[本地 Windows]
  1. git pull（保证源码最新）
  2. dotnet publish CatHome4/CatHome4.csproj -c Debug -r linux-x64 --self-contained false -o <out>
  3. tar -czf ch4-linux.tar.gz -C <out> .
  4. scp ch4-linux.tar.gz cathome:/srv/inbox/

[Linux（cathome-linux）]
  5. ssh cathome "sudo bash /cathome4/mau/tools/deploy/apply.sh /tmp/ch4-linux.tar.gz"
     └─ 解包 → Flows 继承 → 权限归一 → 完整性闸 → 停服 → 原子切换 → 启服 → 探活（15 s）
        └─ 探活失败：整目录回滚到上一版本
```

## 二点五、`take-app.sh` —— 投放区取包（2026-11-16 增）

**为什么有这一步**：ubuntu 被关进 `/srv/inbox`（700 · 唯一可写落点），它穿不进 `/cathome4`，也就改不到仓库 HEAD。取包由 **root** 执行，把「权限面 + 校验面」串起来。

```
用法：bash /cathome4/mau/tools/deploy/take-app.sh <包名|包路径> [--dry-run]
      包名 = /srv/inbox 下的相对名；也可给绝对路径
```

**六段**：解包/取目录 → 结构闸（`buildinfo.txt` + `CatHome4`）→ 指纹闸（`commit` 40 位 · `dirty=false` · **`commit == git rev-parse HEAD`**）→ Flows 继承（包内未带则沿用现有）→ 权限归一 + 落位（旧 `app` 留存 `app.prev`）→ 源包归档到 `/srv/inbox/_done/<时间戳>-<名>`。

**为什么指纹闸是硬闸**：攻击者若能伪造包，也只能往 inbox 丢。要过闸必须让 `commit == 仓库 HEAD`，而 HEAD 只有 root 能改——**权限隔离 + 指纹校验两道并联，缺一不可**。

**边界**：不重启、不碰运行区。取包完成后由猫会话发起 `restart-incr`。

---

## 三、两种更新档

| 档 | 什么时候 | 命令 |
|:--|:--|:--|
| **A · 代码/前端**（常规） | 改了宿主程序集 / html | 上面 2→5，**不带 Flows**（沿用现有） |
| **B · 语料** | 改了 `corpus/ch4/**` 或积木 | 本地跑组翻译产出 `Flows/` 一并打进包（或仍在 Linux 上跑 `mau proj --build`，18 组约 3 分钟） |

## 四、约束与前提

| 项 | 说明 |
|:--|:--|
| 绑定 | `CH4_HTTP_BIND=0.0.0.0`（unit 内已设）；探活走 `127.0.0.1:8080/api/v1/auth/status` |
| 运行用户 | `cathome`；包内权限由脚本统一 `chown -R cathome:cathome` + `chmod -R g+rX` |
| 首次部署 | 仍需在 Linux 上跑一次 `SetUp prepare`（生成 `Mau-public/` 与 `Flows/`），此后走本链路 |
| 版本 | 本地 publish 用仓库 `Directory.Build.props` 的版本，Linux 侧不再自增 |
| 回滚 | `mauout.old` 仅探活成功前保留；成功后删除 |
| 数据 | 与产物完全分离（`CH4_DATA_ROOT=/cathome4/data`），切换不动数据 |

## 五、时间对照（实测基线）

| 方式 | 耗时 |
|:--|:--|
| 远端 `SetUp prepare` 全链 | 8 分 25 秒 |
| 本方案（代码档 A） | 待测（预计：publish 30–60 s + 传输 + 切换 10 s） |
