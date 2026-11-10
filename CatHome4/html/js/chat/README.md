# CH4 对话页前端（chat）

> 定位：对话页前端的**唯一实现处**——契约 **chat 快照协议 v2**（`design-ch4-protocol.md` §十二）的前端侧。
> 结构口径：**素材（渲染）与主干（时序）分离**——素材零全局状态、零网络；挂载 / 排序 / 跟随 / 指令归主干。
> 加载入口：`../chat.html` 的脚本清单（顺序即依赖）；本目录不自行启动。

---

## 一、目录结构

| 目录 | 内容 |
|:--|:--|
| `lib/` | 渲染共用件（9 件）——DOM 构造 `el`/`elText` / 格式化 `fmtCount`（**统计数字统一入口**：两位小数 + K/M 进位，M 为最大单位）·`fmtMs` / MD 渲染 / 图片包裹 / 按压判据 / 工具骨架 / 兜底件 `fallback`（未识别 type · 渲染异常——两区共用） / **命令解码 `cmd`（PS 意图）** |
| `blocks/` | 持久块型 8 类（persist——`gap_text` 与 `text` 视觉同形，收包即归一为 `text`，见 §二）+ 流式件 2 个（live 的 `replysse` / `thinksse`；`toolrun` 复用持久区工具卡渲染件） |
| `state/` | **state 段投影——各投影件 + 分发表**（A194 完成）：`status.js`（六态状态条——容器唯一写者）· `info.js`（顶栏信息位）· `meta.js`（会话标识——浏览器标题 + 左上角 `#chatTitle`）· `note.js` · `delay.js` · `pending.js`（插话队列——内核 `_pending` 队列投影，含 QQ / 指令 / 定时等非前端来源）· `conn.js`；`registry.js` 的 `STATE_DECL`（投影件 → 消费字段 / 输出面 / 入口的唯一映射处）+ `stateProjectAll()` 整段分发 |
| `fx/` | **独立功能——每功能一件 + 声明表**（A176 归位）：`pet`（桌宠）· `scroll`（滚动带）· `cmd-intent`（命令解码显示）· `controls`（按钮态）· `live-stats`（流式统计行——开关上方 line / char / spd）；登记在 `fx/registry.js` 的 `FX_FEATURES`（名称 / 输入源 / 输出面 / 启动入口），启动由 `fxBoot()` 统一执行 |
| 主干 | `registry.js`（**块声明表 + 映射表**——A178 起不兼实现）· `persist.js`（持久区）· `stream.js`（**流式呈现机制**——前缀增量 + 打字机 + 跟随 + 段期光标，整套效果单点承载）· `live.js`（临时区）· `state.js`（状态投影——**整段覆盖 + 按分发表分发薄层**）· `main.js`（收包入口 + 公共小件：信息位单点 `chatInfoSet`——**state 段专属**）· `input.js`（用户出口） |
| 面板与侧翼 | `note.js`（Note 面板——气泡 + 弹层）· `delay.js`（定时面板——列表 / 倒计时 / 改时刻）· `paste.js`（图片粘贴上传 + 待发区） |

---

## 二、块型 ↔ 文件（契约 §12.5）

**形态**列 = 该 type 的行形态（气泡 `.chat-bubble` / 朴素件 `.chat-plain`）。
🔴 **块声明的唯一处 = `registry.js` 的块声明表 `BLOCK_DECL`**（每 type 一行：`form` 形态 / `row` 行语义类 / `tick` 滚动带刻度角色 / `body` 块体语义类）——渲染函数经 `formClass(type)` 取形态、`bodyClass(type)` 取块体（形态 + 语义）、`blockRow(type)` 取行容器（含 `data-type`），滚动带经 `data-type` 查 `tickRole(type)`；均不自持类名字符串。改形态或语义 = 改表一行（A179 收口）。本表仅作可读快照。

### persist 类——条目 `{ type, ts, msgIndex, round, payload }`

| type | 文件 | 渲染函数 | 形态 |
|:--|:--|:--|:--:|
| `user` | `blocks/user.js` | `buildUserBlock(payload)` | 🫧 气泡 |
| `systemautomessage` | `blocks/systemautomessage.js` | `buildSystemAutoMessageBlock(payload)` | 🫧 气泡 |
| `text` | `blocks/text.js` | `buildTextBlock(payload, item)` | 🫧 气泡 |
| `reason` | `blocks/reason.js` | `buildReasonBlock(payload)` | 🫧 气泡 |
| `toolcard` | `blocks/toolcard.js` | `buildToolBlock(payload)` | 🫧 气泡 |
| `retry` | `blocks/retry.js` | `buildRetryBlock(payload)` | 🫧 气泡 |
| `error` | `blocks/error.js` | `buildErrorBlock(payload)` | 🫧 气泡 |
| `inject_report` | `blocks/inject-report.js` | `buildInjectReportBlock(payload)` | 🫧 气泡 |
| `roundsum` | `blocks/roundsum.js` | `buildRoundSumBlock(payload)` | 🫧 气泡 |

### live 类——段 `{ type, context }`（A196 状态投影——两个字符串，整段覆盖）

| type | 渲染 | 形态 |
|:--|:--|:--:|
| `replysse` | `liveReplySse({ text: context })`（`blocks/reply-sse.js`）——该流当前全文；**挂流式呈现机制**（`stream.js`——前缀增量 + 打字机 + 面板跟随 + 段期光标） | ▬ 朴素 |
| `thinksse` | `liveThinkSse({ text: context })`（`blocks/think-sse.js`）——该流当前全文；**挂流式呈现机制**（同上；写入点 = 块体内首个文本节点，§12.5 呈现口径） | ▬ 朴素 |
| `toolrun` | `context` = 未完成工具卡**数组 JSON**，由 `live.js` 逐卡调 `buildToolBlock(card, null, 'toolrun')`（第三参 = 行身份；复用 `blocks/toolcard.js`，两区同源） | ▬ 朴素 |
| `empty` | 不产元素——面板空态（链路正常、内容为空） | — |

