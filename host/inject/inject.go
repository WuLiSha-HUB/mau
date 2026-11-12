// Package inject 承载注入面构建——注入提示词 + 逐文件结果 + 注入报告 JSON + 项目一句话摘要。
//
// 契约对齐（逐字）：
//   - mau:CatHome4/Program.Chat.cs 的 BuildInjectPrompt（段序 / 文案 / 三态 / 目录展开）
//   - mau:CatHome4/CatHome4.Core/ChatBridge.cs 的 BuildInjectReportJson（键序 / 计数 / 工具组）
//   - mau:CatHome4/CatHome4.Core/ProjectSummary.cs 的 Of / Extract（摘要抽取三路）
//
// 纯构建：不触碰会话状态；读文件失败按 error / missing 逐条入账，不阻断会话（与 C# 同语义）。
// 出声口径：C# LogStore.Add 的注入缺失 / 失败三类文案由调用方注入的 Logf 承载（nil = 静默）。
package inject

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cathome5/host/session"
)

// newline 段落分隔——对齐 C# Environment.NewLine（Windows 宿主为 CRLF）。
const newline = "\r\n"

// 逐文件三态——与 C# InjectFileResult.Status 同值域。
const (
	// StatusOK 已注入
	StatusOK = "ok"
	// StatusMissing 目标缺失（可选，已跳过）
	StatusMissing = "missing"
	// StatusError 读取或寻址失败
	StatusError = "error"
)

// 注入缺失 / 失败文案——C# 逐字。
const (
	// msgFileMissing 单文件不存在
	msgFileMissing = "文件不存在（可选，已跳过）"
	// msgDirEmpty 目录内无 .md
	msgDirEmpty = "目录内无 .md 文件（可选，已跳过）"
)

// Logf 注入面出声回调（级别 / 消息 / 分类；nil = 静默）。
type Logf func(level int, message, category string)

// log 出声（nil 安全）。
func (l Logf) log(level int, message, category string) {
	if l != nil {
		l(level, message, category)
	}
}

// Resolver 注入条目寻址解析——`id:相对路径` → 绝对路径；失败返回错误。
type Resolver func(spec string) (string, error)

// ReadText 文本读取（编码探测 + BOM 剥离）——按绝对路径读。
type ReadText func(absPath string) (string, error)

// FileResult 逐文件注入结果——对齐 C# InjectFileResult（File / Status / Message / Chars / Bytes）。
type FileResult struct {
	// File 条目显示名（单文件 = 清单原文；目录展开 = 前缀/文件名）
	File string
	// Status 三态：ok / missing / error
	Status string
	// Message 失败原因（ok 时空串）
	Message string
	// Chars 正文字符数（UTF-16 码元，对齐 C# string.Length）
	Chars int
	// Bytes 文件字节数（FileInfo.Length）
	Bytes int64
}

// PromptResult 注入提示词构建结果——提示词全文 + 逐文件结果。
type PromptResult struct {
	// Prompt 注入提示词全文
	Prompt string
	// Files 逐文件结果（按清单 / 目录展开序）
	Files []FileResult
}

