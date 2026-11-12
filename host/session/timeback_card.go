package session

import (
	"strconv"
	"strings"
)

// TimebackSuggestedLength 建议长度（前文条目）——五 type 统一值；建议非闸门。
const TimebackSuggestedLength = 100

// timebackLockedText 域内不可用摘要——锁定族 · 系统信息类 · 共同禁面。
const timebackLockedText = "Note · sleep · timer（域内锁定）｜系统信息类 info / host-flows｜" +
	"powershell / temp-exec / file-move·file-delete·file-copy / config-* / mau-gen·mau-proj·mau-setup / host-reload / restart-*"

// timebackBrowserGuideText 浏览器操作指引——仅 browser_vision 域追加。
const timebackBrowserGuideText = "⑥ 浏览器操作（本域专属）：\n" +
	"  · 寻路：open 只开页，不能点击/输入——翻页改 URL，或先 read(mode=links) 拿候选\n" +
	"  · 读页：read(mode=text|ax|links) 落盘；DOM 属性 / 内嵌数据 / img 直链一律用 eval 取\n" +
	"  · 截图：shot(full) 落盘返回路径——存 data:browser/<cat>/out/（tree 自 data:browser 下扫会漏 out，给全路径才有）\n" +
	"  · 页签：tabs 保留来过哪一页，可回退对比\n" +
	"  · 流程：开列表页（query 拼进 URL 直开）→ 取下一跳 → 逐个开详情（截图 / 取值）→ 回卷\n" +
	"  · 看图：本域**不含识图**——截图经 back 的「成果」带回路径，要「看见」另开 image_vision 域\n" +
	"  · 扒图：eval 取 img 直链 → 出域后 powershell 落盘 → 图片包裹回主干\n" +
	"  · 边界：原图直链常 403（CDN 缩略图可取）；未登录态不影响截图；网络可达性因机而异"

// timebackImageGuideText 看图操作指引——仅 image_vision 域追加。
const timebackImageGuideText = "⑦ 看图操作（本域专属）：\n" +
	"  · 分流：要「亲眼看」用 image-inject（原图进前文，随域回收）；只要文字（OCR / 逐字转录）用 image-analyze\n" +
	"  · 图证：findings 的 `图证` 用 ASCII 画判定所需区域——≤24 行 × ≤80 列，字符 + - | / \\ . # @\n" +
	"  · 只画结构：布局 / 位置 / 状态用 ASCII；颜色 / 字号 / 纹理写一行文字，别拿字符硬凑\n" +
	"  · 多图：一域内可连插多图对比，findings 每条对应一张，`证据位置` 写清是哪张的哪个区域\n" +
	"  · 落点：本地路径 / http(s) URL 都吃；截图路径直接来自上一域 `成果`（data:browser/<cat>/out/——给全路径）\n" +
	"  · 边界：单图 ≤32MiB；判断不了就说判断不了（不做模糊描述），需要更大信息量另开一域"

// TimebackBoundary 本域边界——一句话说明这个域只做什么。
func TimebackBoundary(domainType string) string {
	switch domainType {
	case TimebackBrowserVision:
		return "网页取证——网页原文与截图留在域内，主干只接结论与位置"
	case TimebackImageVision:
		return "看图取证——原图随域回收，主干只接 ASCII 图证与结论"
	case TimebackTextSearch:
		return "文本 / 文档检索——查证体量留在域内，主干只接结论与位置"
	case TimebackCodeReview:
		return "只读取证——读码 / 查引用 / 查死码；不含编译与跑测（那属 code_write）"
	case TimebackCodeWrite:
		return "实现域——读 / 改 / 编译 / 跑测一域内完成；遇计划外问题先 back 回主干"
	}
	return "（未登记边界的域类型）"
}

// TimebackAvailableText 域内可用工具摘要——由白名单派生（白名单唯一真相源）。
func TimebackAvailableText(domainType string) string {
	return strings.Join(TimebackTools(domainType), " · ")
}

