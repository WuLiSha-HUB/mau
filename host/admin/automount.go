package admin

import (
	"strconv"

	"cathome5/host/config"
	"cathome5/host/session"
)

// 本文件是 mountAuto 的消费面——消息里「上工 / 开工 <项目>」的即时挂载。
//
// 权威实现 = `mau:CatHome4/CatHome4.Admin/AdminService.InputRoutes.cs` 的
// `RouteChat` / `TryAutoMountOnMessage`（2026-11-09 去首条限制）：判定四条判据后
// 复用挂载执行体（补件 → 清单重算 → 落盘），前文 ≤1 条时与这条消息同一次重建完成。

// TryAutoMountOnMessage 消息自动挂载——返回 true 表示已接管投递（挂载 + 带消息重建）。
//
// 四条判据：① 未挂载项目 ② 消息首词是「上工<名字>」（解析器保证）③ 该项目目录存在
//
//	④ 挂载落盘成功。前文 ≤1 条时挂载与这条消息同一次重建完成（返回 true 接管投递）；
//	已有对话则只落盘、消息按普通输入投递（返回 false，不清前文——重建会把正在进行的对话冲掉）。
func (d *Deps) TryAutoMountOnMessage(content string) bool {
	if d.Session == nil || d.Workspace == nil {
		return false
	}
	intent := d.parseMountIntent(content)
	name := intent.Project
	if name == "" {
		if intent.Raw != "" {
			d.logInject(2, "挂载意图未识别：token="+intent.Raw)
		}
		return false
	}
	cfg, err := config.LoadCatCfg(d.catCfgPath("majordomo"))
	if err != nil || cfg == nil {
		d.logInject(2, "消息自动挂载读取 cat.cfg 失败——按普通消息投递")
		return false
	}
	if !cfg.MountAuto {
		return false
	}
	if cfg.ProjectMount != "" {
		return false
	}
	msgCount := d.sessionMessageCount()
	// rebuild=false——重建由本方法在下方按前文条数决定（带消息重建与先重建再投消息有竞态）
	resp, next := d.applyInjectProjectDefault(name, cfg.ProjectMountExpert, false, false)
	if next == nil {
		d.logInject(2, "消息自动挂载失败：cat=majordomo project="+name+" · "+receiptText(resp))
		return false
	}
	if msgCount > 1 {
		d.logInject(1, "消息自动挂载（不重建）：cat=majordomo project="+name+" injectList="+strconv.Itoa(len(next)))
		return false
	}
	if d.Dispatch != nil {
		d.Dispatch("session.new " + content)
	}
	d.logInject(1, "消息自动挂载（首条）：cat=majordomo project="+name+" injectList="+strconv.Itoa(len(next)))
	return true
}

// TryAutoMountForCat 每猫端口 Chat 的消息自动挂载——C# TryAutoMountOnMessage 按猫目标版逐条对齐。
//
// 四判据：① 该猫未挂载项目 ② 该猫 mountAuto 开 ③ 消息首词是「上工<名字>」（解析器保证）④ 挂载落盘成功。
// 前文 ≤1 条返回 fresh=true（调用方带消息重建接管投递）；已有对话只落盘、消息按普通输入投递（返回 false 侧）。
// 返回 (mounted, fresh)：mounted=false 一律按普通消息投递。
func (d *Deps) TryAutoMountForCat(catID string, sess *session.Session, content string) (bool, bool) {
	if sess == nil {
		return false, false
	}
	intent := d.parseMountIntent(content)
	project := intent.Project
	if project == "" {
		if intent.Raw != "" {
			d.logInject(2, "挂载意图未识别：token="+intent.Raw)
		}
		return false, false
	}
	id, name, _, _, catSess, ok := d.catLookup(catID)
	if !ok || catSess == nil || catSess != sess {
		return false, false
	}
	cfg, err := config.LoadCatCfg(d.catCfgPath(id))
	if err != nil || cfg == nil {
		d.logInject(2, "消息自动挂载读取 cat.cfg 失败（cat="+id+"）——按普通消息投递")
		return false, false
	}
	if !cfg.MountAuto {
		return false, false
	}
	if cfg.ProjectMount != "" {
		return false, false
	}
	msgCount := sessionMessageCountOf(catSess)
	resp, next := d.applyInjectProjectCat(id, name, catSess, project, false, false, false)
	if next == nil {
		d.logInject(2, "消息自动挂载失败：cat="+id+" project="+project+" · "+receiptText(resp))
		return false, false
	}
	if msgCount > 1 {
		d.logInject(1, "消息自动挂载（不重建）：cat="+id+" project="+project+" injectList="+strconv.Itoa(len(next)))
		return true, false
	}
	d.logInject(1, "消息自动挂载（首条）：cat="+id+" project="+project+" injectList="+strconv.Itoa(len(next)))
	return true, true
}

// AutoMountForCat 投递前自动挂载（cat.chat 通道）——C# AdminService.InputRoutes.cs AutoMountForCat 逐字对齐：
// 当前会话不动（清单下个新会话生效）；失败出声，不影响投递。
func (d *Deps) AutoMountForCat(catID, content string) {
	id, _, _, _, sess, ok := d.catLookup(catID)
	if !ok || sess == nil {
		return
	}
	cfg, err := config.LoadCatCfg(d.catCfgPath(id))
	if err != nil || cfg == nil {
		d.logInject(2, "投递自动挂载读取 cat.cfg 失败（cat="+id+"）——按普通消息投递")
		return
	}
	if cfg.ProjectMount != "" {
		return
	}
	if !cfg.MountAuto {
		return
	}
	intent := d.parseMountIntent(content)
	project := intent.Project
	if project == "" {
		if intent.Raw != "" {
			d.logInject(2, "挂载意图未识别（cat.chat）：token="+intent.Raw)
		}
		return
	}
	resp, next := d.applyInjectProjectCat(id, cfg.DisplayName, sess, project, false, false, false)
	if next == nil {
		d.logInject(2, "投递自动挂载失败：cat="+id+" project="+project+" · "+receiptText(resp))
		return
	}
	d.logInject(1, "投递自动挂载（不重建）：cat="+id+" project="+project+" injectList="+strconv.Itoa(len(next)))
}

// receiptText 从挂载执行体回执里取人读文本（失败取 reason、成功取 msg）——日志出声用。
func receiptText(resp any) string {
	switch value := resp.(type) {
	case injectFailResponse:
		return value.Reason
	case injectProjectWriteResponse:
		return value.Msg
	}
	return ""
}
