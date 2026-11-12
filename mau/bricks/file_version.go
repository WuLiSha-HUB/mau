package bricks

import (
	"strconv"
	"strings"

	"cathome5/mau/fs"
)

// FileVersion 复现 file-version——读取 PE 文件版本信息（版本三件 + 修改时间 + 大小；非 PE / 无版本资源明确报错）。
func FileVersion(svc *fs.Service, argsJSON string) (string, bool) {
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
	facts, err := svc.VersionFacts(path)
	if err != nil {
		return fs.ErrText(err), false
	}
	var sb strings.Builder
	// [展示层] 文案与回落注记归积木层（A228 档 2 · B3）
	sb.WriteString("路径: " + facts.ResolvedPath)
	sb.WriteString("\n版本: " + facts.Version)
	if facts.FallbackUsed {
		sb.WriteString("（ProductVersion 为空——回落 FileVersion）")
	}
	sb.WriteString("\nFileVersion: " + orNone(facts.FileVersion))
	sb.WriteString("\nProductVersion: " + orNone(facts.ProductVersion))
	sb.WriteString("\n修改时间: " + facts.LastWrite.Format("2006-01-02 15:04:05"))
	sb.WriteString("\n大小: " + strconv.FormatInt(facts.SizeBytes, 10) + " 字节")
	return MetaHead("file-version", true, path, -1) + "\n" + path + " | " + sb.String(), true
}

// orNone 空串显示为「(无)」。
func orNone(value string) string {
	if value == "" {
		return "(无)"
	}
	return value
}
