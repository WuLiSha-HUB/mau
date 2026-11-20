# Go 时代升级候选池

> 基线：0.36.8 · 2026-11-15 · 入口 `workspace:Mau`（Go 1.26.0，module `cathome5`）
> 定位：Go 仓「能用得更好的地方」候选池——**不是承诺，是等考虑清楚的备选**。
> 状态：本轮全部未实施。一次试改（callBrick 隔离 / Runner 平行数组合并 / args.go 注释）已回滚，存于 `git stash list` 的 `go-upgrade-rollback`。
> 边界：不改行为面契约（回执字节 / 状态迁移序 / 错误文本形态）；本池只动内部结构与工具面。

---

## 一、三条链路事实（已读码，未实测）

> 三条都与「要不要改」无关——它们现在就是这样。

| # | 事实 | 位置 | 实测判据 |
|:--:|:--|:--|:--|
| 1 | `CurrentFlowID` 是无同步包级变量：主线程 `Tick` 段 3 写、`defer` 复位；异步导线经 `go` 语句启动后，后台 goroutine 读它没有 happens-before 保证——可能读到 0 或别的 flow 的 ID | `mau/runtime/runner.go:259` 定义 · `:291`/`:300`/`:312`/`:321` 写与复位 · `mau/interp/wire.go` async 分支 · `mau/toolset/load.go` 12 处消费（OA 四件 / llm 五件 / self 两件 / host 一件） | `CGO_ENABLED=1` 下 `go test -race`；本机 cgo 未开。需构造「`[par]` 导线 + OA 积木」真链路 |
| 2 | **回执转义是双层**：回执层把非 ASCII 转 `\u4e2d`；发 LLM 时传输层是松口径（非 ASCII 直出），`\` 被再转一次 → **LLM 收到字面 `\u4e2d` 六个字符**，不是「中」 | 回执层 `mau/bricks/args.go` `jsonString` · 传输层 `host/session/cardpayload.go:68` `JSONString`（`b.WriteRune` 直出）· 拼接点 `host/llm/request.go:101` · 回执原样存 `host/session/toolbatch.go:37` | 抓一条真实请求体，看 `content` 里是 `\\u4e2d` 还是 `中`——日志 `runs/*/log_all.txt` 留档 |
| 3 | `graph` 包 4 个真实语料测试**长期 SKIP**：默认路径指向已不存在的 `D:\Mau\Mau\corpus\ch4\TextCat\text_cat.mau`；真实语料在 `mauout:corpus/ch4`（仓内无 corpus） | `mau/graph/graph_test.go:21-36` `loadTextCat` | `go test ./mau/graph -v` → 4 例 SKIP |

**事实 2 的三个口径**（选哪个决定改动面，未定）：

| 方案 | 回执层 | 传输层 | LLM 收到 | 前文字节 |
|:--|:--|:--|:--|:--|
| A 现状 | 严 `\u4e2d` | 松 | 字面 `\u4e2d`（6 字符） | 最差 |
| B 全严 | 直出 | 严 | 「中」 | ×3，撞 3 MB 预算 |
| C 全松 | 直出 | 松 | 「中」 | 最省 |

---

## 二、升级候选（按「一次改动消掉多少面」排序）

| # | 候选 | 现状 | Go 答案 | 量 | 代价 |
|:--:|:--|:--|:--|:--|:--|
| 1 | **context 全线接入** | `CurrentFlowID` 全局变量 + `interp` 靠闭包捕获 `prepared` 传数据 | `Tick(ctx)` → `Execute(ctx)` → `bricks.Xxx(ctx,…)`；`Wire[T]` 泛型让数据流进签名 | 12 处消费点 + 3 个包 | 大；需 `-race` 才能验 |
| 2 | **日志面 slog** | 三套风格并存：`func(level int, message string)` 回调 **286 处** · `LogStore.Add` 九参平铺（`logstore.go:209`）· `BrickOptions` 两字段（`WebLogf`/`Log`）· `OA.Log` 字段 | `*slog.Logger` + 自定义 `slog.Handler`（三投影天生是 Handler 形状）+ `slog.Level` | 286 处回调 | 中-大；横穿 `host/observe` + `bricks` |
| 3 | **`db.Result[T]` 泛型** | `Result{Data string}`——载荷是 JSON 文本，`Success(message, data string)` 手工序列化 | `Result[T]` 或 `Data json.RawMessage`（至少让类型系统看见「这是 JSON」） | 一处定义 + 调用点 | 中 |
| 4 | **包级可变函数变量收口** | `host/session/session.go:21-24` `var IsPrivilegedTool = func(...)` / `var SessionMetaPathProvider func(...)`——同文件里 `APIRole` 窄接口是正确姿势 | 构造注入 / 字段 | 2 处 | 小 |
| 5 | **`slices` / `maps`** | `sort.` 36 处（mau 16 / host 20）；`runtime/oa.go:380-390` `sortOffices` 手写双层循环；全仓 `slices.` **0 命中** | `slices.Sort` / `slices.SortFunc` / `slices.Contains` | 36 处 | 小（谁改到谁换） |
| 6 | **测试面** | 34 处 `time.Sleep` 做时序等待（mau 31 / host 3）；语料测试长期 SKIP（见事实 3） | `testing/synctest`（1.24）+ `t.Context()`；语料路径需先定「源放哪」（与 G18 同一决策） | — | 小-中 |

---

## 三、已判否

| 项 | 结论 | 依据 |
|:--|:--|:--|
| 手写 JSON 转义 → `encoding/json` | **不做**。保留手写 | 口径不可控：`encoding/json` 非 ASCII 直出、float 用最短往返（可能科学计数法）、`[]string` 不复用逐元素 `jsonString`。且回执层严格转义在双层链路（事实 2）下会退化成「LLM 看转义序列」 |

---

## 四、取回与再评估

- 试改内容：`git stash list` → `go-upgrade-rollback`（`callBrick` panic 隔离 + `TestBrickPanicIsolated` 57 行 + `Runner` 平行数组合并 + `args.go` 注释与键安全化）
- 取回：`git stash pop` · 丢弃：`git stash drop`
- 再评估前提：先落事实 2 的口径（A/B/C），再决定候选 1 与 5 是否同轮做——两者都碰 `runtime` 包
