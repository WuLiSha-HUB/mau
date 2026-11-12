package bricks

// TsService —— node 常驻服务执行面（TsCat ts-* 五件消费）。
//
// 语义对齐 C# Mau.Runtime.NodeService + StdioServiceHost（design-ch4-node-services）：
//   - 解释器 / 脚本 / 参数 / 超时全读配置（node.node_path · node.service.ts.*）
//   - stdio 严格协议：stdout 一行 JSON；首行 ready 握手；stderr 归日志（不进协议面）
//   - 单服务纯串行；超时报错不杀进程；进程崩溃下次调用重拉并在回执标注 restarted
//   - 服务名固定 "ts"（错误码前缀 NODE_ 与 C# 同口径）
// 脚本只允许可写受控根内（对齐 C# IsUnderWritableRoot）；解释器未配置即明示不可用（不回落 PATH）。
//
// 协议核心在 stdio_host.go（与 py 共用）；本文件只做配置解析与薄包装。

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"cathome5/mau/fs"
)

const (
	// tsServiceName 是服务实例名——对应配置段 node.service.ts.*。
	tsServiceName = "ts"
	// tsDefaultTimeoutMs 是单次调用默认超时（与工具执行层同口径）。
	tsDefaultTimeoutMs = 120000
	// tsMinTimeoutMs 与 tsMaxTimeoutMs 是 timeout_ms 的夹取区间（C# ParseServiceTimeout 上限同值）。
	tsMinTimeoutMs = 1000
	tsMaxTimeoutMs = 120000
)

// TsService 是 ts 服务的进程宿主——首次调用按需拉起，跨调用复用同一进程。
type TsService struct {
	// roots 受控根表（脚本路径归属校验面）
	roots []fs.Root
	// cfg 配置读取（node.* 段；nil = 全部按未配置口径）
	cfg func(key string) string
	// logf 日志出口（level 0=debug 1=info 2=warn 3=err；nil = 不出声）
	logf func(level int, message string)

	// mu 串行锁——服务调用纯串行（C# lock (_gate) 同语义）
	mu sync.Mutex
	// host 协议宿主（nil = 未构造；首次拉起时按解析结果装配）
	host *stdioHost

	// resolved 解析结果已缓存（配置读一次；进程重拉复用）
	resolved bool
	// exe 解释器全路径
	exe string
	// script 入口脚本全路径
	script string
	// args 附加启动参数
	args []string
	// home 环境目录（--home 传入；空 = 不传）
	home string
	// timeoutMs 单次调用超时毫秒
	timeoutMs int
	// env 追加环境变量（测试注入替身进程用；生产为空）
	env []string
}

// NewTsService 构造 ts 服务宿主。
//
// roots 取受控根全表（脚本归属校验：非可写根按「超出受控根」拒绝）；
// cfg 读配置有效值（file → env → default，由组合根适配）。
func NewTsService(roots []fs.Root, cfg func(key string) string, logf func(level int, message string)) *TsService {
	return &TsService{roots: roots, cfg: cfg, logf: logf}
}

// Name 返回服务名（固定 "ts"）。
func (s *TsService) Name() string { return tsServiceName }

// Start 主动拉起服务——已存活即幂等返回空串。
func (s *TsService) Start() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host != nil && s.host.alive() {
		return ""
	}
	if !s.resolved {
		if errText := s.resolveLocked(); errText != "" {
			return errText
		}
	}
	return s.hostLocked().start()
}

// Stop 停止服务——关 stdin（脚本按宿主退出处理），5 秒未退强杀进程树。
func (s *TsService) Stop() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host == nil {
		return ""
	}
	return s.host.stop()
}

// Shutdown 宿主退出收尾（错误不阻断退出）。
func (s *TsService) Shutdown() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host == nil || s.host.cmd == nil {
		return
	}
	s.host.stop()
}

// Alive 报告服务进程是否存活。
func (s *TsService) Alive() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.host != nil && s.host.alive()
}

