# Mau-Go S1 侦察补漏 —— 语料↔积木精确映射（§1）/ MauGeneratorV3 规模（§2）

> 建立：2026-11-12 · 线：委托台第 1 轮 L1 线
> 基线：`mau:` 仓库（`mau:Bricks/index.json` 生成时刻 `2026-11-12T03:40:09Z`，mtime 2026-11-12 11:40:09）
> 定位：S1.6（§1）+ S1.7（§2）的实测读数与清单。只放事实与复跑命令，不复述设计理由。
> 口径（全篇）：
> - 行数一律 `find /c /v "" <真实路径>` 的读数——**非空行数，空行不计**（同一命令复跑读数一致）。
> - 积木名一律取自 `mau:Bricks/index.json` 的 `name` 字段；语料侧引用名与 index 名逐字一致才计入。
> - 计数口径：**unique**（同一积木在同组出现多次只计一次）。

---

## §1 语料 ↔ 积木精确映射（S1.6）

### 1.1 方法与口径

| 项 | 做法 |
|:--|:--|
| 语料侧 | 20 组 `mau:corpus/ch4/<组>/<组>.mau` 逐组实读全文（非抽样、非按目录名推断） |
| 引用识别 | 动作位（`→` 之后）引号内的 `a.b` 形态标识符，如 `'text.read'[@rdArgs]`。状态名（`'Idle'`）、导线名（`'T_ReadClaim'`）、盒名（`@rdArgs`）无点号，自然排除 |
| index 侧 | `mau:Bricks/index.json` 共 **125** 条（`find /c "BRIK-" mau:Bricks/index.json` → 125） |
| 命中判据 | 语料引用名 ∈ index `name` 集合；反向差异见 §1.4 |

### 1.2 通用核（20 组共用，5 件）

| 积木 | 语料里的角色 |
|:--|:--|
| `self.desc` | 状态自述（初始化 / 执行中 / 空闲待命） |
| `oa.is_open` | 传感器探测（纯只读判断 → bool 落盒） |
| `oa.claim_next_simple` | 原子接单 |
| `oa.get_str` | 读载荷 args |
| `oa.complete_str` | 回执（成功 / 失败两路） |

抽验：AudioCat 全文 55 行逐行核对，5 件均在动作位出现（行 17/20/21/26/29/35/38/43/46/49/52/55）。

### 1.3 20 组 × 积木清单

| # | 组 | `.mau` 文件 | 非空行 | 组特有积木（unique） | 特有 | 合计（＋通用核 5） |
|:--:|:--|:--|--:|:--|--:|--:|
| 1 | AudioCat | `audio_cat.mau` | 55 | `audio.play`、`audio.speak` | 2 | 7 |
| 2 | BrowserCat | `browser_cat.mau` | 113 | `browser.open`、`browser.read`、`browser.eval`、`browser.shot`、`browser.tabs`、`browser.headful` | 6 | 11 |
| 3 | ConfigCat | `config_cat.mau` | 75 | `config.bridge`、`catcfg.bridge` | 2 | 7 |
| 4 | CsCat | `cs_cat.mau` | 136 | `csharp.bridge` | 1 | 6 |
| 5 | DbAdmin | `db_admin.mau` | 38 | `dbAdmin.libs`、`dbAdmin.create`、`dbAdmin.drop` | 3 | 8 |
| 6 | DbCat | `db_cat.mau` | 58 | `db.put`、`db.query`、`db.get`、`db.update`、`db.delete` | 5 | 10 |
| 7 | FileCat | `file_cat.mau` | 124 | `file.tree`、`file.find`、`file.move`、`file.delete`、`file.copy`、`file.version`、`file.stat`、`file.hash`、`file.diff` | 9 | 14 |
| 8 | GitCat | `git_cat.mau` | 135 | `git.status`、`git.log`、`git.diff`、`git.show`、`git.commit`、`git.push` | 6 | 11 |
| 9 | GroupCat | `group_cat.mau` | 85 | `group.info`、`group.inbox`、`group.say`、`group.send`、`group.new_session`、`group.catinfo`、`group.catcfg_get`、`group.catcfg_set` | 8 | 13 |
| 10 | LLMSimpleRequest | `llm_simple_request.mau` | 41 | `llm.simple_request`、`llm.simple_ready`、`llm.simple_read` | 3 | 8 |
| 11 | Majordomo | `majordomo_cat.mau` | 119 | `host.restart`、`host.command`、`host.catinfo` | 3 | 8 |
| 12 | MauCat | `mau_cat.mau` | 93 | `mau.verify`、`mau.gen`、`mau.proj`、`mau.setup` | 4 | 9 |
| 13 | ProcCat | `proc_cat.mau` | 105 | `proc.run`、`proc.start`、`proc.stop`、`proc.list` | 4 | 9 |
| 14 | PsCat | `ps_cat.mau` | 62 | `ps.exec` | 1 | 6 |
| 15 | PyCat | `py_cat.mau` | 83 | `py.run`、`py.list`、`py.service` | 3 | 8 |
| 16 | TempToolCat | `temp_tool_cat.mau` | 61 | `temp.keys`、`temp.exec` | 2 | 7 |
| 17 | TextCat | `text_cat.mau` | 129 | `text.read`、`text.write`、`text.append`、`text.replace`、`text.read_lines`、`text.read_between`、`text.grep` | 7 | 12 |
| 18 | TsCat | `ts_cat.mau` | 58 | `ts.check`、`ts.list`、`ts.read`、`ts.findRef`、`ts.patch` | 5 | 10 |
| 19 | VisionCat | `vision_cat.mau` | 63 | `vision.analyze`、`image.inject` | 2 | 7 |
| 20 | WebCat | `web_cat.mau` | 151 | `web.search`、`web.fetch`、`web.fetch.jobs`、`web.ds.search`、`http.get`、`http.post`、`http.probe` | 7 | 12 |
| — | **合计** | 20 个文件 | **1784** | **组特有 83 个**（去重后全集合 **88** 个＝83＋通用核 5） | **83** | — |

