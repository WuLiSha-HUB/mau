# Mau — CatHome5 / Mau-Go

> **把 CatHome4 + Mau 在 Go 里重新实现的那一层**：宿主（CatHome5）+ 运行时（Mau-Go）共一个 Go module `cathome5`。
>
> 判据一句话：**行为对得上，实现自主设计**——同一对话输入 → 同一 LLM 请求字节（`messages[]` / `tools[]`）· 同一工具调用 → 同一回执字节 · 同一数据目录 → 能继续跑。C# 版是功能基准，不是实现基准。
>
> 档案：本仓级 → `ccbp:Project/Mau`；Go 两区块细档 → `raw/ccbp/Project/{CatHome5,Mau-Go}/`（2026-11-14 归档，逐步迁回）。

---

## 一、它是什么

一个**自举宿主**：模型在它里面跑，工具由它提供，行为由 `.mau` 语料表达。

| 层 | 内容 | 位置 |
|:--|:--|:--|
| **宿主面** | 会话 / 工具池 / LLM 流 / 管理端 45 路由（13 段）/ 静态页 / 外壳 | `host/` · `cmd/cathome5/` |
| **运行时面** | `.mau` 解释执行 / 四柱图 / 受控根与文件服务 / 积木执行体 / 工具组注册 | `mau/` · `cmd/mauprobe/` |
| **桥与工具** | cs 桥（Roslyn 13 方法）/ 对拍器 / 浏览器链路验证 / 各域探针 | `host/csbridge/` · `tools/` |

**三个「可」**：

| # | 属性 | 含义 | 缺了会怎样 |
|:--:|:--|:--|:--|
| 1 | **可复现** | 同一输入 → 同一输出字节（对 C# 基准） | 两版行为漂移，无从对拍 |
| 2 | **可自举** | 工具组 = `.mau` 语料 Flow，注册 / Tick / 自述 / 热重载独立 | 每加一个工具都要改宿主程序集 |
| 3 | **可观测** | 日志三投影（`log_all` / `oa_all` / `err_all`）+ 快照十二字段 + 帧号 | 出事只能猜 |

## 二、双区块与目录

| 区块 | 子树 | 职责 |
|:--|:--|:--|
| **CatHome5**（宿主面） | `host/` · `cmd/cathome5/` | 会话 / 工具池 / LLM / 管理端 / 静态页 / 外壳 |
| **Mau-Go**（运行时面） | `mau/` · `cmd/mauprobe/` | 语料解释 / 四柱图 / 受控根 / 积木 / 工具组 |

```
cmd/cathome5/      宿主入口——启动链（数据根 → schema → 配置 → workspace → 日志 → 路由 → 起服 → 100ms 帧序）
cmd/mauprobe/      P1a Go 探针——合成语料跑状态迁移
host/config/       数据根三级锚定 / 端口段 / schema 同步 / 全局配置三层 / 每猫配置
host/session/      视图块 / ViewBus / 待处理队列 / 注入面快照 / 轮执行（LLM 流消费 · 相位七态 · 工具批）
host/toolpool/     工具池——定义聚合 / 内置定义 11 件 / 池三索引 / 执行序表 / 授权面 / tools_drift
host/llm/          请求体逐字节序列化 / SSE 翻译 / 端点池与密钥 / 视觉服务 / 自报身份头
host/admin/        管理端 13 段 45 路由（LLM 池 / cat-config / cat-detail / workspace / packs / groups …）
host/http/         主端口外观层——路由装配 / 静态面 / SSE 帧四行 + 512 DropOldest / 快照 250ms 循环
host/observe/      快照结构体（version=3 十二字段）/ 日志总账（内存环形 + 三投影）/ 依赖探测 / 资源采集
host/inject/       注入面构建——提示词组装（baseRole / persona / injectList）/ 注入报告
host/group/        组数据面——group.json + messages.jsonl / 六态校验 / 订阅游标 / _trash 可恢复
host/fetest/       前端测试服务拉起——四锚解析 / 已监听跳过 / 缺依赖降级 / watchdog
host/csbridge/     cs 桥外置服务——.NET 10 + Roslyn，POST /invoke（13 方法白名单）
mau/runtime/       机制核——DataBox / Inbox / Cube / OA（四态九法）/ Runner / Trace
mau/interp/        导线解释器——条件→消费→执行 / 忙门 / 代际回投 / 时限
mau/front/         .mau lexer / parser / validator / analyzer + 四柱 AST + 积木契约表
mau/graph/         四柱 AST → 运行时图（Flow + SensorLoop，状态迁移 trace）
mau/fs/            受控根与编码契约——根寻址 / 只读拒绝 / BOM·换行·GBK 保真 / 软删除回收 / stat·hash·diff
mau/bricks/        工具积木执行体（91 件）+ 公共积木（14 件）
mau/toolset/       组装配——BuildBrickSet（105 件）+ LoadGroups（引用积木全齐才注册）
mau/db/            条目库——库 / 条目 / 墓碑 / 内容寻址
tools/             探针与对拍器（browsercheck / csprobe / dbprobe / hostprobe / llmprobe / p3a-ab / p3c-round …）
reports/           S1 侦察报告（历史材料，不承载路线）
```

