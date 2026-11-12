package main

import (
	"strconv"
	"time"
)

// 本文件是 --run 单指令模式（对齐 mau:CatHome4/Program.Run.cs 的 RunSingle）：
// 完整启动链装配完成后投递一条指令 → 驱动机制帧至全局 Idle → 输出状态 → 退出。
//
// 边界：
//   - 非交互模式——不监听端口 / 不拉前端测试服务 / 不起托盘 / 不写 HKCU 自启（对齐 C# headless 语义）；
//   - 驱动为单 goroutine 推进（Runner.Tick + 会话 Pump + 按猫新会话消费），不依赖 HTTP pump——
//     与常驻模式的 100ms ticker 串行语义等价，且无跨线程读状态竞争；
//   - 等待上限 120 s（LLM 60 s 现实耗时 + OA 1200 帧结算窗口），超时仍退出 0 并出声（C# 同形）。

const (
	// runWaitMax 单指令驱动上限
	runWaitMax = 120 * time.Second
	// runPollInterval 驱动帧间隔（与机制节拍 100ms 同源）
	runPollInterval = 100 * time.Millisecond
)

// runDriver 是单指令驱动的依赖束（装配层注入，便于单测替换）。
type runDriver struct {
	// router 会话指令路由
	router *commandRouter
	// gate 全局 Idle 判据（与重启闸门同源——只读，不触发重启）
	gate *restartGate
	// tick 推进一帧机制（Runner.Tick + 会话 Pump + 按猫新会话消费）
	tick func()
	// state 读取会话状态（runState / 前文条数 / 视图块数）
	state func() (string, int, int)
}

// runSingleCommand 执行一条指令并等待全局 Idle，返回退出码（0=成功 / 2=指令未受理）。
func runSingleCommand(driver runDriver, command string) int {
	outln("[CMD] 单指令模式 | 指令: " + command)
	if !driver.router.Handle(command) {
		outln("[CMD] 指令未受理: " + command)
		return 2
	}
	// 先推一帧（投递产生的相位 / 工单在下一帧才可见）
	driver.tick()
	var blocker string
	deadline := time.Now().Add(runWaitMax)
	for {
		blocker = driver.gate.describeBlocker()
		if blocker == "" {
			break
		}
		if time.Now().After(deadline) {
			outln("[CMD] 驱动上限 " + strconv.Itoa(int(runWaitMax.Seconds())) + " s 到达——仍有未闭环活动")
			break
		}
		driver.tick()
		time.Sleep(runPollInterval)
	}
	runState, contextCount, viewBlocks := driver.state()
	blocked := "无"
	if blocker != "" {
		blocked = blocker
	}
	outln("[CMD] 结清 | 阻塞: " + blocked + " | runState=" + runState +
		" | 前文 " + strconv.Itoa(contextCount) + " 条 | 视图 " + strconv.Itoa(viewBlocks) + " 块")
	return 0
}
