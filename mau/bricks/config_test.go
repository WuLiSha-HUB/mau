package bricks

import (
	"strings"
	"testing"
)

// fakeConfigService 是配置窄接口的测试替身（读面 / 写面 / 声明面全部可观察）。
type fakeConfigService struct {
	schema      []ConfigItem
	entries     map[string]ConfigEntry
	values      [][2]string
	declareErr  string
	declareFile string
	newSegment  bool
	registerOut string
	declared    []string
	setErr      string
	setCalls    [][2]string
	resetErr    string
	resetCalls  []string
}

func (f *fakeConfigService) AllEntries() []ConfigEntry {
	out := make([]ConfigEntry, 0, len(f.schema))
	for _, item := range f.schema {
		out = append(out, f.resolve(item.Key))
	}
	for _, pair := range f.values {
		known := false
		for _, item := range f.schema {
			if item.Key == pair[0] {
				known = true
				break
			}
		}
		if !known {
			out = append(out, f.resolve(pair[0]))
		}
	}
	return out
}

func (f *fakeConfigService) Resolve(key string) ConfigEntry { return f.resolve(key) }

func (f *fakeConfigService) resolve(key string) ConfigEntry {
	if entry, ok := f.entries[key]; ok {
		return entry
	}
	for _, item := range f.schema {
		if item.Key == key {
			return ConfigEntry{Key: key, Value: item.Default, Source: "default", Default: item.Default,
				Desc: item.Desc, EmptyDesc: item.EmptyDesc, Type: item.Type, Min: item.Min, Max: item.Max,
				Declared: true, Writable: item.Writable, Sensitive: item.Sensitive}
		}
	}
	return ConfigEntry{Key: key, Desc: "未在 schema 声明（只读；声明后可见可改）"}
}

func (f *fakeConfigService) Values() [][2]string { return f.values }

func (f *fakeConfigService) SchemaItems() []ConfigItem { return f.schema }

func (f *fakeConfigService) DeclareConfig(key, desc string) (string, bool, string, string) {
	f.declared = append(f.declared, key)
	if f.declareErr != "" {
		return "", false, "", f.declareErr
	}
	file := f.declareFile
	if file == "" {
		file = "x.cfg"
	}
	return file, f.newSegment, f.registerOut, ""
}

func (f *fakeConfigService) SetChecked(key, value string) (bool, string) {
	f.setCalls = append(f.setCalls, [2]string{key, value})
	if f.setErr != "" {
		return false, f.setErr
	}
	return true, ""
}

func (f *fakeConfigService) ResetToDefault(key string) (bool, string) {
	f.resetCalls = append(f.resetCalls, key)
	if f.resetErr != "" {
		return false, f.resetErr
	}
	return true, ""
}

// newFakeConfig 两声明项（一项敏感只读 / 一项可写）+ 一条未声明落盘键。
func newFakeConfig() *fakeConfigService {
	return &fakeConfigService{
		schema: []ConfigItem{
			{Key: "llm.api_key", File: "llm.cfg", Default: "", Desc: "接口密钥", Sensitive: true, Writable: false, Type: "string"},
			{Key: "ui.font_scale", File: "ui.cfg", Default: "100", Desc: "界面字号缩放", EmptyDesc: "空=基准", Writable: true, Type: "int", Min: "50", Max: "300"},
		},
		entries: map[string]ConfigEntry{
			"llm.api_key": {Key: "llm.api_key", Value: "sk-secret-value", Source: "file", Desc: "接口密钥", Sensitive: true, Declared: true},
			"ui.font_scale": {Key: "ui.font_scale", Value: "100", Source: "default", Default: "100",
				Desc: "界面字号缩放", EmptyDesc: "空=基准", Type: "int", Min: "50", Max: "300", Declared: true, Writable: true},
			"ui.legacy_flag": {Key: "ui.legacy_flag", Value: "1", Source: "file", Desc: "未在 schema 声明（只读；声明后可见可改）"},
		},
		values: [][2]string{{"llm.api_key", "sk-secret-value"}, {"ui.legacy_flag", "1"}},
	}
}

