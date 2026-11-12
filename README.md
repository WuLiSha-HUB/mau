# CatHome5 / Mau-Go — 施工仓

> 档案在 `ccbp:Project/CatHome5/` 与 `ccbp:Project/Mau-Go/`；本仓只放代码与施工产物。
> 施工面隔离：只写本目录；`mau:` 与 `mauout:` 只读引用（不触发其构建、不回写）。

## 目录

| 路径 | 是什么 |
|:--|:--|
| `mau/runtime/` | P1a 机制核——DataBox / Inbox / Cube / OA（四态 + `GetOffice` / `Cancel` / 四态日志）/ Runner / Trace |
| `mau/interp/` | P1a 导线解释器——条件→消费→执行、忙门、代际回投、时限 |
| `mau/front/` | P1b 前端——`.mau` lexer / parser / validator / analyzer + 四柱 AST；`brick.go` 加载积木契约索引 |
| `mau/graph/` | P1b-3 图构建——四柱 AST → 运行时图（`runtime.Flow` + `SensorLoop`，状态迁移 trace） |
| `mau/fs/` | P2 受控根与编码契约——根寻址 / 只读拒绝 / BOM·换行·GBK 保真 / 目录树·文件名搜索·移动 / 软删除回收（Recycle）·复制·PE 版本解析 / 状态（stat）·SHA256·文件与目录对比 |
| `mau/bricks/` | 工具积木全集（**47 件**）——JsonArgs 零容忍 / A214 头（System.Text.Json 转义口径）/ text 7 · file 9 · ps 1 · git 6 · http 3 · web 4 · browser 6 · vision 2 · ts 5 · proc 4 + 公共五积木（`oa_self.go`） |
| `mau/toolset/` | **P4 组装配**——47 件积木映射 + 公共五积木（`oa.is_open` / `oa.claim_next_simple` / `oa.get_str` / `oa.complete_str` / `self.desc`）+ `LoadGroups`（语法零诊断 + **引用积木全齐才注册**，缺件逐条出声跳过；现注册 9 组） |
| `host/config/` | P3-a 宿主配置面——数据根三级锚定 / 端口段 / schema 同步 / 全局配置三层解析 / 每猫配置 / workspace.json 降级 |
| `host/toolpool/` | P3-b 工具池——定义聚合（Bricks → 常量解析）/ 内置定义 11 件 / 池三索引 / 执行序表 / 注册表 / 授权面 / `tools_drift` |
| `host/session/` | P3-b/P3-c 会话层——视图块与载荷 / ViewBus / 系统注入 / 待处理队列 / Note / timeback / 图像包裹 / 前文与视图存储（jsonl）/ 会话元数据 / 注入面快照 / **轮执行（LLM 流消费 · 相位七态 · 工具批 PlanBatches · roundsum · 关轮结算）** · **OA 工具工单面（`ToolOffice` 窄接口 / `pollOffice` 四态轮询 / Tick 收回执）** · **图片注入批后段** · **自报身份头** |
| `host/llm/` | P3-c LLM 面——请求体逐字节序列化（messages / tools / 参数）/ SSE 翻译（reasoning·content·tool_calls·usage·finish·异常）/ 端点池与密钥解析 / 重试与错误面 / **视觉识别服务（`vision.go`）** / 自报身份头（`selftag.go`） |
| `host/admin/` | P3-d 管理端——**13 段 45 条路由**（LLM API 池 CRUD / cat-config 读·合并写 / cat-detail / cat-default / workspace / packs / appearance / user-state / host-restart / open-data-dir / media / groups / inject / db 占位）；路由装配 `Register` + 读面池解析（packs / appearance / 固定根表 / 工具清单） |
| `host/inject/` | P3-d 注入面构建——提示词组装（baseRole / persona / injectList）/ 注入报告 JSON / 项目摘要 |
| `host/group/` | P3-d 组数据面——`group.json` + `messages.jsonl` / 六态校验 / 订阅游标 / `_trash` 可恢复 |
| `host/fetest/` | P3-e 前端测试服务拉起——四锚解析 / 已监听跳过 / 缺依赖降级 / watchdog |
| `host/http/` | P3-a 主端口外观层——路由装配（构造期条件注册）/ 静态面（`__V__` / 穿越校验 / 缓存口径）/ SSE 帧四行 + 512 DropOldest / 只读 API（snapshot·logs·config·cats）+ 250ms 快照循环 |
| `host/observe/` | P3-a 观测面——快照结构体（version=3 十二字段）/ 日志总账（内存环形 8192 + log_all·oa_all·err_all 三投影）/ 启动降级登记（boot 段九字段）/ 依赖探测（14 条——受控根 / 解释器 / 内核 / 服务 / 自举链）/ 资源采集（PSAPI 工作集与私有字节） |
| `cmd/cathome5/` | 宿主入口——启动链（数据根 → schema 同步 → 配置 → workspace.json → 日志 → 路由装配 → 起服 → 250ms Pump）+ 早期诊断 sink 三级落点 + **机制面装配（OA / Runner / 组注册 / git·下载·浏览器·视觉·ts·proc 各服务构造）+ 100ms 帧序 ticker** |
| `cmd/mauprobe/` | P1a Go 探针——合成语料（TextCat 读链）跑状态迁移 |
| `host/csbridge/` | P0 cs 桥外置服务——.NET 10 + Roslyn，`POST /invoke` |
| `tools/mau-probe/` | 乙1 C# 对拍探针——独立进程驱动 `FL_TextCat.dll` |
| `tools/p3a-ab/` | P3-a 实机对拍器——两侧起服 + 8 类响应逐条勾选（报告 `reports/<ts>/`） |
| `tools/p3c-round/` | P3-c 请求级对拍器——两份数据副本 + 本地 mock LLM，逐字节比对请求体与 roundsum / session.json 结算；`-fixture-tool` 走工具链对拍（首轮 tool_call → 次轮终文，比第 2 请求体 + RoundTools=1） |
| `tools/fe-fake/` | frontend-test 上游替身——固定响应假 Vitest（`-port` `-body`），对拍转发保真用 |
| `tools/browsercheck/` | 浏览器链路验证器——起真内核跑 Open/Read/Eval/Shot/Tabs/Close 全链（`-kernel` `-data-root` `-url`） |
| `bin/` | 构建产物（不入库） |

