package bricks

import (
	"strconv"

	"cathome5/mau/fs"
)

// FileDelete 复现 file-delete——软删除到回收站（文件与目录均支持；目录=整棵子树）。
func FileDelete(svc *fs.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path", "path", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	if path == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path", false
	}
	if svc == nil {
		return "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService", false
	}
	outcome, err := svc.Recycle(path)
	if err != nil {
		return fs.ErrText(err), false
	}
	return MetaHead("file-delete", true, path, -1) + "\n" + path + " → " + outcome.Target + formatRecycleStats(outcome), true
}

// formatRecycleStats 内容统计文本——文件=体积；目录=文件数 + 子目录数 + 合计体积。
func formatRecycleStats(outcome fs.RecycleOutcome) string {
	if !outcome.IsDirectory {
		return "（文件 · " + sizeText(outcome.TotalBytes) + "）"
	}
	return "（目录 · 文件 " + strconv.Itoa(outcome.FileCount) + " / 子目录 " + strconv.Itoa(outcome.DirectoryCount) + " / 合计 " + sizeText(outcome.TotalBytes) + "）"
}

// sizeText 体积自适应文本——B / KB / MB / GB（整数运算，四舍五入，无小数文化差异）。
func sizeText(n int64) string {
	switch {
	case n < 1024:
		return strconv.FormatInt(n, 10) + " B"
	case n < 1024*1024:
		return strconv.FormatInt((n+512)/1024, 10) + " KB"
	case n < 1024*1024*1024:
		return strconv.FormatInt((n+512*1024)/(1024*1024), 10) + " MB"
	default:
		return strconv.FormatInt((n+512*1024*1024)/(1024*1024*1024), 10) + " GB"
	}
}
