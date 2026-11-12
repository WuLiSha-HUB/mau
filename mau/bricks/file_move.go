package bricks

import (
	"cathome5/mau/fs"
)

// FileMove 复现 file-move——移动 / 重命名（文件与目录均支持；目标已存在拒绝）。
func FileMove(svc *fs.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "src dest", "src dest", "", ""); bad != "" {
		return bad, false
	}
	src := Get(argsJSON, "src")
	dest := Get(argsJSON, "dest")
	if src == ParseFail || dest == ParseFail {
		return badParse, false
	}
	if src == "" || dest == "" {
		return "ERR|BAD_ARGS|缺少参数 src 或 dest", false
	}
	if svc == nil {
		return "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService", false
	}
	if err := svc.Move(src, dest); err != nil {
		return fs.ErrText(err), false
	}
	return MetaHead("file-move", true, src, -1) + "\n" + src + " → " + dest, true
}