// BuildPrompt 构建注入提示词——段0 基础角色 + 角色设定 + 系统前文来源（逐文件）。
//
// 段序与文案对齐 C# BuildInjectPrompt：
//  1. baseRole（空 = 不追加；回退逻辑在调用方：cat-default.cfg baseRole → FallbackBaseRole）
//  2. persona 非空（trim 后）→ CRLF×2 + 「【角色设定】」+ persona（trim）
//  3. injectList 非空 → CRLF×2 + 「【系统前文来源】（清单 cat.cfg）：」→ 每条 CRLF×2 + 「===== label =====」+ CRLF + 正文
//
// 目录条目展开为其一级 *.md（文件名 OrdinalIgnoreCase 升序，显示名 = 去尾斜杠的清单原文 + "/" + 文件名）。
func BuildPrompt(resolve Resolver, readText ReadText, baseRole, persona string, injectList []string, logf Logf) PromptResult {
	var sb strings.Builder
	files := make([]FileResult, 0, len(injectList))
	// [段0] 基础角色段——空 = 无基础角色行
	if baseRole != "" {
		sb.WriteString(baseRole)
	}
	// [段0] 角色段——persona 非空追加
	if trimmed := strings.TrimSpace(persona); trimmed != "" {
		sb.WriteString(newline)
		sb.WriteString(newline)
		sb.WriteString("【角色设定】")
		sb.WriteString(trimmed)
	}
	// [段1] 注入知识——按清单顺序读取；缺失跳过不阻断会话
	if len(injectList) > 0 {
		sb.WriteString(newline)
		sb.WriteString(newline)
		sb.WriteString("【系统前文来源】（清单 cat.cfg）：")
		for _, file := range injectList {
			path, err := resolve(file)
			if err != nil {
				logf.log(2, "注入失败："+file+"（"+err.Error()+"）", "INJECT")
				files = append(files, FileResult{File: file, Status: StatusError, Message: err.Error()})
				continue
			}
			// [段1a] 条目展开——目录：一级 *.md 升序；文件：单条原语义
			var targets []string
			var labels []string
			if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
				entries, readErr := os.ReadDir(path)
				if readErr != nil {
					logf.log(2, "注入失败："+file+"（"+readErr.Error()+"）", "INJECT")
					files = append(files, FileResult{File: file, Status: StatusError, Message: readErr.Error()})
					continue
				}
				names := make([]string, 0, len(entries))
				for _, entry := range entries {
					if entry.IsDir() || !hasMDSuffix(entry.Name()) {
						continue
					}
					names = append(names, entry.Name())
				}
				sort.SliceStable(names, func(i, j int) bool {
					return strings.ToLower(names[i]) < strings.ToLower(names[j])
				})
				prefix := strings.TrimRight(file, "/\\")
				for _, name := range names {
					targets = append(targets, filepath.Join(path, name))
					labels = append(labels, prefix+"/"+name)
				}
				if len(targets) == 0 {
					logf.log(2, "注入缺失："+file+"（目录内无 .md，已跳过）", "INJECT")
					files = append(files, FileResult{File: file, Status: StatusMissing, Message: msgDirEmpty})
					continue
				}
			} else {
				targets = append(targets, path)
				labels = append(labels, file)
			}
			// [段1b] 逐条读取注入——顺序即清单顺序（目录内为文件名升序）
			for idx, target := range targets {
				fr := FileResult{File: labels[idx], Status: StatusError}
				info, statErr := os.Stat(target)
				if statErr != nil || info.IsDir() {
					logf.log(2, "注入缺失："+labels[idx]+"（可选，已跳过）", "INJECT")
					fr.Status = StatusMissing
					fr.Message = msgFileMissing
					files = append(files, fr)
					continue
				}
				content, readErr := readText(target)
				if readErr != nil {
					logf.log(2, "注入失败："+labels[idx]+"（"+readErr.Error()+"）", "INJECT")
					fr.Message = readErr.Error()
					files = append(files, fr)
					continue
				}
				sb.WriteString(newline)
				sb.WriteString(newline)
				sb.WriteString("===== ")
				sb.WriteString(labels[idx])
				sb.WriteString(" =====")
				sb.WriteString(newline)
				sb.WriteString(content)
				fr.Status = StatusOK
				fr.Chars = utf16Length(content)
				fr.Bytes = info.Size()
				files = append(files, fr)
			}
		}
	}
	return PromptResult{Prompt: sb.String(), Files: files}
}

// hasMDSuffix 判定 .md 后缀（大小写不敏感，对齐 Windows 通配 `*.md`）。
func hasMDSuffix(name string) bool {
	return len(name) >= 3 && strings.EqualFold(name[len(name)-3:], ".md")
}

// utf16Length 字符串的 UTF-16 码元数——对齐 C# string.Length（BMP 外按代理对计两码元）。
func utf16Length(s string) int {
	count := 0
	for _, r := range s {
		if r > 0xFFFF {
			count += 2
			continue
		}
		count++
	}
	return count
}

// ToolGroup 注入报告工具组投影——组名 + 组内工具（name / desc）。
type ToolGroup struct {
	// Group 组名（工具归属 Flow 名；空串亦成组，对齐 C# ownerMap 未命中按 "" 归组）
	Group string
	// Tools 组内工具（声明序）
	Tools []ToolSpecBrief
}