// Invoke 执行一次服务调用——发一行 JSON、读一行 JSON（纯串行）。
//
// 未启动则按需拉起；进程崩溃重拉并在回执标注 restarted（内层对象浅合并）。
// 服务层回执原样返回（调用方按 JSON 解析）；失败面一律 ERR|NODE_* 文本。
func (s *TsService) Invoke(payload string) string {
	if s == nil {
		return "ERR|NODE_UNAVAILABLE|服务不可用（" + tsServiceName + "）"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.resolved {
		if errText := s.resolveLocked(); errText != "" {
			return errText
		}
	}
	return s.hostLocked().invoke(payload)
}

// hostLocked 取协议宿主并把解析结果同步进去——调用方持锁。
func (s *TsService) hostLocked() *stdioHost {
	if s.host == nil {
		s.host = &stdioHost{name: tsServiceName, errPrefix: "NODE"}
	}
	s.host.logf = s.logf
	s.host.exe = s.exe
	s.host.script = s.script
	s.host.args = s.args
	s.host.home = s.home
	s.host.timeoutMs = s.timeoutMs
	s.host.env = s.env
	return s.host
}

// resolveLocked 解析解释器与脚本——配置缺失 / 路径不存在一律明示 ERR（不静默回落）。
func (s *TsService) resolveLocked() string {
	nodePath := s.cfgValue("node.service." + tsServiceName + ".node")
	if nodePath == "" {
		nodePath = s.cfgValue("node.node_path")
	}
	if nodePath == "" {
		return "ERR|NODE_NOT_CONFIGURED|Node 解释器未配置——请在配置面填写 node.node_path（node.exe 全路径；获取方式 where node）"
	}
	if _, err := os.Stat(nodePath); err != nil {
		return "ERR|NODE_NODE_NOT_FOUND|配置的解释器路径不存在：" + nodePath + "——请更新 node.node_path"
	}
	scriptRaw := s.cfgValue("node.service." + tsServiceName + ".script")
	if scriptRaw == "" {
		return "ERR|NODE_SERVICE_NOT_CONFIGURED|服务未配置：" + tsServiceName + "——请在配置面填写 node.service." + tsServiceName + ".script"
	}
	base, _ := os.Getwd()
	full, root, code, msg, ok := fs.ResolveOwnedRoot(scriptRaw, s.roots, base)
	if !ok {
		if code == fs.ErrOutOfScope {
			return "ERR|NODE_SCRIPT_OUTSIDE_ROOT|服务脚本超出受控根：" + full + "——只允许可写受控根内的脚本"
		}
		return "ERR|NODE_BAD_PATH|服务脚本路径非法: " + msg
	}
	if !root.Writable {
		return "ERR|NODE_SCRIPT_OUTSIDE_ROOT|服务脚本超出受控根：" + full + "——只允许可写受控根内的脚本"
	}
	if _, err := os.Stat(full); err != nil {
		return "ERR|NODE_SCRIPT_NOT_FOUND|服务脚本不存在：" + full
	}
	s.exe = nodePath
	s.script = full
	s.args = strings.Fields(s.cfgRaw("node.service." + tsServiceName + ".args"))
	s.home = s.cfgValue("node.service." + tsServiceName + ".home")
	s.timeoutMs = tsTimeoutFromConfig(s.cfgValue("node.service." + tsServiceName + ".timeout_ms"))
	s.resolved = true
	return ""
}

// cfgRaw 读配置原文（不去空白——启动参数按原样切分）。
func (s *TsService) cfgRaw(key string) string {
	if s.cfg == nil {
		return ""
	}
	return s.cfg(key)
}

// cfgValue 读配置并去首尾空白（路径 / 数值类字段）。
func (s *TsService) cfgValue(key string) string {
	return strings.TrimSpace(s.cfgRaw(key))
}

// tsTimeoutFromConfig 解析 timeout_ms——非法 / 缺省取默认，夹取 1000..120000。
func tsTimeoutFromConfig(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return tsDefaultTimeoutMs
	}
	if value < tsMinTimeoutMs {
		return tsMinTimeoutMs
	}
	if value > tsMaxTimeoutMs {
		return tsMaxTimeoutMs
	}
	return value
}