## 一、C# 对拍探针（乙1）

```powershell
# 编译
dotnet build tools/mau-probe/MauProbe.csproj
# 跑：20 帧，每帧 100ms
tools/mau-probe/bin/Debug/net10.0/MauProbe.exe 20
```

依赖注入序列（最小驱动）：

```csharp
AuditStore.Default = new AuditStore();
DataBox.Bind<FileSystemService>(new FileSystemService(new[]{ "D:\\Mau\\WorkSpace" }, "…/fs_recycle"));
var guard = new ThreadGuard();
var oa = new OA(guard);
DataBox.Bind<IOA>(oa);      // 传感器积木经 DataBox 取 OA
oa.Audit = AuditStore.Default;
var cmd = new CommandBus(guard);
var ids = new IdAllocator();
var runner = new FlowRunner(guard, oa, cmd, ids);
runner.RegisterFlow((IFlow)Activator.CreateInstance(type), "TextCat");
```

关键事实：

- **引用源 = mauout 运行区，TFM = net10.0**（`CatHome4.runtimeconfig.json` 实测；L3 手册「.NET 8」已过时）。
- `Reference` + `HintPath` + dll 副本可行——**不使用 `ProjectReference`**（会在 mau 目录生成 obj/bin）。
- 生成物内部直写 `AuditStore.Default?.Record("Flow","trace.fire"/"trace.state", …)`；`AuditStore.Snapshot()` 从 `LogStore` 重建，**是状态迁移序列的唯一干净出口**。
- `trace.sample` 被 `Record` 过滤（高频采样），`trace.fire` / `trace.state` 完整保留。

## 二、P0 cs 桥服务

