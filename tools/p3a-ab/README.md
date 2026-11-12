# p3a-ab — P3-a 实机对拍器（第一版）

> 用途：同一数据副本（`CatTemp/cat5-data`）起两侧实例——C# 基线（`MauOut/CatHome4.exe`）+ CAT5（`bin/cathome5.exe`）——抓取 8 类响应、归一化、逐条勾选。
> 边界：只写本仓；C# 侧只读执行；不触碰运行中的 CH4（8080/8081）。基线取法 = B（另起 C# 实例 + 独立端口），A 仅兜底。

## 用法

```powershell
# 构建
D:\Mau\WorkSpace\tools\go\bin\go.exe build -o bin\p3a-ab.exe ./tools/p3a-ab

# 起（port 0 = 9180 起自动选空闲；报告落 tools/p3a-ab/reports/<ts>/）
bin\p3a-ab.exe --data-root D:\Mau\WorkSpace\tools\CatTemp\cat5-data --html-root D:\Mau\MauOut\html
```

- `--spawn=false --go-base http://... --cs-base http://...`：只对拍已运行实例（未实测）
- `--keep`：对拍后保留进程（调试）
- 证据：`report.txt` / `summary.json` / `status.log` / `raw/<side>/` / `norm/` / `*.stdout.log` / `*.stderr.log`

## 起服约定（B 方案）

1. C# 基线：`CH4_DATA_ROOT=<dataRoot>` + 副本 `config/app.cfg` 的 `http.port=<csPort>`（工具写入，跑完恢复）；首选端口被占时从 stdout 解析实际端口
2. CAT5：`--data-root <dataRoot> --html-root <htmlRoot> --area dev --port <goPort>`
3. schema 模板：`<csExeDir>/config/schema.json.example` → `<goExeDir>/config/schema.json.example`（部署件形态；缺失会降级 `SCHEMA_TEMPLATE_MISSING`）
4. 两侧 `deps` 14 条收敛后才对拍（IO 探测后台单飞）
5. 收尾：两侧 `taskkill /T /F`；`app.cfg` 恢复

## 8 类检查

| 类 | 内容 |
|:--|:--|
| config | 44 项 × 9 字段同值同结构 |
| snapshot | 十二字段/顺序 / version / pid / frame / 段类型与计数 / boot / deps 归一化深比较 / res 形状 / `?logs=N` 替换 |
| logs | 七字段 / 上限 / cat 过滤 |
| static | 主页（BOM 与 `__V__` 归一）/ css / js / favicon 字节级 + MIME + Cache-Control |
| sse | 首帧四行 + data 十二字段 |
| 404 | 未注册条件路由 / 条件注册对照 / 缺失静态资源 |
| error | 非法/越界参数夹取 + POST 占位错误面 |
| runs | 三投影 + `startup.log` + 行格式 |

## 归一化清单

`pid` / `frame` / `probedAt` / `res.t` / `res.proc.*` / `res.gpu.*` / `res.loop.*` / `boot.sink` / `boot.reportPath` / `boot.degraded[].at` / 日志 `time` / `module`（CatHome4↔CatHome5 → `CatHome*`）/ 静态页 `?v=` 与裸版本串（`__V__` 替换产物）

## 状态语义

- `PASS` 一致 · `KNOWN` 已知有意差异（逐条在报告）· `DIFF` 非预期差异（需处置）· `FAIL` 抓取/结构失败 · `INFO` 信息项
- `summary.json` 的 `green` = 无 FAIL 且无 DIFF（进程退出码同口径）

## 首轮实测（2026-11-11）

`reports/20261111_ab3/report.txt`：PASS 72 · KNOWN 13 · DIFF 0 · FAIL 0 · INFO 6；config 44/44 × 9 字段全等。KNOWN 13 条即当前已知差异清单（P3-b/P3-d 缺口与 C# 数据面差异）。