## 三、坐标系（四柱与工具面）

**四柱**——语料的四种声明单元，也是运行时的四个机制面：

| 柱 | 语义 | 一句话判据 |
|:--|:--|:--|
| 状态机 | 互斥单值 FSM | 集合首元素 = 初始态，出度 0 = 终态，自环 = 保持 |
| 传感器 | 探针 → 规范参数 | 三形态：被动（DataBox 信号沿）/ Command（CommandBus）/ 主动（壳协程） |
| 导线 | 条件 → 动作 → 结果 | 条件 = 事件沿 + 状态断言；结果 = 转移分叉（成功 / 失败互斥） |
| 槽 | 容量声明 + 观测快照 | 计数 + TryAcquire / Release |

**工具面三个口径**（数不同源，别混）：

| 口径 | 值 | 权威 |
|:--|:--|:--|
| 积木 | **105 件** = 工具 91 + 公共 14 | `mau/toolset/load.go` `BuildBrickSet` |
| 工具池 | **110 件 / 20 组** | 启动日志 `tools.boot | 组 20 | 池 110 件 | 注册表 110 件` |
| 工具定义组 | 20 组（每组一块 `tools.<组>` 定义积木） | `mau/bricks/` TOOLS 类目 |

## 四、面结构（宿主四面）

| 面 | 读什么 | 写什么 | 判据 |
|:--|:--|:--|:--|
| **会话面** | 前文 jsonl / 视图块 / 待处理队列 | 轮执行（相位七态）/ 工具批 / 关轮结算 | 同一数据目录能继续跑 |
| **工具面** | 工具定义（池三索引）/ 执行序表 | 授权面实时 + 注入面会话快照 | 两面漂移 = 「授权了但工具不可见」→ 新会话 |
| **LLM 面** | 端点池 / 密钥 / 视觉 | 请求体逐字节序列化 / SSE 翻译 / 自报身份头 | 请求字节可对拍 |
| **观测面** | `runs/<ts>/` 三文件 + `frame` | 内存环形 8192 + 三投影同锁同批 | 排查第一入口 = `err_all.txt` |

**配置与受控根**：`data:config` 只读面 + `POST /api/v1/config` 写通道（body 文件法）；每猫 `cat.cfg` 20 字段，`enabledRoots` 空 = 仅常驻根，越界解析期拒绝（`ROOT_UNKNOWN_ID`）。

## 五、构建与起服

```powershell
# 门禁（本机 go：D:\Mau\WorkSpace\tools\go\bin\go.exe）
go build ./...
go vet ./...
gofmt -l .
go test ./... -count=1

# 构建宿主
go build -o bin/cathome5.exe ./cmd/cathome5

# 起服（开发区 8079 + 数据副本；不碰运行中的 8080 生产实例）
bin\cathome5.exe --data-root <数据目录> --area dev --html-root <html 目录>
```

