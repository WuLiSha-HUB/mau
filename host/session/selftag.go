package session

// DefaultClientID 客户端标识兜底——产品级归因（C# Tag.ClientId 默认 cat-home4 同口径）。
const DefaultClientID = "cat-home5"

// SelfTag 是入站自报身份容器——发往「自家 apis」（回环 + 18100）的请求附加头数据源。
//
// 语义对齐 C# CatHome4 的 ChatSession._selfTag（Mau.Runtime.LlmSelfTag.Tag）：
// 猫身份三项按请求取实时值（改显示名 / 新会话即时反映）；项目挂载三项由挂载面写入
// （启动装配初值 + 挂载 / 取消挂载刷新）；客户端标识装配期写入一次。
type SelfTag struct {
	// CatID 猫 key（会话目录名）
	CatID string
	// CatName 猫显示名
	CatName string
	// SessionID 会话世代 ID（session.new 换新）
	SessionID string
	// Project 当前挂载项目名（空 = 未挂载）
	Project string
	// MountMode 挂载模式（expert / readme；未挂载为空）
	MountMode string
	// Jobs 挂载项目 Job 清单（逗号分隔；未挂载为空）
	Jobs string
	// ClientID 客户端标识（产品 / 版本）
	ClientID string
}

// SetClientID 设置客户端标识（空 = 保持原值）。
func (s *Session) SetClientID(clientID string) {
	if clientID == "" {
		return
	}
	s.mu.Lock()
	s.clientID = clientID
	s.mu.Unlock()
}

// SetSelfTagMount 写入项目挂载自报信息——项目名空即整组清空（适配层按空跳过，不发空值）。
func (s *Session) SetSelfTagMount(project, mode, jobs string) {
	s.mu.Lock()
	s.selfProject = project
	if project == "" {
		s.selfMountMode = ""
		s.selfJobs = ""
	} else {
		s.selfMountMode = mode
		s.selfJobs = jobs
	}
	s.mu.Unlock()
}

// SelfTag 取当前自报身份快照——猫身份取实时值（每请求调用）。
func (s *Session) SelfTag() SelfTag {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SelfTag{
		CatID:     s.CatKey,
		CatName:   s.DisplayName,
		SessionID: s.sessionID,
		Project:   s.selfProject,
		MountMode: s.selfMountMode,
		Jobs:      s.selfJobs,
		ClientID:  ClientIDOrDefault(s.clientID),
	}
}

// ClientIDOrDefault 客户端标识兜底——空值回落产品默认（C# Tag.ClientId 默认同口径）。
func ClientIDOrDefault(clientID string) string {
	if clientID == "" {
		return DefaultClientID
	}
	return clientID
}
