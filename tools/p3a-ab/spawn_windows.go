//go:build windows

package main

// spawn —— 进程启动与停止（Windows 专用：隐藏控制台 + 整棵进程树终止）。
//
// 边界：本工具只起「对拍基线实例」（CAT5 / C# 各一份），不参与宿主重启链——
// 宿主自身的重启走 restart-*，这里起的是独立数据根的临时进程。

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// createNoWindow 是 CREATE_NO_WINDOW（GUI 程序也隐藏控制台窗口）。
const createNoWindow = 0x08000000

// launched 是一份已启动进程的句柄与落盘路径。
type launched struct {
	name       string
	cmd        *exec.Cmd
	pid        int
	stdoutPath string
	stderrPath string
	done       chan error
}

// launchProcess 起一个进程并把 stdout/stderr 落到文件。
func launchProcess(name, exe string, args []string, dir string, env []string, outDir string) (*launched, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	stdoutPath := outDir + string(os.PathSeparator) + name + ".stdout.log"
	stderrPath := outDir + string(os.PathSeparator) + name + ".stderr.log"
	outFile, err := os.Create(stdoutPath)
	if err != nil {
		return nil, err
	}
	errFile, err := os.Create(stderrPath)
	if err != nil {
		_ = outFile.Close()
		return nil, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = outFile
	cmd.Stderr = errFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	if err := cmd.Start(); err != nil {
		_ = outFile.Close()
		_ = errFile.Close()
		return nil, err
	}
	l := &launched{
		name:       name,
		cmd:        cmd,
		pid:        cmd.Process.Pid,
		stdoutPath: stdoutPath,
		stderrPath: stderrPath,
		done:       make(chan error, 1),
	}
	go func() {
		err := cmd.Wait()
		_ = outFile.Close()
		_ = errFile.Close()
		l.done <- err
	}()
	return l, nil
}

// stillRunning 判定进程是否仍在跑（非阻塞）。
func (l *launched) stillRunning() bool {
	select {
	case <-l.done:
		return false
	default:
		return true
	}
}

// stop 终止整棵进程树（taskkill /T /F），并等待退出。
func (l *launched) stop() error {
	if l == nil || l.cmd == nil || l.cmd.Process == nil {
		return nil
	}
	if !l.stillRunning() {
		return nil
	}
	kill := exec.Command("taskkill", "/PID", strconv.Itoa(l.pid), "/T", "/F")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	_ = kill.Run()
	select {
	case <-l.done:
		return nil
	case <-time.After(15 * time.Second):
		return fmt.Errorf("%s 进程 %d 未在 15s 内退出", l.name, l.pid)
	}
}

// processEnv 组装子进程环境（继承 + 覆盖）。
func processEnv(overrides map[string]string) []string {
	env := os.Environ()
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}
