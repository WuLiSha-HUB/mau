# biliwatch —— B 站直播状态监控 → QQ 群播报

> 定位：轮询 B 站直播间公开接口，命中开播 / 下播跃迁时，经 **CH4 通道出口**（`POST /api/v1/channels/publish`）向配置好的 QQ 群推一条消息。
> 定位二：**独立常驻服务**——自带端口与配置页，不寄生 CH4 进程；CH4 只作消息出口，两者松耦合（CH4 侧零改动）。
> 形态参照：`tools/linux-see-window/`（同为零依赖常驻 + 单页界面）。**服务端 TS · 前端纯 JS。**

---

## 一、形态

| 项 | 值 |
|:--|:--|
| 服务端 | **TypeScript 直跑**——Node 原生类型擦除（`node server.ts`，需 Node ≥ 22.6；本机实测 v24.15.0），**无构建步骤、无产物目录** |
| 依赖 | 运行时**零 npm 依赖**（只用 `node:*` 内置模块）；`@types/node` 仅编译期类型，devDependency |
| 前端 | 单页纯 HTML + 原生 JS（零框架零依赖，`web/index.html`） |
| 端口 | **7901**（外围工具下行段；linux-see-window 占 7900） |
| 监听 | 默认 `127.0.0.1:7901`（本机测试）；`BW_BIND=0.0.0.0` 时靠来源白名单（localhost + tailnet）兜底 |
| 数据面 | 直读 `api.live.bilibili.com/room/v1/Room/get_info`——无需开发者资质、无需主播身份码 |
| 出口面 | CH4 通道播报端点（Bearer 通道 Key · 广播语义：白名单即目标集） |
| 状态面 | 自带配置页（`GET /`）+ 房间状态 + 事件流水；每 3 秒自刷新 |
| 单实例 | 端口占用即启动失败（`EADDRINUSE` → 退出码 2）——端口本身就是锁 |

**为什么服务端用 TS：** 本服务后续要接 B 站开放平台（弹幕 / 礼物 / 长连）与多平台取数，边界数据形状会持续变。TS 的价值在**边界声明 + 编译期自检**——外部回执（B 站 JSON、CH4 回执）全部显式建模，改字段时编译期即报。前端保持纯 JS：静态页无构建链，形态与 CH4 前端一致。

## 二、文件结构

```
tools/biliwatch/
├── server.ts        服务端（取数 + 状态机 + 出口 + HTTP 面 · TS 直跑）
├── tsconfig.json    工程口径（strict · noEmit · nodenext）
├── package.json     工程声明（type=module · scripts · devDeps: @types/node）
├── web/index.html   前端单页（配置 + 状态 + 事件流水，零框架）
├── data/            运行时数据（**不入库**）
│   ├── config.json  配置（含通道 Key）
│   ├── state.json   场次基线（跨重启保留）
│   └── events.jsonl 事件流水（追加）
├── node_modules/    编译期依赖（**不入库**，按 package.json 复现）
└── README.md
```

## 三、快速开始（本机）

```bash
cd tools/biliwatch
npm install                                  # 只装 @types/node（编译期）
node server.ts                               # 前台常驻 → 浏览器开 http://127.0.0.1:7901
node server.ts --selfcheck                   # 自检：起服务 → 打自己的 HTTP 面 → 退出
node server.ts --selfcheck --room <roomId>   # 附一次真实取数探针（只读）
```

页面里填三样：**CH4 播报端点**（默认 `http://127.0.0.1:8080/api/v1/channels/publish`）· **通道 Key** · **房间号**。保存后点「立即检查」即可看到事件流水。

## 四、工程与校验（TS 工具面）

| 工具 | 入口 | 口径 |
|:--|:--|:--|
| `ts-check` | `tsconfig.json` | **唯一编译权威**——当前 0 错 0 警 |
| `ts-lint` | 同上 | 松散写法扫描；本工程有意项见下表 |
| `ts-format` | 同上 | 格式哨兵——当前 `changedFiles=0` |

**lint 有意项（非缺陷）：**

| 规则 | 条数 | 理由 |
|:--|:--|:--|
| `as-unknown-as` | 2 | 合并器的**形状认定出口**（`asConfig` 及递归点）——键集恒取自默认配置、值域由 `normalizeIncoming` 逐字段清洗，不做类型转换；代码注释已标理由 |
| `console-log` | 8 | 常驻服务的日志出口（前台可见性）；事件另落 `data/events.jsonl` |

**类型擦除的写法约束**（本工程遵守）：不用 `enum` / `namespace` / 构造器参数属性（需转换，擦除模式不支持）；ESM 走 `node:` 前缀 + `import.meta.dirname`（无 `__dirname`）。

## 五、出口链路（CH4 侧）

通道在 **CH4 控制台 → 配置页「HTTP 注入通道」区块**新建：绑定机器人 + 填「允许群」（填写项即播报目标集），**能力两项至少留一项**（`catKey` 可空 = 纯播报通道）。Key 明文只在新建 / 重置时显示一次，填进本服务即可。

