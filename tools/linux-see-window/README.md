# linux-see-window —— Linux 运行健康度窗口

> 定位：CatHome4 在 Linux 服务器上的**运行健康度观测窗口**——实时看 CPU / 负载 / 内存 / 磁盘 / 网络 / IO / 进程。
> 定位二：**Linux 部署链的最小载体**——用一条不依赖 .NET 编译的链路，把「仓库 → 常驻服务 → systemd → 端口可达」先走通，为 CH4 的 Linux 常驻（A360）铺路。

---

## 一、形态

| 项 | 值 |
|:--|:--|
| 运行时 | Node.js（Ubuntu 26.04 apt 源，22.x）· **零 npm 依赖** |
| 端口 | **7900**——外围工具走下行段（CH4 主端口向上寻路，两者不撞） |
| 监听 | `0.0.0.0:7900`，服务内只放行 tailnet `100.64.0.0/10` 与 localhost |
| 数据面 | 直读 `/proc` + `fs.statfs`，不调外部命令、不装采集 agent |
| 常驻 | systemd system unit（开机自启、崩溃自愈、日志进 journald） |
| 运行用户 | `cathome`（系统用户，无登录 shell；只读仓库，不写盘） |

## 二、文件结构

```
tools/linux-see-window/
├── server.js       服务端（采集 + HTTP）
├── web/index.html  前端单页（2 秒轮询，零框架）
├── deploy/
│   ├── linux-see-window.service  systemd unit 副本
│   └── install.sh                幂等安装脚本
└── README.md
```

## 三、部署（换机复现）

前置：`nodejs` 已装（`sudo apt install -y nodejs`）、`/cathome4/mau` 仓库已克隆。

```bash
sudo bash /cathome4/mau/tools/linux-see-window/deploy/install.sh
```

脚本做六件事：建运行用户 → 校验 node → 校验文件 → 装 unit → enable + restart → 状态核验。
幂等，重复执行安全。

手动核验：

```bash
systemctl status linux-see-window
journalctl -u linux-see-window -n 50 --no-pager
curl -s http://127.0.0.1:7900/api/ping      # 期望 pong
```

## 四、HTTP 面

| 路径 | 返回 |
|:--|:--|
| `GET /` | 前端单页 |
| `GET /api/health` | 全量快照 JSON（含 `errors` 数组——采集失败项如实上报，不静默回默认值） |
| `GET /api/ping` | `pong`（存活探针） |

## 五、采集项

CPU（总体 + 每核使用率，两次采样差值）· 负载（1/5/15 + 运行进程数）· 内存与 Swap · 磁盘用量（`/` 与 `/cathome4`）· 网络收发速率 · 磁盘读/写速率与忙碌度 · 开机时长 · 进程 TOP 8（CPU% + 常驻内存）· 发行版 / 内核 / 架构。

数值口径：速率类均为**相邻两次采样差值 ÷ 时间差**，首轮为 `null`（前端显示 `—`），不做零值伪装。

## 六、安全面

三层，任一层单独失守仍不暴露：

1. 云防火墙：公网只开 22，7900 不对公网开放
2. systemd：`IPAddressDeny=any` + `IPAddressAllow=100.64.0.0/10`（cgroup eBPF 层丢包）
3. 应用层：来源地址白名单（tailnet + localhost），其余 403

另加 systemd 加固：`NoNewPrivileges` · `ProtectSystem=strict` · `ProtectHome` · `PrivateTmp` · `PrivateDevices` · `CapabilityBoundingSet=`（空集）· `MemoryHigh=150M` / `MemoryMax=200M`。

## 七、已知限制

| 限制 | 说明 |
|:--|:--|
| 无历史曲线 | 只报「此刻」——没有时序库，要历史看 journald 或后续接 CH4 的 runs/ |
| 温度 | 虚拟机上 `/sys/class/thermal` 通常不存在，显示为 `—` |
| 进程 CPU% 首轮为空 | 需要相隔两次采样才有差值 |
| 不显示服务健康 | 只报资源；`systemctl is-failed` 类判定留给后续 |
| 无鉴权 | 依赖网络层三重门禁；tailnet 内任何节点可读 |

## 八、单位与命名约定

- 端口落在**外围工具下行段**（7900），CH4 主端口族向上寻路，组端口 / 前端测试端口亦走下行段
- 服务区路径固定 `/cathome4/<并列目录>`：`mau` / `ccbp` / `mauout` / `data` / `workspace`
