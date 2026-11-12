package toolpool

// BuiltinToolsJSON 返回宿主内置工具定义 JSON——与 C# Program.BuildBuiltinToolsJson 逐字同构。
//
// 内置 11 件：Note / time / random / info / host-reload / host-flows / pack / sleep /
// timer / timeback-start / timeback-back。格式与工具组自曝同构（{"group":"","tools":[...]}），
// 同一解析器，统一工具池。内容面（description / parameters）必须与 C# 源码一致——
// 请求等价（判据 1）的 tools[] 字节来源。
func BuiltinToolsJSON() string {
	return `{"group":"","tools":[` +
		`{"name":"Note","description":"轻量任务追踪器（内存存储，会话关闭即消失）。只记可自主执行的步骤——等确认 / 等回应的闸门项不写入（引擎对未完成项强制拉起）。无参数=推进到下一条；action='set'+content=[\"任务1\",\"任务2\"]=写入新计划（已有未完成需force=true强制覆盖）。返回当前第X/Y条 已完成Z 待完成W 任务目标：... 最后一条时追加提示（已是最后一条需求，完成后可结束本轮）。全部完成后自动清空。剩余1条时引擎不自动拉起。","parameters":{"type":"object","properties":{"action":{"type":"string","description":"set=写入新计划，不传=推进"},"content":{"type":"array","items":{"type":"string"},"description":"action=set时必填——任务条目数组（每元素一条）"},"force":{"type":"boolean","description":"覆盖已有未完成计划时传true"}},"required":[]}}` +
		`,` +
		`{"name":"time","description":"当前系统日期时间（yyyy-MM-dd HH:mm:ss）——会话内直执，无需 OA","parameters":{"type":"object","properties":{}}}` +
		`,` +
		`{"name":"random","description":"生成 [min, max) 范围内的随机整数（min 含下限，max 不含上限，要求 min < max）——会话内直执，无需 OA","parameters":{"type":"object","properties":{"min":{"type":"integer","description":"随机范围下限（含）"},"max":{"type":"integer","description":"随机范围上限（不含）"}},"required":["min","max"]}}` +
		`,` +
		`{"name":"info","description":"本会话环境自省——返回分类 JSON 块：version（版本 + 编译时刻）/ time（当前时间）/ llm（本猫生效端点）/ endpoint（本地端点：对话页 + 管理面板）/ roots（可见受控根 id/writable/note/path——绝对路径）/ tokens（前文长度 + 最近前文变动时刻）/ packs（挂载包）/ tools_drift（漂移——added 附用法摘要 name/desc/args（*=必填）· removed 给名字；仅不一致时输出）/ tools_defect（工具组定义缺陷——group/stage/reason；仅非空时输出）/ timeback（上下文作用域——active 活跃作用域 · recent 归档尾部 5 条 · archive 写面可用性；无数据时不出该键）（agent 的眼睛；R1.2）","parameters":{"type":"object","properties":{}}}` +
		`,` +
		`{"name":"host-reload","description":"热重载语料 dll（宿主级）——在 mau-proj 编译成功后单独调用（建议下一轮）；事务三段式：加载失败保留旧版本；cat=已加载 Flow 注册名（host-flows 查当前清单）","parameters":{"type":"object","properties":{"cat":{"type":"string","description":"Flow 注册名（host-flows 查当前全部）"}},"required":["cat"]}}` +
		`,` +
		`{"name":"host-flows","description":"查看当前运行 Flow 现状（宿主级）——Registry 动态面：每个已加载 Flow 的 Id/Name/Kind/句柄状态/dll 路径；新增/重载后查询目标清单用","parameters":{"type":"object","properties":{}}}` +
		`,` +
		`{"name":"pack","description":"加载包注入——按 key 注入预设文件包（池 packs.json 定义 + 本猫挂载授权），一次调用完成加载；返回逐件明细（件数/字符数/失败原因）。收工/蒸馏/审查等跨身份动作的前置加载入口。","parameters":{"type":"object","properties":{"key":{"type":"string","description":"包 key（info 可见本猫已挂载包）"}},"required":["key"]}}` +
		`,` +
		`{"name":"sleep","description":"登记定时唤醒（时/分/秒 · 合计 1..3600 秒）——等待语义：本轮正常继续（不挂起），到点由宿主自动注入一条唤醒消息；主干被任何其他输入提前启动时本等待即作废（自动销毁并告知）。适合「等你回话 / 等外部条件」类需求。","parameters":{"type":"object","properties":{"hours":{"type":"integer","description":"小时（可缺省=0）"},"minutes":{"type":"integer","description":"分钟（可缺省=0）"},"seconds":{"type":"integer","description":"秒（可缺省=0）"}},"required":[]}}` +
		`,` +
		`{"name":"timer","description":"登记延迟指令注入（content + 时/分/秒 · 合计 1..86400 秒）——排程语义：到点把 content 原样注入本会话（闹钟必响，不因主干启动而销毁）；loop=true 时触发后按「投递时刻 + 时长」重排（周期巡检）。本轮正常继续，且不影响本轮收尾语义。适合「长任务中途自查 / 周期巡检」。","parameters":{"type":"object","properties":{"content":{"type":"string","description":"到点注入的指令文本（必填）"},"hours":{"type":"integer","description":"小时（可缺省=0）"},"minutes":{"type":"integer","description":"分钟（可缺省=0）"},"seconds":{"type":"integer","description":"秒（可缺省=0）"},"loop":{"type":"boolean","description":"true=周期重排（可缺省=false）"}},"required":["content"]}}` +
		`,` +
		`{"name":"timeback-start","description":"上下文作用域·开锚——type 选域类型（决定域内可用工具白名单 / 回执骨架 / 资源预热），purpose 记用途；过程在作用域内膨胀，回收时用 timeback-back 回卷（膨胀过程从上下文销毁，只把 findings 带回主干——下一轮首条可见）。域类型枚举：browser_vision（浏览网页取证：browser-* + 基础读面，**不含识图**）· image_vision（看图取证：image-* + 基础读面——回执给 ASCII 图证 + 结论）· text_search（文本与文档检索：文本读面 + 文件结构面）· code_review（代码审查取证：基础读面 + cs 只读七件，不含跑测面）· code_write（代码实现：基础读面 + cs 全套十三件 + 文本写面 + mau-verify——读 / 改 / 编译 / 跑测一域内完成）。🔴 域内只放行本类型白名单——名单外调用被拒（ERR|TIMEBACK_PROFILE，需用则先 back 回主干，不在域内换类型）；系统信息类工具（info / host-flows）不进任何域；域内不做计划、不调 Note / sleep / timer。热重载 / 重启 / 部署不在域内（进程中断即失域）。🔴 开域判据：本环节预期串接 ≥3 次工具调用才收口——单次 / 两次即得答案留主干。v1 未闭合前禁止再次 start。","parameters":{"type":"object","properties":{"type":{"type":"string","description":"域类型（必填）——browser_vision | image_vision | text_search | code_review | code_write"},"purpose":{"type":"string","description":"用途标签（短）"}},"required":["type","purpose"]}}` +
		`,` +
		`{"name":"timeback-back","description":"上下文作用域·回卷回收——findings 即本次调用的返回值，回卷后作用域关闭（区间内容从上下文销毁）。findings 按 start 回执给出的骨架写（审查类给「成果位置」· 实现类给「变更 + 可复跑跑测」· 识图类给「ASCII 图证 + 结论」）——只写「在哪 / 改了什么 / 看到了什么」不写无证据的结论；没读到的、凭印象复述的一律不写。回执另附宿主**工具台账**（域内用过的每个工具：工具名 · 目标 · 成败——含只读与被拒）。回收时本调用的思考内容会被替换为占位句（原文留视图层）——防「回域断言」被后续轮次误当事实。域内**人工输入**（用户在域内插的话）不随域销毁——保留在前文（落回 start 结果之后、back 声明之前）；系统注入（域规范卡 / 状态自述 / 图片注入）仍随域删除。","parameters":{"type":"object","properties":{"findings":{"type":"string","description":"带回载荷（骨架：成果 / 未竟 / 卡点 / 失败——只写位置与简短描述，不写结论）"}},"required":["findings"]}}` +
		`]}`
}
