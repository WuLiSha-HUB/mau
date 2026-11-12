//go:build windows

package main

import (
	"os"
	"strings"
	"syscall"
	"unsafe"

	"cathome5/host/config"
	"cathome5/host/observe"
)

// 本文件是开机自启面（对齐 mau:CatHome4/Program.Tray.cs 的 InitAutoStart / SyncAutoStart / CheckAutoStartPoll）：
// 配置 `app.autostart` 为真相，HKCU Run 注册表为物化——启动同步一次 + 运行时 5 s 轮询配置变化。
//
// 边界（与 C# 同规）：
//   - 仅交互模式启用——非交互探针不得改写 HKCU Run 项（数据副本无 app.cfg → 默认 false → 会静默删掉部署区自启项）；
//   - 注册表值名固定 "CatHome5"（C# 用 "CatHome4"——两值并存互不覆盖；值内容 = 当前 exe 全路径，部署实例自引用）；
//   - 失败仅日志告警不崩溃（注册表非关键路径），但失败必须可见。

const (
	// autoStartKey 自启配置键（schema 声明：app.cfg / bool / writable）
	autoStartKey = "app.autostart"
	// autoStartRunPath HKCU Run 键路径
	autoStartRunPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	// autoStartRunName 注册表值名
	autoStartRunName = "CatHome5"
)

const (
	// hkeyCurrentUser HKEY_CURRENT_USER 预定义句柄
	hkeyCurrentUser = 0x80000001
	// keyQueryValue / keySetValue 打开权限
	keyQueryValue = 0x0001
	keySetValue   = 0x0002
	// regSZValue REG_SZ 类型
	regSZValue = 1
	// errorFileNotFound ERROR_FILE_NOT_FOUND（RegDeleteValue / RegQueryValueEx 的「不存在」）
	errorFileNotFound = syscall.Errno(2)
)

var (
	// advapi32DLL 注册表 API 所在 DLL
	advapi32DLL = syscall.NewLazyDLL("advapi32.dll")
	// procRegOpenKeyExW RegOpenKeyExW
	procRegOpenKeyExW = advapi32DLL.NewProc("RegOpenKeyExW")
	// procRegSetValueExW RegSetValueExW
	procRegSetValueExW = advapi32DLL.NewProc("RegSetValueExW")
	// procRegDeleteValueW RegDeleteValueW
	procRegDeleteValueW = advapi32DLL.NewProc("RegDeleteValueW")
	// procRegQueryValueExW RegQueryValueExW
	procRegQueryValueExW = advapi32DLL.NewProc("RegQueryValueExW")
	// procRegCloseKey RegCloseKey
	procRegCloseKey = advapi32DLL.NewProc("RegCloseKey")
)

// autoStartOn 读配置判断自启是否开启（托盘勾选态唯一源）。
func autoStartOn(store *config.Store, schema *config.Schema) bool {
	if store == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(store.Resolve(schema, autoStartKey).Value))
	return value == "true" || value == "1"
}

// syncAutoStart 按配置同步注册表并返回状态描述（日志用）。
// true → 写入 "\"<exe 全路径>\""；false → 删除值（不存在视为成功——幂等）。
func syncAutoStart(store *config.Store, schema *config.Schema) string {
	if autoStartOn(store, schema) {
		exe, err := os.Executable()
		if err != nil {
			return "自启同步失败：取可执行路径失败：" + err.Error()
		}
		if err := writeRunValue(`"` + exe + `"`); err != nil {
			return "自启同步失败：" + err.Error()
		}
		return "自启同步: true | " + exe
	}
	if err := deleteRunValue(); err != nil {
		return "自启同步失败：" + err.Error()
	}
	return "自启同步: false"
}

// handleAutoStartToggle 自启切换（托盘点击 → 主线程执行）——配置写 + 注册表同步。
func handleAutoStartToggle(store *config.Store, schema *config.Schema, logs *observe.LogStore) {
	target := "true"
	if autoStartOn(store, schema) {
		target = "false"
	}
	if ok, reason := store.SetChecked(autoStartKey, target, schema); !ok {
		logs.Add("CatHome5", observe.LevelWarn, "自启设置失败："+reason, "AUTOSTART", "", "", 0, 0, false)
		return
	}
	logs.Add("CatHome5", observe.LevelInfo, syncAutoStart(store, schema), "AUTOSTART", "", "", 0, 0, false)
}

