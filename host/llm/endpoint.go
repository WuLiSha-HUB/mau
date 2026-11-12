package llm

import (
	"cathome5/host/config"
)

// APIConfig 是一条 LLM 端点配置——类型权威在 config 包（读写同源，管理面写入后
// 请求面立即读到同一份解析结果）。
type APIConfig = config.LlmAPIConfig

// Pool 是 LLM 端点池——配置与密钥分离（配置零 key，密钥在 secrets/llm-api.cfg）。
//
// 读路径委托 config.LlmAPIStore（单一解析实现）：管理面写盘后，请求面每次取用都
// 从同一存储重读（C# Runtime 每次 ChatStream 实时读 Store 的同语义）。
type Pool struct {
	// store 端点池存储（配置 + 密钥）
	store *config.LlmAPIStore
}

// LoadPool 读取端点池与密钥——配置缺失 = 空池；密钥缺失 = 空映射（失败出声，不阻断启动）。
func LoadPool(configPath, secretsPath string) *Pool {
	store := config.LoadLlmAPIStore(configPath, secretsPath)
	if store.LastError() != "" {
		logAt(3, store.LastError())
	}
	return &Pool{store: store}
}

// NewPoolFromStore 用既有存储构造端点池——管理面写入与请求面读取共用同一实例（写入即时可见）。
func NewPoolFromStore(store *config.LlmAPIStore) *Pool {
	return &Pool{store: store}
}

// Store 返回底层存储（管理面写面用；nil 安全）。
func (p *Pool) Store() *config.LlmAPIStore {
	if p == nil {
		return nil
	}
	return p.store
}

// Reload 从磁盘重读端点池（配置池写盘后刷新缓存态）。
func (p *Pool) Reload(configPath, secretsPath string) {
	if p == nil {
		return
	}
	p.store = config.LoadLlmAPIStore(configPath, secretsPath)
}

// Configs 全量端点配置副本（身份序）。
func (p *Pool) Configs() []APIConfig {
	if p == nil {
		return nil
	}
	return p.store.Configs()
}

// ResolveDefault 解析默认端点（无默认标记 → nil）。
func (p *Pool) ResolveDefault() *APIConfig {
	if p == nil {
		return nil
	}
	for _, cfg := range p.store.Configs() {
		if cfg.IsDefault {
			out := cfg
			return &out
		}
	}
	return nil
}

// ResolveBackup 解析备用端点（无备用标记 → nil）。
func (p *Pool) ResolveBackup() *APIConfig {
	if p == nil {
		return nil
	}
	for _, cfg := range p.store.Configs() {
		if cfg.IsBackup {
			out := cfg
			return &out
		}
	}
	return nil
}

// TryGet 按身份取配置（空身份 / 未命中 → nil）。
func (p *Pool) TryGet(id string) *APIConfig {
	if p == nil {
		return nil
	}
	cfg, ok := p.store.TryGet(id)
	if !ok {
		return nil
	}
	return &cfg
}

// Secret 取配置对应密钥（空身份 / 未命中 → 空串）。
func (p *Pool) Secret(id string) string {
	if p == nil {
		return ""
	}
	return p.store.Secret(id)
}

// IsZeroGUID 判定空身份（全零 GUID = 跟随默认端点语义）。
func IsZeroGUID(value string) bool { return config.IsZeroGUID(value) }

// DeriveChatEndpoint 端点推导——已含 /chat/completions 原样；其余拼尾（空入参 → 空串）。
func DeriveChatEndpoint(endpoint string) string {
	return config.DeriveChatEndpoint(endpoint)
}