// ToolSpecBrief 工具组内的工具简要——name + desc 两字段。
type ToolSpecBrief struct {
	// Name 工具名
	Name string
	// Desc 工具描述（空 = 空串）
	Desc string
}

// BuildToolGroups 按声明序聚合工具组——组序 = 首次出现序，组内序 = 声明序（对齐 C# BuildInjectReportJson）。
func BuildToolGroups(specs []session.ToolSpec, ownerFlow map[string]string) []ToolGroup {
	groups := make([]ToolGroup, 0, 8)
	index := make(map[string]int, 8)
	for _, spec := range specs {
		group := ""
		if ownerFlow != nil {
			if owner, ok := ownerFlow[spec.Name]; ok {
				group = owner
			}
		}
		pos, exists := index[group]
		if !exists {
			index[group] = len(groups)
			groups = append(groups, ToolGroup{Group: group})
			pos = len(groups) - 1
		}
		groups[pos].Tools = append(groups[pos].Tools, ToolSpecBrief{Name: spec.Name, Desc: spec.Description})
	}
	return groups
}

// BuildReportJSON 构建注入报告 JSON——键序对齐 C# BuildInjectReportJson：
// files[] → total → ok → missing → failed → injectCount → mount → mountExpert → mountAuto → mountDesc → toolGroups[]。
//
// files 项五键：file / status / message / chars / bytes；toolGroups 项：group / tools[{name,desc}]。
func BuildReportJSON(files []FileResult, injectList []string, specs []session.ToolSpec, ownerFlow map[string]string, mount string, mountExpert, mountAuto bool, mountDesc string) string {
	var b strings.Builder
	total := 0
	ok := 0
	missing := 0
	failed := 0
	b.WriteString(`{"files":[`)
	for i, f := range files {
		if i > 0 {
			b.WriteString(",")
		}
		switch f.Status {
		case StatusOK:
			ok++
		case StatusMissing:
			missing++
		default:
			failed++
		}
		total++
		b.WriteString(`{"file":`)
		b.WriteString(session.JSONString(f.File))
		b.WriteString(`,"status":`)
		b.WriteString(session.JSONString(f.Status))
		b.WriteString(`,"message":`)
		b.WriteString(session.JSONString(f.Message))
		b.WriteString(`,"chars":`)
		b.WriteString(strconv.Itoa(f.Chars))
		b.WriteString(`,"bytes":`)
		b.WriteString(strconv.FormatInt(f.Bytes, 10))
		b.WriteString("}")
	}
	b.WriteString(`],"total":`)
	b.WriteString(strconv.Itoa(total))
	b.WriteString(`,"ok":`)
	b.WriteString(strconv.Itoa(ok))
	b.WriteString(`,"missing":`)
	b.WriteString(strconv.Itoa(missing))
	b.WriteString(`,"failed":`)
	b.WriteString(strconv.Itoa(failed))
	b.WriteString(`,"injectCount":`)
	b.WriteString(strconv.Itoa(len(injectList)))
	b.WriteString(`,"mount":`)
	b.WriteString(session.JSONString(mount))
	b.WriteString(`,"mountExpert":`)
	b.WriteString(strconv.FormatBool(mountExpert))
	b.WriteString(`,"mountAuto":`)
	b.WriteString(strconv.FormatBool(mountAuto))
	b.WriteString(`,"mountDesc":`)
	b.WriteString(session.JSONString(mountDesc))
	b.WriteString(`,"toolGroups":[`)
	for i, group := range BuildToolGroups(specs, ownerFlow) {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"group":`)
		b.WriteString(session.JSONString(group.Group))
		b.WriteString(`,"tools":[`)
		for k, tool := range group.Tools {
			if k > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"name":`)
			b.WriteString(session.JSONString(tool.Name))
			b.WriteString(`,"desc":`)
			b.WriteString(session.JSONString(tool.Desc))
			b.WriteString("}")
		}
		b.WriteString("]}")
	}
	b.WriteString("]}")
	return b.String()
}