### 1.4 总账与差异行

| 项 | 数 | 判据 |
|:--|--:|:--|
| index 契约总数 | **125** | `find /c "BRIK-" mau:Bricks/index.json` → 125 |
| 语料实际引用（unique） | **88** | 20 组清单去重：组特有 83 ＋ 通用核 5 |
| 差集：index 有、语料未引用 | **37** | 125 − 88 |

**37 条未引用清单（按类目）**

| 类目 | 条 | 名字 |
|:--|--:|:--|
| TOOLS（工具定义组） | 20 | `tools.textcat`、`tools.maucat`、`tools.cscat`、`tools.configcat`、`tools.visioncat`、`tools.temptoolcat`、`tools.pscat`、`tools.majordomocat`、`tools.filecat`、`tools.browsercat`、`tools.webcat`、`tools.llmsimplerequest`、`tools.audiocat`、`tools.pycat`、`tools.dbcat`、`tools.dbadmin`、`tools.tscat`、`tools.groupcat`、`tools.proccat`、`tools.gitcat` |
| OA 增量面 | 5 | `oa.post`、`oa.set_str`、`oa.is_closed`、`oa.is_done`、`oa.get_result_str` |
| LLM 增量面 | 5 | `llm.stream`、`llm.chunk_ready`、`llm.done_ready`、`llm.reply_read`、`llm.chunk_read` |
| DATA | 2 | `data.box_set_str`、`data.box_get_str` |
| PROBE（测试件） | 3 | `probe.source`、`probe.sink`、`probe.sink_slow` |
| SELF 增量 | 1 | `self.desc.add` |
| LOG | 1 | `log.write`（**仅注释出现**，见下） |

**两条口径注记**

1. `log.write` 在语料文本里出现 2 次，均在注释行而非动作位：`mau:corpus/ch4/FileCat/file_cat.mau:10` 与 `mau:corpus/ch4/TextCat/text_cat.mau:10`（原文「…＋ log.write TOOL 类别 ＋ trace.timeout / trace.late_discard」）。按动作位口径计入「未引用」。
2. `tools.*` 20 条是工具定义组（`return:"String"`、`inputs:[]`，实现为 `GetToolsJson`），由宿主 toolpool 读源码常量供给，不经语料动作位——它们在契约索引里存在但语料不引用属预期结构，不是缺口。