// openRunKey 打开 HKCU Run 键（读写权限）。
func openRunKey() (syscall.Handle, error) {
	var handle syscall.Handle
	subkey, err := syscall.UTF16PtrFromString(autoStartRunPath)
	if err != nil {
		return 0, err
	}
	ret, _, _ := procRegOpenKeyExW.Call(
		uintptr(hkeyCurrentUser),
		uintptr(unsafe.Pointer(subkey)),
		0,
		uintptr(keyQueryValue|keySetValue),
		uintptr(unsafe.Pointer(&handle)),
	)
	if ret != 0 {
		return 0, syscall.Errno(ret)
	}
	return handle, nil
}

// writeRunValue 写 Run 值（REG_SZ，UTF-16，含结尾 NUL）。
func writeRunValue(value string) error {
	handle, err := openRunKey()
	if err != nil {
		return err
	}
	defer procRegCloseKey.Call(uintptr(handle))
	name, err := syscall.UTF16PtrFromString(autoStartRunName)
	if err != nil {
		return err
	}
	data, err := syscall.UTF16FromString(value)
	if err != nil {
		return err
	}
	ret, _, _ := procRegSetValueExW.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(name)),
		0,
		regSZValue,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)*2),
	)
	if ret != 0 {
		return syscall.Errno(ret)
	}
	return nil
}

// readRunValue 读 Run 值（启动读回自证用；不存在返回 ok=false）。
func readRunValue() (string, bool, error) {
	handle, err := openRunKey()
	if err != nil {
		return "", false, err
	}
	defer procRegCloseKey.Call(uintptr(handle))
	name, err := syscall.UTF16PtrFromString(autoStartRunName)
	if err != nil {
		return "", false, err
	}
	buf := make([]uint16, 512)
	size := uint32(len(buf) * 2)
	ret, _, _ := procRegQueryValueExW.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(name)),
		0, 0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret == uintptr(errorFileNotFound) {
		return "", false, nil
	}
	if ret != 0 {
		return "", false, syscall.Errno(ret)
	}
	n := int(size) / 2
	if n > 0 && buf[n-1] == 0 {
		n--
	}
	return syscall.UTF16ToString(buf[:n]), true, nil
}

// deleteRunValue 删除 Run 值（不存在视为成功——幂等）。
func deleteRunValue() error {
	handle, err := openRunKey()
	if err != nil {
		return err
	}
	defer procRegCloseKey.Call(uintptr(handle))
	name, err := syscall.UTF16PtrFromString(autoStartRunName)
	if err != nil {
		return err
	}
	ret, _, _ := procRegDeleteValueW.Call(uintptr(handle), uintptr(unsafe.Pointer(name)))
	if ret != 0 && ret != uintptr(errorFileNotFound) {
		return syscall.Errno(ret)
	}
	return nil
}

// autoStartPoller 自启配置轮询——启动同步一次 + 5 s 粒度对比配置值（覆盖 config-* / 前端写路径）。
type autoStartPoller struct {
	// last 上次同步时的配置值（小写归一——true/1 两种真值写法不触发反复同步）
	last string
}

// init 启动同步——记录配置快照 + 同步注册表一次 + 读回自证。
func (p *autoStartPoller) init(store *config.Store, schema *config.Schema) string {
	p.last = strings.ToLower(strings.TrimSpace(store.Resolve(schema, autoStartKey).Value))
	note := syncAutoStart(store, schema)
	value, exists, err := readRunValue()
	if err != nil {
		return note + " | 读回失败：" + err.Error()
	}
	if exists {
		return note + " | 注册表值：" + value
	}
	return note + " | 注册表值：（无）"
}

// poll 轮询配置变化——变化则重新同步；返回状态描述（空 = 无变化）。
func (p *autoStartPoller) poll(store *config.Store, schema *config.Schema) string {
	value := strings.ToLower(strings.TrimSpace(store.Resolve(schema, autoStartKey).Value))
	if value == p.last {
		return ""
	}
	p.last = value
	return syncAutoStart(store, schema)
}