```
POST http://127.0.0.1:8080/api/v1/channels/publish
Authorization: Bearer <通道 Key>
{"text": "..."}
→ 200 全部成功（delivered=N）/ 207 部分成功（逐群明细）/ 400 参数 / 401 Key 无效
   403 未配出口或白名单为空 / 404 群不可达 / 502 平台拒绝 / 503 停机态
```

🔴 群不在本服务配置里——**群由通道白名单决定**；服务只负责「什么时候发、发什么」。要换群改 CH4 侧通道，不动本服务。

## 六、状态机口径

- **场次键 = `live_time`（开播日期串）**——同一场次内恒定，天然去重（同场不重复报）。
- `live_status === 1` 视为开播；**`0`（未开播）与 `2`（轮播）一律视为非开播**。
- 跃迁 = 场次键变化：`'' → 日期串` 记开播；`日期串 → ''` 记下播（时长 = 检测时刻 − 场次起点）。
- 首轮只建基线不发消息；基线**落盘**（`state.json`），服务重启不重报、不漏报。
- `live_time` 是 **UTC+8 日期串**（未开播时恒为 `"0000-00-00 00:00:00"`），不是 Unix 时间戳——时长按 +8 手工解析，不依赖本机时区。
- 取数失败：指数退避（`intervalSec × 2^(n−1)`，上限 `backoffMaxSec`）并落 `check-fail` 事件——不静默。

## 七、配置项（`data/config.json`，页面可编辑）

| 键 | 默认 | 说明 |
|:--|:--|:--|
| `rooms[]` | `[]` | `{ roomId, note, enabled }`——`note` 用于正文 `{name}` |
| `poll.intervalSec` | 30 | 轮询间隔（5–3600） |
| `poll.timeoutMs` | 10000 | 单次请求超时（1000–30000） |
| `poll.backoffMaxSec` | 300 | 失败退避上限（30–3600） |
| `publish.endpoint` | `http://127.0.0.1:8080/api/v1/channels/publish` | CH4 播报端点 |
| `publish.key` | 空 | 通道 Key；**页面留空 = 不修改**（防误清） |
| `publish.enabled` | true | 关 = 只检测不发送 |
| `notify.onLive` / `onOffline` | true | 两类通知开关 |
| `templates.live` / `offline` | 见默认 | 占位符：`{name}` `{roomId}` `{title}` `{uid}` `{url}` `{duration}` |

## 八、HTTP 面

| 路径 | 方法 | 说明 |
|:--|:--|:--|
| `GET /` | — | 配置与状态页 |
| `GET /api/ping` | GET | `pong`（存活探针） |
| `GET /api/status` | GET | 配置（Key 只回掩码）+ 房间运行态 + 事件 + 错误 |
| `POST /api/config` | POST | 保存配置（字段级清洗，非法项出声不静默） |
| `POST /api/check` | POST | 立即检查全部启用房间 |
| `POST /api/test` | POST | 发一条测试消息（默认文案，可传 `{"text":"..."}`） |

## 九、验证判据

| # | 判据 | 面 |
|:--:|:--|:--|
| 1 | `ts-check` 0 错 0 警 · `ts-format mode=check` 零差异 | 编译层 |
| 2 | `node server.ts --selfcheck` 输出 `SELFCHECK_OK`，`/api/ping` 返 `pong` | 服务面 |
| 3 | `node server.ts --selfcheck --room <id>` 探针打印 `live_status` / `live_time` / `title` | 取数面 |
| 4 | 填 Key 后「发送测试消息」→ 页面显示 `sent`，目标群收到 | 出口面 |
| 5 | 主播真实开播 → 群里出现一条；下播 → 第二条（含时长） | 端到端 |
| 6 | 同一场次内多次轮询不产生第二条开播消息 | 去重 |

## 十、已知限制

| 限制 | 说明 |
|:--|:--|
| 无历史曲线 | 只报「此刻」，历史看 `data/events.jsonl` |
| 无鉴权 | 默认只绑 127.0.0.1；对外暴露时必须走 tailnet 白名单 |
| 群不可自服务选择 | 群由 CH4 通道白名单决定（设计如此——主体授权在 CH4 侧） |
| 轮播语义 | `live_status=2` 按非开播处理（`1→2` 记一次下播） |
| 长时不跑 | 本机测试期前台运行；上 Linux 需补 systemd unit（见 `linux-see-window/deploy/` 同型） |
| Node 版本 | 类型擦除需 Node ≥ 22.6（否则 `server.ts` 直接跑不起来） |

## 十一、与 linux-see-window 的差异

| 项 | linux-see-window | biliwatch |
|:--|:--|:--|
| 语言 | 纯 JS | 服务端 TS（直跑）· 前端纯 JS |
| 数据源 | 本地 `/proc` 只读 | 外网 B 站公开接口 |
| 写面 | 无（纯只读） | 配置落盘 + 事件流水 |
| 出口 | 无 | CH4 通道播报 |
| 端口 | 7900 | 7901 |