func TestConfigBridgeArgs(t *testing.T) {
	svc := newFakeConfig()
	if out, ok := ConfigBridge(svc, "drop", "{}"); ok || out != "ERR|UNKNOWN_METHOD|config.drop" {
		t.Fatalf("未知方法: %q %v", out, ok)
	}
	if out, ok := ConfigBridge(svc, "get", `{"zzz":"1"}`); ok || !strings.Contains(out, "ERR|BAD_ARGS|未知参数: zzz") {
		t.Fatalf("未知参数: %q %v", out, ok)
	}
	if out, ok := ConfigBridge(svc, "get", `{}`); ok || !strings.Contains(out, "缺参数 key") {
		t.Fatalf("缺必填: %q %v", out, ok)
	}
	if out, ok := ConfigBridge(nil, "list", `{}`); ok || out != "ERR|CONFIG_NO_STORE|宿主未注入 ConfigStore" {
		t.Fatalf("服务未注入: %q %v", out, ok)
	}
	// catId 是宿主保留键（参数面放行）
	if _, ok := ConfigBridge(svc, "get", `{"key":"ui.font_scale","catId":"majordomo"}`); !ok {
		t.Fatal("catId 保留键应放行")
	}
}

func TestConfigListRendering(t *testing.T) {
	out, ok := ConfigBridge(newFakeConfig(), "list", `{}`)
	if !ok {
		t.Fatalf("list 失败: %s", out)
	}
	head := `{"ok":true,"tool":"config-list","items":3,"writable":1,"sensitive":1,"sections":"llm / ui"}`
	if !strings.HasPrefix(out, head+"\n") {
		t.Fatalf("头不符: %q", strings.SplitN(out, "\n", 2)[0])
	}
	if !strings.Contains(out, "\n3 项 · 可写 1 · 敏感 1\n") {
		t.Fatalf("摘要行不符: %q", out)
	}
	if !strings.Contains(out, "llm.api_key=**** | 来源=file | 默认= | 只读 | 接口密钥") {
		t.Fatalf("敏感键应掩码且只读: %q", out)
	}
	if !strings.Contains(out, "ui.font_scale=100 | 来源=default | 默认=100 | 可写 | 空=基准 | 类型=int[50,300] | 界面字号缩放") {
		t.Fatalf("声明项渲染不符: %q", out)
	}
	if !strings.Contains(out, "ui.legacy_flag=1 | 来源=file | 未在 schema 声明（只读；声明后可见可改）") {
		t.Fatalf("未声明键渲染不符: %q", out)
	}
}

func TestConfigGetPaths(t *testing.T) {
	svc := newFakeConfig()
	if out, ok := ConfigBridge(svc, "get", `{"key":""}`); ok || !strings.Contains(out, "缺参数 key") {
		t.Fatalf("空 key: %q %v", out, ok)
	}
	// 未声明且未落盘 → NOT_FOUND（键不存在 + 段内键清单）；方法本身受理（C# Invoke 同口径）
	out, ok := ConfigBridge(svc, "get", `{"key":"ui.unknown"}`)
	if !ok || !strings.HasPrefix(out, "ERR|NOT_FOUND|键不存在: ui.unknown——段 ui 现有键 ") {
		t.Fatalf("缺键说明: %q %v", out, ok)
	}
	// 段不存在
	out, ok = ConfigBridge(svc, "get", `{"key":"zzz.k"}`)
	if !ok || !strings.Contains(out, "段不存在: zzz——可用段: llm / ui") {
		t.Fatalf("段说明: %q %v", out, ok)
	}
	// 敏感键掩码 + 头字段
	out, ok = ConfigBridge(svc, "get", `{"key":"llm.api_key"}`)
	if !ok {
		t.Fatalf("get 失败: %s", out)
	}
	if !strings.HasPrefix(out, `{"ok":true,"tool":"config-get","target":"llm.api_key","source":"file","declared":true,"writable":false,"sensitive":true}`+"\n") {
		t.Fatalf("头不符: %q", strings.SplitN(out, "\n", 2)[0])
	}
	if !strings.Contains(out, "llm.api_key=**** | 来源=file") {
		t.Fatalf("掩码不符: %q", out)
	}
	if !strings.Contains(out, "llm.api_key | 来源 file") {
		t.Fatalf("摘要行不符: %q", out)
	}
}