```powershell
dotnet build host/csbridge/MauCsBridge.csproj
host/csbridge/bin/Debug/net10.0/MauCsBridge.exe --port 8130 --root workspace=D:\Mau\WorkSpace --root ccbp=D:\Mau\CatCatBigParty
```

契约：

```
GET  /health  → { ok, service, version, pid, roots, methods }
POST /invoke  { "method": "list", "argsJson": "{\"path\":\"…csproj\"}" }
              → { ok, result }（result 内层为工具回执文本）
```

方法白名单 13：`check / build / test / list / read / find_ref / find / patch / member / comment / dead / comment_check / format`。

实测（2026-11-11）：

| 调用 | 结果 |
|:--|:--|
| `list` | 277 ms，返回 `Program` 类 |
| `check` | 7 ms（项目缓存命中），0 错误 0 警告 |
| `read` | 8 ms，返回带行号源码 |
| 未知方法 | `ERR\|UNKNOWN_METHOD`，`ok=false` |
| 双进程并发 build 同一项目 | 1.3 s / 2.2 s，**均 exit 0，无 obj 冲突** |

## 三、P1a Go 机制核

```powershell
D:\Mau\WorkSpace\tools\go\bin\go.exe build -o bin\mauprobe.exe ./cmd/mauprobe
bin\mauprobe.exe 20
D:\Mau\WorkSpace\tools\go\bin\go.exe test ./...
```

已实现：

- `Runner` 六段帧序（帧推进 → Inbox 排空 → 指令分发（空）→ Flow 驱动 → 传感器壳 → OA 结算）
- `Cube` 时限（Total / Idle / Infinite 三模式骨架）
- `Inbox[T]` 跨线程双缓冲
- `DataBox` 三区（服务 / 数据 / 信号）
- `OA` 四态 + 九法 + **超时不对称**（Open `>` / Work `>=`）
- `interp.Wire` 导线解释器（条件→消费→执行、忙门、代际回投、时限迟到丢弃）
- `Trace` 内存审计流（对应 `AuditStore`）

对拍结果（C# vs Go，同一 TextCat 读链）：

```
frame 1  T_Init        fire / S_Read=Idle
frame 2  T_ReadClaim   fire / oa.claim / S_Read=Claimed
frame 2  T_ReadArgs    fire / S_Read=Ready
frame 2  T_ReadExec    fire
frame 3  T_ReadExec    S_Read=Done
frame 3  T_ReadReply   fire / oa.complete / S_Read=Idle
```

**序列、帧号、状态名逐行一致。** 已知差异两处：

1. `oa.complete` 的 result 摘要：C# `ints:0,strs:1`，Go `strs:1`——载荷摘要格式未对齐（非语义）。
2. Go 探针是合成语料，只实现 `S_Read` 一条链；C# 报告 7 个状态机 / 36 导线。

## 三-B、P1b `.mau` 前端（lexer / parser / validator / analyzer）

```powershell
D:\Mau\WorkSpace\tools\go\bin\go.exe test ./mau/front/ -count=1
```

- `mau/front/lexer.go`——v3-default 17 字形（15 单字符 + `:=` / `<-`）+ 参数子词法 + BOM 剥离 + E001–E004
- `mau/front/parser.go`——按 § 切段、四单元前缀分派（S_/P_/T_/R_）、条件区 / 动作区 / 结果区、E100–E105
- `mau/front/ast.go`——四柱 IR（Doc / StateMachine / Sensor / Wire / Slot）
- `mau/front/validator.go`——语义门禁 E200–E206 / 积木门禁 E400–E408（依赖 `Bricks/index.json` 契约）
- `mau/front/analyzer.go`——图论 E300–E302（无界环 / 不可达 / 失败侧无恢复）+ SCC 关键路径报告
- `mau/front/brick.go`——`BrickTable` 加载 `Bricks/index.json`（含 BOM 剥离）
- `mau/front/validator_test.go` / `analyzer_test.go`——13 例；`TestValidateTextCat` 读真实语料 + 真实索引，全链零诊断
- `mau/graph/graph.go`——四柱 AST → 运行时图：`Build` / `Tick` / `TickSensors` / 状态迁移 `Trace`
- `mau/graph/graph_test.go`——7 例；`TestTextCatTraceSequence` 断言 `trace.state` 序列（T_Init → T_ReadClaim → T_ReadArgs → T_ReadExec → T_ReadReply）与 C# 实测一致
- `mau/front/parser_test.go`——7 例；`TestParseTextCat` 读真实语料，断言四柱 7/7/36/0 与 C# 生成物注释头一致

