package admin

import (
	"os"
	"strings"
	"unicode"
)

// 本文件是「上工 / 开工 <项目>」消息的挂载意图解析——机械解析，不调模型、不猜语义。
//
// 权威实现 = `mau:CatHome4/CatHome4.Admin/AdminService.MountIntent.cs`：触发词、名字段边界、
// 六路候选顺序、别名表、模糊命中口径逐条对齐（语料基线 = 1389 份会话留档首条用户消息：
// 完全等于目录名 48% · 别名 35% · 包含关系 7.6% · 完全对不上 8.2%）。

// mountTriggers 挂载触发词——语料里两种写法都高频出现（上工 609 条 / 开工大量）。
var mountTriggers = []string{"上工", "开工"}

// mountAlias 别名表——语料取证的高频简称，键忽略大小写；值为 Project 目录名。
var mountAlias = map[string]string{
	"kkm":               "KKManager",
	"kkcard":            "KKManager",
	"kk":                "KKManager",
	"kmr":               "KKManager",
	"apis":              "ApiSiteAnalyzer",
	"work":              "ApiSiteAnalyzer",
	"workbuddy":         "ApiSiteAnalyzer",
	"workbuddy-manager": "ApiSiteAnalyzer",
	"clash":             "ClashVerge",
	"clashverge":        "ClashVerge",
	"ourcosm":           "Cosmoteer",
	"cgss":              "CGSS-DMM",
	"starlet":           "StarlitSeason",
	"cgse":              "CGSE_CORE",
	"kkfetch":           "KkFetch",
	"qqbotb":            "QQBotB",
	"ch4":               "CH4",
	"mau":               "Mau",
	"rea":               "REA",
	"acs":               "ACS",
	"hime":              "Hime",
	"psd2live":          "PSD2Live",
	"vault":             "Vault",
	"ecc":               "ECC",
	"trad":              "TradingView",
	"sc2":               "SC2Replay",
	"tsuru":             "TsuruPeta",
	"协商知识库":             "协商知识库",
	"boss直聘":            "BOSS直聘",
}

// mountNameCut 名字段之后视为结束的字符集——空白另判（unicode.IsSpace），其余为句读。
const mountNameCut = "，,。！!？?、；;"

// mountTrimCut 名字 token 两端应剥掉的引号与标点（对齐 C# ResolveMountName 的 Trim 字符集）。
const mountTrimCut = "`\"'。，.：:、（(）)【】"

// mountSplitCut 多项目分隔符——「和 / + / 、 / & / 以及」（顺序影响切分，逐条对齐 C#）。
var mountSplitCut = []string{"和", "+", "、", "&", "以及"}

// MountIntent 挂载意图——用户消息里「上工 / 开工 <项目>」的机械解析结果。
type MountIntent struct {
	// Raw 触发词后的原始 token（未解析前）
	Raw string
	// Project 解析出的主项目名（空 = 未解析）
	Project string
	// Source 解析来源——exact / job / tool / workspace / alias / fuzzy / miss
	Source string
	// Extras 同一条消息里识别到的其余项目（参考项目，本轮只记录不挂载）
	Extras []string
}

// parseMountIntent 解析挂载意图——首行内找触发词，取其后的名字段并解析成项目名。
//
// 无触发词 / 名字为空（如「上工。任务卡：…」）返回空意图，调用方按普通消息处理。
func (d *Deps) parseMountIntent(content string) MountIntent {
	result := MountIntent{Extras: []string{}}
	all := strings.TrimSpace(content)
	if all == "" {
		return result
	}
	// 只看首行——正文里偶然出现的「上工」不算意图
	head := all
	if cut := strings.IndexAny(head, "\r\n"); cut >= 0 {
		head = head[:cut]
	}
	at := -1
	trigger := ""
	for _, word := range mountTriggers {
		pos := strings.Index(head, word)
		if pos >= 0 && (at < 0 || pos < at) {
			at = pos
			trigger = word
		}
	}
	if at < 0 {
		return result
	}
	after := strings.TrimLeft(head[at+len(trigger):], " \t"+mountNameCut+"：:（(")
	token := firstNameSegment(after)
	if token == "" {
		// 「上工，继续」「上工。任务卡：…」——触发词后没有名字段：不算意图，不动挂载
		return result
	}
	extras := []string{}
	for _, rawPiece := range splitMountPieces(token) {
		piece := strings.TrimSpace(rawPiece)
		if piece == "" {
			continue
		}
		name, source, ok := d.resolveMountName(piece)
		if ok {
			if result.Project == "" {
				result.Project = name
				result.Source = source
				result.Raw = piece
			} else if !strings.EqualFold(name, result.Project) {
				extras = append(extras, name)
			}
			continue
		}
		if result.Raw == "" {
			result.Raw = piece
			result.Source = "miss"
		}
	}
	result.Extras = extras
	return result
}