**形态契约（2026-10-03 立 · 2026-10-05 改判 · 莎定）**——**persist 八类全部气泡**（`.chat-bubble`：底 + 描边 + 圆角 + 内边距）；**live 三件朴素**（`.chat-plain`——面板自身即泡，内部件不再套壳）。类别区分由**描边色**承担（色相 = 语义，与状态条 / 滚动带刻度同源）；底色四档不新增令牌：内容类 `--ch-bg-panel` · 过程类 `--ch-bg-sunken`（reason）· 系统类 `--ch-bg-faint`（retry / error）· 注入报告 `--ch-note-bg`；**`inject_report` 与 `roundsum` 描边同为淡紫**（`--ch-state-all`）。
间隙文本（后端 type `gap_text`）与 `text` 视觉同形——前端收包即归一为 `text`（`persist.js::TYPE_ALIAS`）；后端保留该 type，供 QQ `/last` 与会话存档区分。

**节点操作条（P6b · 2026-10-06 复归）**——`text` 块底部按钮（`⟲` 回滚：`session.rollback <msgIndex>`；`⧉` 分支已随 A326 删除）；入口判据 = 块级 `msgIndex ≥ 0`（独立块 / 旧块不挂）；指令走指令总线 `postCommand`（出口面），投递回执走桌宠气泡 `warn`（通知面）。渲染件**第二参 `item`** = 整条条目——块级字段取用口（挂载入口 `persist.js::persistRender`）。

---

## 三、主干职责（对接契约）

| 件 | 职责 | 契约依据 |
|:--|:--|:--|
| `main.js` | 单一应用入口——一个 SSE 流收包 → `chatOnFrame` → 三段各归各位（**无事件分派 / 无键算术 / 无配对 / 无排序**）；滚动跟随；公共小件（信息位单点 `chatInfoSet`——**富文本分片载荷**，2026-10-06） | §12.1 / §12.3 |
| `state.js` | state 段**整段覆盖 + 按表分发薄层**（分发表见下行） | §12.2 ① |
| `state/registry.js` | state 段分发表 `STATE_DECL`——投影件 → 消费字段 / 输出面 / 入口的**唯一映射处**（字段面 = 表内 `fields` 并集须**覆盖**后端清单，A201）；无入口时出声、单件异常隔离；新增字段 = 加一件 + 表加一行 | §12.2 ① |
| `persist.js` | 持久区——`full` 清区重绘 / `append` 逐条追加；不判流式结束、不判换手 | §12.2 ② / §6.1-D |
| `stream.js` | **流式呈现机制**（整套效果的单点承载处）——前缀增量 + 打字机走步（标称 60 步/秒 · 窗口 220ms · 走步容差）+ 容器贴底跟随 + 段期光标；挂载面 `STREAM_TYPES`（thinksse / replysse） | §12.5 呈现口径 |
| `live.js` | 临时区——挂载件交 **stream.js**，其余件整体替换（清区重绘）；live 段 type 写 `#chatinput` 的 `data-live`（态色单一色源） | §12.2 ③ / §12.5 |
| `registry.js` | type → 渲染函数映射 + **块声明表 `BLOCK_DECL`**（形态 / 行语义类 / 刻度角色 / 块体语义）+ 六态元信息 `RUN_PHASES`——**前端唯一扩展点**：新增块型 = 后端加 type + 本表加一行（渲染函数 + 声明）+ 一个渲染函数；类名一律经 `formClass(type)` / `bodyClass(type)` / `blockRow(type)` 取用 | §12.5 |
| `input.js` | 用户出口——发送 / 停止 / 继续 / 新会话 / 刷新 / 临时区切换（输入框 ↔ 面板**同区域互斥**，回车发送自动切回输入态） | §12.2 ③ |

**帧形**（后端 `ViewBus`）：全量 `{v:2, state, persist:{mode:"full",items}, live:{type,context}}` · 追加 `{persist:{mode:"append",items}}` · 变化增量 `{state:{…整段…}}` / `{live:{type,context}}`；无事发生零字节。

---

## 四、边界

- **不含**：面板面（`../app.js` · `../panel*.js` · `../index.html`）· 两页共用件（`../ui-common.js`）
- **独立功能归位 `fx/`**（A176——独立功能面）：桌宠 / 滚动带 / 插话队列 / 命令解码显示 / 按钮态各一件 + 声明表 `fx/registry.js`（登记 = 契约 §12「独立功能面」节）；面板与出口面（`note.js` / `delay.js` / `paste.js`）留主干层；转移清单与要点 → `design-ch4-frontend-rebuild_log.md` 阶段 3 §侧翼件清单
- **显示面归属**（契约 §12.7 · A179 收尾；2026-10-06 A201 改写）：顶栏信息位 `#chatInfo` = **state 段专属**（后端必要信息读数——前文条数 / 长度 / 字符数 / 会话级消耗与命中率）；左上角 `#chatTitle` 与浏览器标题 = `meta.displayName`（`state/meta.js`）；前端提示与告警 → **桌宠气泡** `chatPetSay`（通知面，TTL 自动消失），两者互不覆盖
- **测试**：`../tests/`（vitest；对话面用例随重构销毁，面板面用例保留）
