package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// spawnConsoleWrappers 是「已隐藏控制台」的封装函数名——函数体内调用其一即视为已遮盖。
var spawnConsoleWrappers = map[string]bool{
	"prepareBrowserCommand": true,
}

// spawnConsoleAllowlist 允许保留默认创建行为的文件（被调程序是 GUI 子系统，不分配控制台）。
var spawnConsoleAllowlist = map[string]string{
	filepath.Join("host", "admin", "media.go"): "explorer.exe 是 GUI 程序，本身不分配控制台",
}

// TestSpawnHidesConsole 是「控制台子进程」门禁。
//
// 判据：扫描 cmd / host / mau 三棵源码树，每个 exec.Command / exec.CommandContext 调用点所在的
// 函数体内必须出现 SysProcAttr 赋值（或调用已知封装）。缺失即 FAIL。
//
// 为什么是门禁而不是纪律：cathome5.exe 是 GUI 子系统（无控制台），Windows 会为每个未隐藏的
// 控制台子进程分配一个**可见**控制台窗口——而 stdout 已被管道吞掉，用户只看到「黑框一闪、
// 什么都没有」。go test 会按包连弹（21 包 ≈ 21 个），tasklist 判活每 500ms 一个。
// 逐处补 SysProcAttr 是人工的，漏一处就复发——所以把它钉在编译面（go test 即红）。
func TestSpawnHidesConsole(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("解析仓库根失败: %v", err)
	}
	var violations []string
	for _, dir := range []string{"cmd", "host", "mau"} {
		base := filepath.Join(root, dir)
		walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				switch d.Name() {
				case "raw", "node_modules", ".git", "testdata":
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if _, ok := spawnConsoleAllowlist[rel]; ok {
				return nil
			}
			violations = append(violations, scanSpawnConsole(path)...)
			return nil
		})
		if walkErr != nil {
			t.Fatalf("扫描 %s 失败: %v", dir, walkErr)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("发现未隐藏控制台的子进程启动点（GUI 宿主会弹黑框）：\n%s", strings.Join(violations, "\n"))
	}
}

// scanSpawnConsole 扫单个文件——返回缺失遮盖的调用点（文件:行 函数名）。
func scanSpawnConsole(path string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return []string{path + ": 解析失败: " + err.Error()}
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		if !containsExecCommand(fn.Body) || bodyHasConsoleCover(fn.Body) {
			return true
		}
		pos := fset.Position(fn.Pos())
		out = append(out, fmt.Sprintf("%s:%d %s（缺 SysProcAttr HideWindow+CREATE_NO_WINDOW）", path, pos.Line, fn.Name.Name))
		return true
	})
	return out
}

// containsExecCommand 判断函数体内是否调用了 exec.Command / exec.CommandContext。
func containsExecCommand(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "exec" {
			return true
		}
		if sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext" {
			found = true
			return false
		}
		return true
	})
	return found
}

// bodyHasConsoleCover 判断函数体内是否已隐藏控制台——SysProcAttr 赋值或已知封装调用。
func bodyHasConsoleCover(body *ast.BlockStmt) bool {
	covered := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if node.Sel.Name == "SysProcAttr" {
				covered = true
				return false
			}
		case *ast.CallExpr:
			if id, ok := node.Fun.(*ast.Ident); ok && spawnConsoleWrappers[id.Name] {
				covered = true
				return false
			}
		}
		return true
	})
	return covered
}