起服解析优先级：数据根 `--data-root` > `CH4_DATA_ROOT` > 仓库根（Mau.sln）> `%LOCALAPPDATA%\CatHome4`；区域 / 端口 `--area` > `CAT5_AREA` > exe 是否在仓库树内（dev 8079 / deploy 8080）。

## 六、现状（2026-11-14 实测）

| 项 | 值 |
|:--|:--|
| 版本 | **0.36.2**（`version.txt` 单点；构建经 `-ldflags -X main.version` 注入） |
| 门禁 | `go build ./...` 0 错 · `go vet ./...` 0 错 · `gofmt -l .` 空 · `go test ./... -count=1` = **16 包 ok + 18 包无测试，0 失败** |
| 运行实例 | `mauout:cathome5.exe` 0.36.2（管理面板 8080 · majordomo 对话 8081 · 数据根 `data:`） |
| 依赖 | `golang.org/x/text`（GBK）· `github.com/dlclark/regexp2`（.NET 语义正则）——仅此两件 |
| 分支 / 远端 | `RushieraChange` · gitee `wu_lisha/mau` + github `Rushiera/Mau`（gitee `master` = C# 旧线，历史锚） |

## 七、路线（P 阶段）

| 阶段 | 内容 | 状态 |
|:--|:--|:--|
| P0 | cs 桥外置服务（Roslyn 13 方法） | ✅ |
| P1a | 机制核（DataBox / Inbox / Cube / OA / Runner / Trace） | ✅ C# 对拍逐行一致 |
| P1b | `.mau` 前端（lexer / parser / validator / analyzer）+ 四柱图 | ✅ |
| P2 | fs 受控根与编码契约 | ✅ |
| P3 | 宿主（主端口 / 工具池 / LLM / 管理端 / 前端测试） | ✅ |
| P4 | 工具面 105/105 执行体收口 | ✅ |
| P5 | 部署链 + 外壳（托盘 / 自启 / CLI 双写） | ✅ 余人工面 |
| d-1 | 多猫面（存储 / 指令族 / 每猫端口 / 管理面编辑） | ✅ |
| 0.33–0.36 | 每猫注入写面 / 重启链双硬化 / 全跟随三键 / **受控根猫级隔离** | ✅ 现行 |

## 八、文档索引

| 文档 | 内容 |
|:--|:--|
| `ccbp:Project/Mau/project.md` | 仓库级档案——定位 / 双区块 / 版本结构 |
| `ccbp:Project/Mau/todo.md` | 行动唯一真相（Go 线现行 + C# 线冻结） |
| `raw/ccbp/Project/CatHome5/project.md` · `todo.md` | 宿主面细档（0.36.2 交接，逐步迁回） |
| `raw/ccbp/Project/Mau-Go/project.md` · `todo.md` | 运行时面细档（0.1.22 线） |
| `raw/ccbp/Project/CH4/` | C# 时代协议规格（功能基准的原始设计） |
| `ccbp:L3/Mau/` | 现场手册——⚠ C# 时代坐标，Go 时代重建中 |
| `README.md`（本文件） | 仓级入口——是什么 / 结构 / 现状 / 路线 |

## 九、与邻居的分工

| 邻居 | 分工 |
|:--|:--|
| **C# Mau / CatHome4**（1.8.266，`raw/` 只读） | **功能基准**——同一输入同一输出字节；不重构、不修 bug、不为了配合 Go 而改 |
| **CH4 时代档案**（`raw/ccbp/Project/CH4/`） | 协议规格与设计理念的原始出处 |
| **ACS**（Agent Control Surface） | 不同控制论层：本仓是「模型跑在里面的宿主」；ACS 是「把模型接到**别的程序**上的桥」——互补，不重叠 |