func TestConfigSetDeclarePaths(t *testing.T) {
	svc := newFakeConfig()
	// 未声明未带 declare → 拒绝（附指引）；方法本身受理（C# Invoke 同口径：错误走 ERR| 文本）
	out, ok := ConfigBridge(svc, "set", `{"key":"x.y","value":"1"}`)
	if !ok || out != "ERR|CONFIG_REJECT|配置键未在 schema 声明: x.y——新建请加 declare=true（可配 desc=说明）" {
		t.Fatalf("未声明拒绝: %q %v", out, ok)
	}
	// declare=true → 先声明再写；新段注册说明随回执
	svc.newSegment = true
	svc.registerOut = " · 新段已即时注册"
	out, ok = ConfigBridge(svc, "set", `{"key":"x.y","value":"1","declare":"true","desc":"测试项"}`)
	if !ok {
		t.Fatalf("声明写入失败: %s", out)
	}
	if len(svc.declared) != 1 || svc.declared[0] != "x.y" {
		t.Fatalf("声明未调用: %v", svc.declared)
	}
	if !strings.Contains(out, `{"ok":true,"tool":"config-set","target":"x.y","sensitive":false,"declaredNow":true}`) {
		t.Fatalf("头不符: %q", out)
	}
	if !strings.Contains(out, "x.y | 已更新 = 1 · 已声明（段文件 x.cfg） · 新段已即时注册") {
		t.Fatalf("回执不符: %q", out)
	}
	// 已声明再带 declare → 忽略说明
	out, ok = ConfigBridge(svc, "set", `{"key":"ui.font_scale","value":"120","declare":"true"}`)
	if !ok || !strings.Contains(out, "已更新 = 120 · 键已声明，declare 忽略") {
		t.Fatalf("忽略说明不符: %q %v", out, ok)
	}
	// 敏感键回执掩码
	out, _ = ConfigBridge(svc, "set", `{"key":"llm.api_key","value":"sk-new"}`)
	_ = out
	// SetChecked 失败 → 拒绝原文
	svc.setErr = "只读配置项: llm.api_key"
	out, ok = ConfigBridge(svc, "set", `{"key":"llm.api_key","value":"sk-new"}`)
	if !ok || out != "ERR|CONFIG_REJECT|只读配置项: llm.api_key" {
		t.Fatalf("写失败面: %q %v", out, ok)
	}
	// 声明失败 → CONFIG_DECLARE 原文
	svc2 := newFakeConfig()
	svc2.declareErr = "键缺少段前缀（形如 ui.font_scale）"
	out, ok = ConfigBridge(svc2, "set", `{"key":"ab","value":"1","declare":"true"}`)
	if !ok || out != "ERR|CONFIG_DECLARE|键缺少段前缀（形如 ui.font_scale）" {
		t.Fatalf("声明失败面: %q %v", out, ok)
	}
}

func TestConfigSetSensitiveEcho(t *testing.T) {
	svc := newFakeConfig()
	out, ok := ConfigBridge(svc, "set", `{"key":"llm.api_key","value":"sk-new"}`)
	if !ok {
		t.Fatalf("敏感键写失败: %s", out)
	}
	if !strings.Contains(out, "llm.api_key | 已更新 = ****") {
		t.Fatalf("敏感值应掩码: %q", out)
	}
}

