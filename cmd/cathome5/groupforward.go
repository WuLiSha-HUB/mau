package main

import (
	"strconv"
	"strings"
	"time"

	"cathome5/host/group"
)

// 本文件复现 C# CatHome4.Admin.GroupAutoForward（订阅推送 · 独立渠道）。
//
// 语义逐条对齐 mau:CatHome4/CatHome4.Admin/GroupAutoForward.cs（193 行）：
//   - 组静默窗口到期后，把每个订阅者的未读区间汇总成一条注入；
//   - 与派单 / 回执无关（所有条目都计入，含订阅者自己发的）；origin 第 4 段 auto（轮末不回执 → 零环）；
//   - 投递成功才推进游标（未运行 / 未受理下次静默窗口重试，不丢消息）；
//   - 主循环每帧巡检（内部 1 秒节流，开销可忽略）；--run 单指令模式不巡检（对齐 C# RunSingle）。
//
// 接线点：main.go [段12f] ticker（对应 C# Program.Run.cs:88 RunInteractive 主循环）。

const (
	// groupForwardMaxBatch 单批最多汇总条数——超出留待下一窗口继续（不一次冲爆订阅者前文）
	groupForwardMaxBatch = 100
	// groupForwardPumpInterval 巡检最小间隔——主循环每帧调用，内部按 1 秒节流
	groupForwardPumpInterval = time.Second
)

// groupForwarder 订阅巡检器——组静默窗口到期后按订阅者推送未读区间。
type groupForwarder struct {
	// svc 组服务面（组数据面 + 投递寻址 + 日志出口）
	svc *hostGroupService
	// lastPumpAt 上次巡检时刻——单调时钟（对齐 C# Environment.TickCount64）
	lastPumpAt time.Time
	// now 墙钟注入（Unix 毫秒；nil = group.Now）——测试注入固定时刻，生产不赋值
	now func() int64
}

// Pump 订阅巡检——主循环每帧调用（内部 1 秒节流）；静默窗口到期且有未读的组按订阅者推送。
func (f *groupForwarder) Pump() {
	if f.svc == nil || f.svc.Store == nil {
		return
	}
	if !f.lastPumpAt.IsZero() && time.Since(f.lastPumpAt) < groupForwardPumpInterval {
		return
	}
	f.lastPumpAt = time.Now()
	nowMs := group.Now()
	if f.now != nil {
		nowMs = f.now()
	}
	groups := f.svc.Store.ListGroups()
	for i := range groups {
		g := &groups[i]
		if !g.Running {
			continue
		}
		if len(g.SubscriberKeys) == 0 {
			continue
		}
		if g.LastSeq <= 0 || g.LastAt <= 0 {
			continue
		}
		idleSec := group.GetSubscribeIdleSec(g)
		if nowMs-g.LastAt < int64(idleSec)*1000 {
			continue
		}
		f.pushGroup(g)
	}
}

// pushGroup 单组推送——逐订阅者取未读区间（游标之后），汇总注入并推进游标。
func (f *groupForwarder) pushGroup(g *group.Summary) {
	for _, sub := range g.SubscriberKeys {
		if sub == "" {
			continue
		}
		cursor := f.svc.Store.GetSubscriberCursor(g.Name, sub)
		if cursor >= g.LastSeq {
			continue
		}
		// [段1] 未读区间——单批上限；投递成功才推进游标（剩余留待下一窗口）
		batch := f.svc.Store.ReadMessages(g.Name, cursor, groupForwardMaxBatch)
		if len(batch) == 0 {
			continue
		}
		cat := f.svc.findDeliveryCat(sub)
		if cat == nil {
			f.svc.log(2, "订阅推送跳过（订阅猫未运行）: "+g.Name+" | "+sub)
			continue
		}
		lastSeq := batch[len(batch)-1].Seq
		content := f.buildSummary(g.Name, batch)
		origin := "group:" + g.Name + ":" + strconv.FormatInt(lastSeq, 10) + ":auto"
		if !cat.PostUser(content, "user", "", origin) {
			f.svc.log(2, "订阅推送未受理: "+g.Name+" | "+sub)
			continue
		}
		// [段2] 游标推进——落盘失败出声（下次窗口会重推该区间，不静默丢）
		result := f.svc.Store.SetSubscriberCursor(g.Name, sub, lastSeq)
		if !strings.HasPrefix(result, "ok|") {
			f.svc.log(2, "订阅游标未写入: "+g.Name+" | "+sub+" | "+result)
		}
		f.svc.log(1, "订阅推送: "+g.Name+" | "+sub+" | 未读 "+strconv.Itoa(len(batch))+" 条 → #"+strconv.FormatInt(lastSeq, 10))
	}
}

// buildSummary 汇总正文——组名 + 区间头 + 逐条（#seq [类型] 发方 → 目标 · 正文 · 关联）。
func (f *groupForwarder) buildSummary(groupName string, batch []group.Message) string {
	var sb strings.Builder
	sb.WriteString("[来自组:")
	sb.WriteString(groupName)
	sb.WriteString("]\n")
	sb.WriteString("（订阅汇总 · 未读 ")
	sb.WriteString(strconv.Itoa(len(batch)))
	sb.WriteString(" 条 · #")
	sb.WriteString(strconv.FormatInt(batch[0].Seq, 10))
	sb.WriteString("–#")
	sb.WriteString(strconv.FormatInt(batch[len(batch)-1].Seq, 10))
	sb.WriteString("）")
	for i := range batch {
		m := &batch[i]
		sb.WriteString("\n#")
		sb.WriteString(strconv.FormatInt(m.Seq, 10))
		sb.WriteString(" [")
		sb.WriteString(groupForwardTypeLabel(m.Type))
		sb.WriteString("] ")
		sb.WriteString(f.displayNameOf(m.From))
		if m.To != "" {
			sb.WriteString(" → @")
			sb.WriteString(f.displayNameOf(m.To))
		}
		sb.WriteString(" · ")
		sb.WriteString(groupForwardFlatten(m.Text))
		if m.Refs != "" {
			sb.WriteString("（关联 ")
			sb.WriteString(m.Refs)
			sb.WriteString("）")
		}
	}
	return sb.String()
}

// displayNameOf 显示名——空 key（人 / 外部）回「人」；解析失败回原 key。
func (f *groupForwarder) displayNameOf(catKey string) string {
	if catKey == "" {
		return "人"
	}
	name := f.svc.resolveCatDisplayName(catKey)
	if name == "" {
		return catKey
	}
	return name
}

// groupForwardFlatten 正文单行化——换行折成空格（汇总里一行一条）。
func groupForwardFlatten(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " ")
}

// groupForwardTypeLabel 消息类型中文标签（delegation→派单 · receipt→回执 · 其余→发言）。
func groupForwardTypeLabel(msgType string) string {
	if msgType == "delegation" {
		return "派单"
	}
	if msgType == "receipt" {
		return "回执"
	}
	return "发言"
}