**反向核对（语料有、index 无）：0 条。** 抽验 29 个名字在 index 全文命中：`csharp.bridge` / `config.bridge` / `catcfg.bridge` / `host.restart` / `host.catinfo` / `host.command` / `image.inject` / `ts.findRef` / `ts.check` / `web.ds.search` / `web.fetch.jobs` / `http.probe` / `mau.verify` / `mau.gen` / `mau.proj` / `mau.setup` / `oa.is_open` / `oa.claim_next_simple` / `oa.get_str` / `oa.complete_str` / `self.desc` / `self.desc.add` / `group.say` / `group.new_session` / `group.catcfg_get` / `py.service` / `temp.keys` / `proc.run` / `vision.analyze` / `llm.simple_request`（13＋16＋1 条 findstr 输出逐行核对，全部命中，无缺失）。

### 1.5 复跑命令

```powershell
# index 契约总数（期望 125）
cmd /s /c 'find /c "BRIK-" D:\Mau\Mau\Bricks\index.json'

# 某组语料非空行数（示例：FileCat，期望 124）
cmd /s /c 'find /c /v "" D:\Mau\Mau\corpus\ch4\FileCat\file_cat.mau'

# 37 条未引用名单中 16 条（不含 tools.*）在语料中零出现（期望 exit 1、无输出）
cmd /s /c 'findstr /s /m /c:"data.box_set_str" /c:"oa.post" /c:"llm.stream" /c:"self.desc.add" /c:"probe.source" D:\Mau\Mau\corpus\ch4\*.mau'

# log.write 只在注释出现（期望 2 行，均 :10 注释行）
cmd /s /c 'findstr /s /n /c:"log.write" D:\Mau\Mau\corpus\ch4\*.mau'

# tools.* 20 条在语料零出现（期望 exit 1、无输出）
cmd /s /c 'findstr /s /m /c:"tools.textcat" /c:"tools.gitcat" D:\Mau\Mau\corpus\ch4\*.mau'
```

> 注：`cmd /s /c '...'` 是 PowerShell 5.1 传空串参数给 `find` 的唯一可行形态——直调 `find /c /v "" <path>` 会被 PS 吞掉空串参数（`FIND: Parameter format not correct`）。

---

## §2 MauGeneratorV3 规模（S1.7）

### 2.1 方法与口径

| 项 | 值 |
|:--|:--|
| 目录 | `mau:Mau/Mau.Translator/`——真实路径 `D:\Mau\Mau\Mau\Mau.Translator\`（受控根 `mau` = `D:\Mau\Mau`，寻址 `mau:Mau/...` 再叠一层 `Mau/`） |
| 分部 | `partial class MauGeneratorV3` 跨 5 个文件（`.cs` / `.Bricks.cs` / `.Names.cs` / `.Tick.cs` / `.Wires.cs`） |
| 分母 | 同目录 16 个 `.cs` 文件（另有 `Mau.Translator.csproj`，非 .cs 不计） |
| 行数口径 | `find /c /v ""`——非空行数，空行不计 |

### 2.2 5 分部行数

| # | 文件 | 非空行 |
|:--:|:--|--:|
| 1 | `MauGeneratorV3.Wires.cs` | 472 |
| 2 | `MauGeneratorV3.Tick.cs` | 436 |
| 3 | `MauGeneratorV3.cs` | 320 |
| 4 | `MauGeneratorV3.Bricks.cs` | 307 |
| 5 | `MauGeneratorV3.Names.cs` | 217 |
| — | **5 分部合计** | **1752** |

### 2.3 占比分母（Mau.Translator 全部 16 个 `.cs`）

| # | 文件 | 非空行 | 是否分部 |
|:--:|:--|--:|:--:|
| 1 | `MauGeneratorV3.Wires.cs` | 472 | ✅ |
| 2 | `MauGeneratorV3.Tick.cs` | 436 | ✅ |
| 3 | `MauGeneratorV3.cs` | 320 | ✅ |
| 4 | `MauGeneratorV3.Bricks.cs` | 307 | ✅ |
| 5 | `MauGeneratorV3.Names.cs` | 217 | ✅ |
| 6 | `MauParserV3.cs` | 727 | — |
| 7 | `MauAnalyzerV3.cs` | 657 | — |
| 8 | `MauValidatorV3.cs` | 552 | — |
| 9 | `MauLexerV3.cs` | 376 | — |
| 10 | `MauDocV3.cs` | 301 | — |
| 11 | `BrickIndex.cs` | 273 | — |
| 12 | `MauCompilerV3.cs` | 211 | — |
| 13 | `DefaultAppearance.cs` | 124 | — |
| 14 | `TokenFlowV3.cs` | 65 | — |
| 15 | `TokenV3.cs` | 46 | — |
| 16 | `MauDiagnostic.cs` | 36 | — |
| — | **16 文件合计** | **5120** | — |

### 2.4 占比读数

| 项 | 值 | 算式 |
|:--|--:|:--|
| 5 分部合计 | **1752** | 472＋436＋320＋307＋217 |
| `Mau.Translator` 全量 | **5120** | 16 个 `.cs` 非空行合计 |
| 分部占全量 | **34.22%** | 1752 / 5120 = 0.3421875 |

### 2.5 复跑命令

```powershell
# 单个文件非空行数（示例：Wires，期望 472）
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\MauGeneratorV3.Wires.cs'

