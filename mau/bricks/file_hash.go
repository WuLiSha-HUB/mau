package bricks

import (
	"strconv"
	"strings"

	"cathome5/mau/fs"
)

// FileHash 复现 file-hash——计算文件 SHA256 摘要（大小 + 耗时随行）。
func FileHash(svc *fs.Service, argsJSON string) (string, bool) {
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
	facts, err := svc.HashFacts(path)
	if err != nil {
		return fs.ErrText(err), false
	}
	// [展示层] 文案与单位换算归积木层（A228 档 2 · B3）
	var sb strings.Builder
	sb.WriteString("路径: " + facts.ResolvedPath)
	sb.WriteString("\n算法: " + facts.Algorithm)
	sb.WriteString("\n大小: " + groupDigits(facts.SizeBytes) + " 字节")
	sb.WriteString("\n摘要: " + facts.Hash)
	sb.WriteString("\n耗时: " + strconv.FormatInt(facts.ElapsedMs, 10) + " ms")
	return MetaHead("file-hash", true, path, -1) + "\n" + path + " | " + facts.Algorithm + " " + facts.Hash + "\n" + sb.String(), true
}