func TestConfigResetPaths(t *testing.T) {
	svc := newFakeConfig()
	out, ok := ConfigBridge(svc, "reset", `{}`)
	if !ok || out != `{"ok":true,"tool":"config-reset","items":1,"scope":"all"}`+"\n"+"全部可写配置项 | 已还原 1 项" {
		t.Fatalf("全群还原: %q %v", out, ok)
	}
	if len(svc.resetCalls) != 1 || svc.resetCalls[0] != "" {
		t.Fatalf("全群应传空 key: %v", svc.resetCalls)
	}
	out, ok = ConfigBridge(svc, "reset", `{"key":"ui.font_scale"}`)
	if !ok || out != `{"ok":true,"tool":"config-reset","target":"ui.font_scale","items":1,"scope":"single"}`+"\n"+"ui.font_scale | 已还原" {
		t.Fatalf("单项还原: %q %v", out, ok)
	}
	svc.resetErr = "schema 未绑定"
	if out, ok := ConfigBridge(svc, "reset", `{}`); !ok || out != "ERR|CONFIG_REJECT|schema 未绑定" {
		t.Fatalf("还原失败面: %q %v", out, ok)
	}
}

func TestCatCfgBridge(t *testing.T) {
	if out, ok := CatCfgBridge(nil, "drop", "{}"); ok || out != "ERR|UNKNOWN_METHOD|catcfg.drop" {
		t.Fatalf("未知方法: %q %v", out, ok)
	}
	if out, ok := CatCfgBridge(nil, "get", `{"zzz":"1"}`); ok || !strings.Contains(out, "未知参数: zzz") {
		t.Fatalf("未知参数: %q %v", out, ok)
	}
	if out, ok := CatCfgBridge(nil, "set", `{"cat":"majordomo","field":"type"}`); ok || !strings.Contains(out, "缺参数 value") {
		t.Fatalf("缺必填: %q %v", out, ok)
	}
	if out, ok := CatCfgBridge(nil, "get", `{"cat":"majordomo"}`); ok || out != "ERR|HOSTCMD_NO_SERVICE|宿主指令服务未注入（宿主未接线）" {
		t.Fatalf("服务未注入: %q %v", out, ok)
	}
	// 指令行拼接（值取行内剩余原文）+ 成功包装
	var gotLine string
	out, ok := CatCfgBridge(func(line string) string {
		gotLine = line
		return "cat.cfg | majordomo\r\n  id=majordomo"
	}, "set", `{"cat":"majordomo","field":"persona","value":"多行 文本"}`)
	if !ok {
		t.Fatalf("set 失败: %s", out)
	}
	if gotLine != "cat.cfg.set majordomo persona 多行 文本" {
		t.Fatalf("指令行不符: %q", gotLine)
	}
	want := `{"ok":true,"tool":"config-cat-set","target":"majordomo"}` + "\n" + "majordomo | cat.cfg 写入\ncat.cfg | majordomo\r\n  id=majordomo"
	if out != want {
		t.Fatalf("回执不符:\n got=%q\nwant=%q", out, want)
	}
	// get 线
	out, ok = CatCfgBridge(func(line string) string {
		gotLine = line
		return "cat.cfg | majordomo"
	}, "get", `{"cat":"majordomo"}`)
	if !ok || gotLine != "cat.cfg.get majordomo" {
		t.Fatalf("get 指令行: %q %v", gotLine, ok)
	}
	if !strings.Contains(out, `{"ok":true,"tool":"config-cat-get","target":"majordomo"}`) {
		t.Fatalf("get 头不符: %q", out)
	}
	// 宿主回执 ERR| 前缀 → 原样直返（不包装头）
	if out, ok := CatCfgBridge(func(string) string { return "ERR|HOSTCMD_X|坏了" }, "get", `{"cat":"majordomo"}`); ok || out != "ERR|HOSTCMD_X|坏了" {
		t.Fatalf("错误面直返: %q %v", out, ok)
	}
	// 空回执 → EMPTY_RESULT
	if out, ok := CatCfgBridge(func(string) string { return "" }, "get", `{"cat":"majordomo"}`); ok || out != "ERR|EMPTY_RESULT|宿主指令无回执" {
		t.Fatalf("空回执: %q %v", out, ok)
	}
}