# 5 分部其余四件（期望 .cs 320 / .Bricks 307 / .Names 217 / .Tick 436）
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\MauGeneratorV3.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\MauGeneratorV3.Bricks.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\MauGeneratorV3.Names.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\MauGeneratorV3.Tick.cs'

# 分母其余 11 个文件（期望 727 / 657 / 552 / 376 / 301 / 273 / 211 / 124 / 65 / 46 / 36）
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\MauParserV3.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\MauAnalyzerV3.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\MauValidatorV3.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\MauLexerV3.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\MauDocV3.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\BrickIndex.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\MauCompilerV3.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\DefaultAppearance.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\TokenFlowV3.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\TokenV3.cs'
cmd /s /c 'find /c /v "" D:\Mau\Mau\Mau\Mau.Translator\MauDiagnostic.cs'
```

> 读数注记：本轮 `BrickIndex.cs` 首次并发跑时回执丢失（exit 0、0 行输出），单独重跑得 273——批量并发跑 `find` 时若某个回执为空，重跑该条即可，不要当 0。

---

## §3 本轮读数汇总（一屏）

| 面 | 读数 | 判据 |
|:--|--:|:--|
| 语料组数 | 20 | `mau:corpus/ch4/` 下 20 个 `.mau` |
| 语料非空行合计 | 1784 | 20 组 `find /c /v ""` 求和 |
| index 契约 | 125 | `find /c "BRIK-" index.json` |
| 语料引用（unique） | 88 | 83 组特有 ＋ 5 通用核 |
| 未引用（index 侧差集） | 37 | 125 − 88（含 tools.* 20 条结构项） |
| 反向缺失（语料有 index 无） | 0 | 29 名抽验全中 |
| Generator 5 分部 | 1752 | 472＋436＋320＋307＋217 |
| Mau.Translator 全量 | 5120 | 16 个 `.cs` 合计 |
| 分部占比 | 34.22% | 1752 / 5120 |

---

## §4 第 2 轮复核（2026-11-12 · 委托台 r2 · L1 线）

> 口径：本节只记「复跑了什么、读数与 §1/§2 是否一致」，**不复制 §1.3 / §2.3 的数值**（同一事实只写一处）。
> 结论：**零差异**——§1 / §2 数据保持原样，未修订。

### 4.1 AC-001（§1 结构 + index 基准）

| 复核项 | 本轮读数 | 命令 / 判据 | 一致 |
|:--|:--|:--|:--:|
| §1.3 表体行数 | **20 行**（文件行 40–59，序号 1…20 连续；合计行不计） | `text-read_lines` 直读 | ✅ |
| 语料文件名单 | **20 个 `.mau`**，路径与表内文件名逐字相同 | `file-find dir=D:\Mau\Mau\corpus\ch4 pattern=*.mau` | ✅ |
| 20 组非空行 | 20 次逐文件单跑，**20/20 与 §1.3 同值**（求和 1784） | `cmd /s /c 'find /c /v "" <组>.mau'` ×20 | ✅ |
| index 契约总数 | **125** | `find /c "BRIK-" D:\Mau\Mau\Bricks\index.json` | ✅ |
| 差异行总数 | **37** = 125 − 88（83 组特有 + 5 通用核） | 算术 + 下列 7 类分类计数 | ✅ |

**37 条差异行的分类交叉核**（按 id 前缀单跑，与 §1.4 分类逐项对上）

| 类目 | id 前缀 | index 条数 | 语料使用 | 未引用 | §1.4 记 |
|:--|:--|--:|--:|--:|--:|
| TOOLS（工具定义组） | `BRIK-TOOLS-` | 20 | 0 | 20 | 20 ✅ |
| OA 增量面 | `BRIK-OA-` | 9 | 4 | 5 | 5 ✅ |
| LLM 增量面 | `BRIK-LLM-` | 8 | 3 | 5 | 5 ✅ |
| DATA | `BRIK-DATA-` | 2 | 0 | 2 | 2 ✅ |
| PROBE（`probe.*`，id 前缀是 `BRIK-TEST-`，类目 `TEST`） | `BRIK-TEST-` | 3 | 0 | 3 | 3 ✅ |
| SELF 增量 | `BRIK-SELF-` | 2 | 1 | 1 | 1 ✅ |
| LOG | `BRIK-LOG-` | 1 | 0 | 1 | 1 ✅ |
| **合计** | — | 45 | 8 | **37** | **37** ✅ |

**语料侧零出现复核**（findstr 全语料 `/s`，exit 1 + 无输出 = 零命中）

```powershell
# 16 条非 tools 差异名 → exit 1 · 0 行（data.box_set_str/data.box_get_str/oa.post/oa.set_str/
#   oa.is_closed/oa.is_done/oa.get_result_str/llm.stream/llm.chunk_ready/llm.done_ready/
#   llm.reply_read/llm.chunk_read/self.desc.add/probe.source/probe.sink/probe.sink_slow）
cmd /s /c 'findstr /s /m /c:"data.box_set_str" ... D:\Mau\Mau\corpus\ch4\*.mau'
# 20 条 tools.* → exit 1 · 0 行
cmd /s /c 'findstr /s /m /c:"tools.textcat" ... /c:"tools.gitcat" D:\Mau\Mau\corpus\ch4\*.mau'
# log.write → 2 行，均在 :10 注释行
cmd /s /c 'findstr /s /n /c:"log.write" D:\Mau\Mau\corpus\ch4\*.mau'
```

> 注：上面三条的 `...` 是省略写法（参数名的完整清单见 §1.4 分类表）；本轮实跑用的是完整参数串（16 个 `/c:` 或 20 个 `/c:` 串成一命令）。

**单组逐行核对（WebCat）**：`text-read` 全文 152 行 → 动作位标识符恰为 7 个组特有（`web.search` / `web.fetch` / `web.fetch.jobs` / `web.ds.search` / `http.get` / `http.post` / `http.probe`）+ 5 个通用核，与 §1.3 第 20 行一致；`find /c /v ""` = **151**（152 − 1 空行），与表内读数一致。

### 4.2 AC-002（§2 复跑）

| 文件 | 本轮读数 | 与 §2 记 |
|:--|--:|:--:|
| `MauGeneratorV3.Wires.cs` | 472 | ✅ |
| `MauGeneratorV3.Tick.cs` | 436 | ✅ |
| `BrickIndex.cs`（上轮并发丢回执那条） | 273（本轮单独跑，一次到位） | ✅ |

分母名单：`file-find` → **16 个 `.cs`**，与 §2.3 表逐字相同。

### 4.3 AC-003（门禁四项，cwd = `D:\Mau\WorkSpace\CatHome5`）

| 门 | 结果 |
|:--|:--|
| `go build ./...` | exit 0 · 1.2 s |
| `go vet ./...` | exit 0 · 0.59 s |
| `gofmt -l .` | **输出空** · exit 0 |
| `go test ./... -count=1` | exit 0 · **30 行 = 18 `ok` + 12 `[no test files]`** · 9.3 s |

### 4.4 本轮新增命令级坑

- `cmd /s /c 'find /c /v "" D:\Mau\Mau\corpus\ch4\*\*.mau'`（**目录层带通配**）→ 30 s 超时、无输出（exit -1）。逐文件单跑即可；不要试图一条命令出 20 组读数。
- `gofmt` **没有 `-C`**（传则 `flag provided but not defined: -C`）——换目录用 `cwd` 参数，不传 flag。