// firstNameSegment 取触发词后的第一个名字段——遇空白 / 句读即止；冒号与斜杠保留（路径形态在解析器里取末段）。
func firstNameSegment(text string) string {
	end := len(text)
	for index, char := range text {
		if unicode.IsSpace(char) || strings.ContainsRune(mountNameCut, char) {
			end = index
			break
		}
	}
	return strings.TrimSpace(text[:end])
}

// splitMountPieces 按多项目分隔符切分名字段——逐分隔符切，空段由调用方丢弃（对齐 C# Split + RemoveEmptyEntries）。
func splitMountPieces(token string) []string {
	pieces := []string{token}
	for _, cut := range mountSplitCut {
		next := make([]string, 0, len(pieces))
		for _, piece := range pieces {
			next = append(next, strings.Split(piece, cut)...)
		}
		pieces = next
	}
	return pieces
}

// resolveMountName 把单个名字 token 解析成 Project 目录名——六路候选，命中即返回。
func (d *Deps) resolveMountName(token string) (string, string, bool) {
	text := strings.TrimSpace(token)
	text = strings.Trim(text, mountTrimCut)
	if text == "" {
		return "", "miss", false
	}
	// 路径 / 受控根形态（tools/REA · workspace:QQBotB · ccbp:Project/Mau）——取末段
	if cut := strings.LastIndexAny(text, "/\\:"); cut >= 0 && cut+1 < len(text) {
		text = text[cut+1:]
	}
	if text == "" {
		return "", "miss", false
	}
	if real, ok := d.dirHit("ccbp:Project", text); ok {
		return real, "exact", true
	}
	if real, ok := d.dirHit("ccbp:L2/Job", text); ok {
		return real, "job", true
	}
	if real, ok := d.dirHit("workspace:tools", text); ok {
		return real, "tool", true
	}
	if real, ok := d.dirHit("workspace", text); ok {
		return real, "workspace", true
	}
	if alias, ok := mountAlias[strings.ToLower(text)]; ok {
		if real, hit := d.dirHit("ccbp:Project", alias); hit {
			return real, "alias", true
		}
	}
	return d.fuzzyHit("ccbp:Project", text)
}

// dirHit 目录名精确命中（忽略大小写）——返回真实目录名，保证后续路径拼接大小写正确。
func (d *Deps) dirHit(rootSpec, name string) (string, bool) {
	dir := d.resolveSpec(rootSpec)
	if dir == "" || name == "" {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		d.logInject(2, "挂载解析枚举失败："+rootSpec+" · "+err.Error())
		return "", false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.EqualFold(entry.Name(), name) {
			return entry.Name(), true
		}
	}
	return "", false
}

// fuzzyHit 唯一模糊命中——token 与项目名互相包含（如 apisite → ApiSiteAnalyzer）。
//
// 只有唯一候选才算命中：多候选即判未解析，交给人（猜错的代价是挂错项目、前文全错）。
func (d *Deps) fuzzyHit(rootSpec, name string) (string, string, bool) {
	if len([]rune(name)) < 3 {
		return "", "miss", false
	}
	dir := d.resolveSpec(rootSpec)
	if dir == "" {
		return "", "miss", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		d.logInject(2, "挂载解析模糊匹配失败："+rootSpec+" · "+err.Error())
		return "", "miss", false
	}
	lower := strings.ToLower(name)
	hit := ""
	hits := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		lowered := strings.ToLower(entry.Name())
		if strings.Contains(lowered, lower) || strings.Contains(lower, lowered) {
			hit = entry.Name()
			hits++
		}
	}
	if hits != 1 {
		return "", "miss", false
	}
	return hit, "fuzzy", true
}