// TimebackFindings findings 骨架——按域类型类别分派。
func TimebackFindings(domainType string) string {
	kind := TimebackKind(domainType)
	if kind == TimebackKindImage {
		return "findings 写「看到了什么 + 证据在哪」——图不随回执回来，证据必须写进正文（段内无内容写「（无）」）：\n" +
			"  结论：<一句话答主干的问题>\n" +
			"  图证：<ASCII 图 · ≤24 行 × ≤80 列 · 只画判定所需区域 · 字符 + - | / \\ . # @>\n" +
			"  证据位置：<原图路径或 URL + 图中关键区域（如「左下状态条」）>\n" +
			"  未竟：<没看清 / 没覆盖的>\n" +
			"  卡点与失败：<卡在哪 · 失败原因>\n" +
			"每条 ≤1 行 · 每段 ≤5 条；`图证` 只承载布局 / 位置 / 状态，颜色 / 字号 / 纹理用一行文字补，不拿 ASCII 硬画。\n" +
			"🔴 结论必须被 `图证` 或 `证据位置` 支撑——只有结论没有证据 = 必红；图证与结论对不上时先回看原图再写。\n" +
			"例：结论：工具栏贴顶、状态条在左下 · 图证：+------+/| tool |/… · 证据位置：data:browser/<cat>/out/a.png（左下角）"
	}
	if kind == TimebackKindWrite {
		return "findings 写「改了什么 + 怎么复验」——主干按位置回读改动、照命令复跑（段内无内容写「（无）」）：\n" +
			"  变更：<逐条：文件:行区间 — 改了什么>\n" +
			"  跑测：<可复制命令> → 期望 <判据>\n" +
			"  未竟：<没做完的>\n" +
			"  卡点与解法：<卡在哪 · 怎么绕过去——为什么这么改只在这里留得下>\n" +
			"  失败：<失败原因>\n" +
			"每条 ≤1 行 · 每段 ≤5 条；位置与命令必须是这趟真改过 / 真跑过的——没跑的、凭印象复述的一律不写。\n" +
			"🔴 每行只写下方台账里有对应调用的——`变更` 段逐条对台账里的写操作条目：台账有而未提 = 漏报；提了台账没有 = 幻觉；台账为空而 findings 非空 = 必红。\n" +
			"例：变更：mau:CatHome4/CatHome4.Core/TimebackProfile.cs:120-140 — 新增白名单对账出口；跑测：dotnet test <csproj> --filter <名> → 期望 12 绿"
	}
	return "findings 只写「成果在哪」，不写「结论是什么」——域内不下判断；主干按位置回读，以回读到的真实内容为准（段内无内容写「（无）」）：\n" +
		"  成果：<逐条：位置（文件:行区间 / URL / 截图路径）+ 一句话说那里是什么——主干据此回读>\n" +
		"  未竟：<没查完 / 没覆盖的>\n" +
		"  卡点：<被什么挡住 / 读不通的地方>\n" +
		"  失败：<失败原因>\n" +
		"每条 ≤1 行 · 每段 ≤5 条；位置必须是这趟真实读到 / 打开过的——没读到的、凭印象复述的一律不写。\n" +
		"🔴 每行只写下方台账里有对应调用的——写完自问「这行来自哪次调用」，答不出即删；台账为空而 findings 非空 = 必红。\n" +
		"例：成果：mau:CatHome4/Program.Tools.cs 的 timeback 描述块——findings 参数说明所在行（行号以你实读到的为准）"
}

// TimebackReadback 收域后回读规范——附于 back 返回体的 findings 之后。
func TimebackReadback(domainType string) string {
	kind := TimebackKind(domainType)
	if kind == TimebackKindImage {
		return "🔴 回读规范（收域第一步）——findings 是自述不是实况：核 `图证` 是否真的支撑 `结论`；要再亲眼看一眼原图 → 另开一个识图域，别拿结论当实况"
	}
	if kind == TimebackKindWrite {
		return "🔴 回读规范（收域第一步）——findings 是自述不是实况：按 `变更` 的位置逐条读回改动面"
	}
	return "🔴 回读规范（收域第一步）——findings 是自述不是实况：按 `成果` 的位置逐条读回原文"
}

// TimebackCard 域规范卡全文——注入用的 systemauto user 文本（五段；两 type 各追加一段）。
func TimebackCard(domainType, purpose string, id int64, anchor int) string {
	var b strings.Builder
	b.WriteString("（系统自动 · timeback #")
	b.WriteString(itoa64(id))
	b.WriteString(" 域规范）")
	b.WriteString("域已开：")
	b.WriteString(domainType)
	b.WriteString(" · 用途「")
	b.WriteString(purpose)
	b.WriteString("」· 锚点 ")
	b.WriteString(strconv.Itoa(anchor))
	b.WriteString("\n\n① 本域边界：")
	b.WriteString(TimebackBoundary(domainType))
	b.WriteString("\n② 域内可用：")
	b.WriteString(TimebackAvailableText(domainType))
	b.WriteString("\n③ 域内不可用：")
	b.WriteString(timebackLockedText)
	b.WriteString("\n④ 回执骨架（back 的 findings 按此写）：\n")
	b.WriteString(TimebackFindings(domainType))
	b.WriteString("\n⑤ 建议长度：本域建议 ≤")
	b.WriteString(strconv.Itoa(TimebackSuggestedLength))
	b.WriteString(" 条前文条目——超出先阶段性 back（建议非闸门）")
	if domainType == TimebackBrowserVision {
		b.WriteString("\n\n")
		b.WriteString(timebackBrowserGuideText)
	}
	if domainType == TimebackImageVision {
		b.WriteString("\n\n")
		b.WriteString(timebackImageGuideText)
	}
	return b.String()
}
