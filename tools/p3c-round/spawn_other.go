//go:build !windows

package main

// spawn —— 非 Windows 兜底（本工具只服务本机 Windows 对拍；这里给出可读错误）。

import (
	"errors"
	"time"
)

// launched 占位。
type launched struct {
	name       string
	pid        int
	stdoutPath string
	stderrPath string
}

// launchProcess 非 Windows 不可用。
func launchProcess(name, exe string, args []string, dir string, env []string, outDir string) (*launched, error) {
	return nil, errors.New("p3a-ab 仅支持 Windows（需 CREATE_NO_WINDOW / taskkill）")
}

// stillRunning 占位。
func (l *launched) stillRunning() bool { return false }

// stop 占位。
func (l *launched) stop() error { return nil }

// processEnv 占位。
func processEnv(overrides map[string]string) []string { _ = time.Now; return nil }