语料路径默认 `D:\Mau\Mau\corpus\ch4\TextCat\text_cat.mau`，可用 `MAU_TEXT_CAT` 覆盖；不可读时 Skip 并在输出出声。

## 三-C、P3-a 宿主启动链（实机起服）

```powershell
# 构建
D:\Mau\WorkSpace\tools\go\bin\go.exe build -o bin/cathome5.exe ./cmd/cathome5

# 起服（开发区 8079 + 数据副本；不碰运行中的 CH4 8080/8081）
bin\cathome5.exe --data-root D:\Mau\WorkSpace\tools\CatTemp\cat5-data --area dev --html-root D:\Mau\Mau\CatHome4\html
```

解析优先级：

| 项 | 顺序 |
|:--|:--|
| 数据根 | `--data-root` > `CH4_DATA_ROOT` > 仓库根（Mau.sln）> `%LOCALAPPDATA%\CatHome4` |
| 区域 / 端口 | `--area` > `CAT5_AREA` > exe 是否在仓库树内；端口 `--port` > `http.port` 配置 > 区段主端口（dev 8079 / deploy 8080） |
| 静态页根 | `--html-root` > `CAT5_HTML_ROOT` > workspace 的 `mau` 根 + `CatHome4\html` > exe 目录 `html` |

实机验收（2026-11-11）：

- `GET /api/v1/snapshot?logs=N` → 200，十二字段齐（`version:3` / `pid` / `frame` / `cats` / `sessions` / `oa` / `tools` / `boot` / `boxes` / `deps` / `res` / `logs`）
- `GET /api/v1/logs?n=&cat=&type=` → `{version:1,logs:[{time,frame,level,category,module,type,message}]}`
- SSE `GET /api/v1/stream?topics=snapshot` 首帧四行 `id: 1 / event: snapshot / data: … / 空行`
- 静态页 `http://127.0.0.1:8079/` 直开——C# `html/` 零改动，页签与配置区正常渲染
- 运行目录 `Data/runs/<yyyyMMdd_HHmmss>/`：`log_all.txt` / `oa_all.txt` / `err_all.txt` / `startup.log`

已知缺口（显式占位，不是缺陷）：管理端 `db ×4`（P4 条目库面）· `llm-apis/probe`（P4 探活）· 主端口 9 条条件路由 · snapshot `cats` 段（Flow 注册表）。

## 四、门禁

| 项 | 状态 |
|:--|:--|
| `go build ./...` | 0 错误 |
| `go vet ./...` | 0 错误 |
| `gofmt -l .` | 空 |
| `go test ./...` | **22 行 = 16 包 ok + 6 包无测试**（0 失败）——逐包明细以 `go test ./... -count=1` 输出为准，本表不列举例数（会过期） |
| 依赖 | `golang.org/x/text` v0.44.0（GBK）· `github.com/dlclark/regexp2` v1.12.0（text-replace 正则面——.NET 语义） |
| `go test -race` | **不可用**——`-race requires cgo`；本机 `CGO_ENABLED=0` 且未装 gcc。接入 CI 前补 |

## 五、施工边界

- 只写 `WorkSpace:CatHome5/`。
- `mau:` / `mauout:` 只读：`Reference` + `HintPath` 引用已编译 dll；**禁用 `ProjectReference`**。
- csbridge 默认根 = `workspace`；需要读 mau 源码时显式加 `--root mau=D:\Mau\Mau`（只读场景，写面自律）。
