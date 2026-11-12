package session

import "strings"

// timeback 域类型——`type` → 域内可用工具白名单（fail-closed）。
//
// 语义：域开着时只有本 type 名单内的工具可用；名单外一律拒（ERR|TIMEBACK_PROFILE，不静默）。
// 与「域外专属集」（image-inject / browser-* 域外不可用）是两个正交维度：
// 本表只决定「域内给不给」；域外专属集决定「域外给不给」——两者不可合并。
const (
	// TimebackBrowserVision 域类型·浏览器——浏览网页取证（页面原文 / 截图 / 取 DOM / 页签）
	TimebackBrowserVision = "browser_vision"
	// TimebackImageVision 域类型·识图——直读原图取证
	TimebackImageVision = "image_vision"
	// TimebackTextSearch 域类型·文本检索——文本 / 文档只读检索
	TimebackTextSearch = "text_search"
	// TimebackCodeReview 域类型·代码审查——基础读面 + cs 只读面（纯取证：不含跑测面）
	TimebackCodeReview = "code_review"
	// TimebackCodeWrite 域类型·代码实现——读 / 改 / 编译 / 跑测一域内完成
	TimebackCodeWrite = "code_write"
)

// 回执类别——决定 findings 骨架形态。
const (
	// TimebackKindReview 审查类——findings 给「位置」清单
	TimebackKindReview = "review"
	// TimebackKindWrite 实现类——findings 给「变更 + 可复跑跑测」
	TimebackKindWrite = "write"
	// TimebackKindImage 识图类——findings 给「ASCII 图证 + 结论」
	TimebackKindImage = "image"
)

// textReadNames 文本读面——被多个 type 复用。
var textReadNames = []string{"text-read", "text-read_lines", "text-read_between", "text-grep"}

// fileShapeNames 文件结构面——目录 / 文件名 / 产物版本 / 路径状态。
var fileShapeNames = []string{"file-tree", "file-find", "file-version", "file-stat"}

// readBaseNames 基础读面 = 文本读面 + 文件结构面。
var readBaseNames = concatNames(textReadNames, fileShapeNames)

// browserVisionNames browser_vision 名单——浏览器族五件 + 基础读面。
var browserVisionNames = concatNames([]string{
	"browser-open", "browser-read", "browser-eval", "browser-shot", "browser-tabs",
}, readBaseNames)

// imageVisionNames image_vision 名单——识图两件 + 基础读面。
var imageVisionNames = concatNames([]string{"image-inject", "image-analyze"}, readBaseNames)

// textSearchNames text_search 名单——基础读面。
var textSearchNames = readBaseNames

// codeReviewNames code_review 名单——基础读面 + cs 只读七件。
var codeReviewNames = concatNames(readBaseNames, []string{
	"cs-check", "cs-list", "cs-read", "cs-find", "cs-find_ref", "cs-dead", "cs-comment_check",
})

// codeWriteNames code_write 名单——基础读面 + cs 全套十三件 + 文本写面 + 语料检查。
var codeWriteNames = concatNames([]string{
	"cs-check", "cs-list", "cs-read", "cs-patch", "cs-member", "cs-comment",
	"cs-find", "cs-find_ref", "cs-dead", "cs-comment_check", "cs-format", "cs-build", "cs-test",
	"text-replace", "text-write", "text-append",
	"mau-verify",
}, readBaseNames)

// TimebackTypes 全部域类型——枚举顺序即 start 回执的枚举展示序。
func TimebackTypes() []string {
	return []string{TimebackBrowserVision, TimebackImageVision, TimebackTextSearch, TimebackCodeReview, TimebackCodeWrite}
}

// TimebackKind 域类型的回执类别。
func TimebackKind(domainType string) string {
	if domainType == TimebackCodeWrite {
		return TimebackKindWrite
	}
	if domainType == TimebackImageVision {
		return TimebackKindImage
	}
	return TimebackKindReview
}

// TimebackTypeValid 类型合法性——开锚参数面校验。
func TimebackTypeValid(domainType string) bool {
	return containsName(TimebackTypes(), domainType)
}

// TimebackTools 类型 → 域内可用工具名单（未知名 = 空）。
func TimebackTools(domainType string) []string {
	switch domainType {
	case TimebackBrowserVision:
		return browserVisionNames
	case TimebackImageVision:
		return imageVisionNames
	case TimebackTextSearch:
		return textSearchNames
	case TimebackCodeReview:
		return codeReviewNames
	case TimebackCodeWrite:
		return codeWriteNames
	}
	return nil
}

// TimebackAllows 域内可用性判定（fail-closed）。
func TimebackAllows(domainType, toolName string) bool {
	if toolName == "" {
		return false
	}
	return containsName(TimebackTools(domainType), toolName)
}

// TimebackNeedsBrowser 浏览器预热判据——名单含浏览器族才预热实例。
func TimebackNeedsBrowser(domainType string) bool {
	return domainType == TimebackBrowserVision
}

// TimebackTypeListText 类型枚举展示文本（" | " 分隔）。
func TimebackTypeListText() string {
	return strings.Join(TimebackTypes(), " | ")
}

// TimebackUnknown 对账——白名单内不在工具池的名字（拼写漂移 / 工具退役后漏改）。
func TimebackUnknown(poolNames []string) []string {
	missing := make([]string, 0, 4)
	for _, domainType := range TimebackTypes() {
		for _, name := range TimebackTools(domainType) {
			if !containsName(poolNames, name) && !containsName(missing, name) {
				missing = append(missing, name)
			}
		}
	}
	return missing
}

// concatNames 拼合两个名单（基础读面复用——避免同一串名字多处手写漂移）。
func concatNames(first, second []string) []string {
	all := make([]string, 0, len(first)+len(second))
	all = append(all, first...)
	all = append(all, second...)
	return all
}

// containsName 名单线性查找（名单规模小，线性查找免建索引）。
func containsName(names []string, name string) bool {
	for _, item := range names {
		if item == name {
			return true
		}
	}
	return false
}
