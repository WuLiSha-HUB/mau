//go:build windows

package bricks

import (
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"unsafe"
)

// internetSettingsKey 是 WinINET 代理设置的注册表路径（当前用户）。
const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// proxyForURL 返回本次请求应走的代理；nil = 直连。
//
// 取值顺序：环境变量（HTTPS_PROXY 等，Go 惯例）→ Windows 系统代理（对齐 .NET HttpClient 的默认面）。
func proxyForURL(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	if fromEnv, err := http.ProxyFromEnvironment(req); err == nil && fromEnv != nil {
		return fromEnv, nil
	}
	server, override, enabled := readSystemProxy()
	if !enabled || server == "" {
		return nil, nil
	}
	if bypassMatch(req.URL.Hostname(), override) {
		return nil, nil
	}
	address := proxyServerFor(server, req.URL.Scheme)
	if address == "" {
		return nil, nil
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return nil, nil
	}
	return parsed, nil
}

// readSystemProxy 读 WinINET 代理设置——开关 / 服务器 / 绕过清单；读不到按未启用处理。
func readSystemProxy() (server string, override string, enabled bool) {
	key, err := openRegistryKey(internetSettingsKey)
	if err != nil {
		return "", "", false
	}
	defer func() { _ = syscall.RegCloseKey(key) }()
	enabled = readRegistryDWORD(key, "ProxyEnable") != 0
	server = readRegistryString(key, "ProxyServer")
	override = readRegistryString(key, "ProxyOverride")
	return server, override, enabled
}

// openRegistryKey 打开 HKCU 下的注册表键。
func openRegistryKey(path string) (syscall.Handle, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var key syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, pathPtr, 0, syscall.KEY_READ, &key); err != nil {
		return 0, err
	}
	return key, nil
}

// readRegistryString 读取字符串值；不存在 / 类型不符返回空串。
func readRegistryString(key syscall.Handle, name string) string {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return ""
	}
	var valueType, size uint32
	if err := syscall.RegQueryValueEx(key, namePtr, nil, &valueType, nil, &size); err != nil || size == 0 {
		return ""
	}
	buffer := make([]uint16, size/2+1)
	if err := syscall.RegQueryValueEx(key, namePtr, nil, &valueType, (*byte)(unsafe.Pointer(&buffer[0])), &size); err != nil {
		return ""
	}
	return syscall.UTF16ToString(buffer)
}

// readRegistryDWORD 读取 32 位数值；不存在返回 0。
func readRegistryDWORD(key syscall.Handle, name string) uint32 {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	var valueType, size uint32 = 0, 4
	buffer := make([]byte, 4)
	if err := syscall.RegQueryValueEx(key, namePtr, nil, &valueType, &buffer[0], &size); err != nil || size != 4 {
		return 0
	}
	return *(*uint32)(unsafe.Pointer(&buffer[0]))
}

// proxyServerFor 从 ProxyServer 值取指定协议的服务地址（支持 `http=host:port;https=host:port` 形态）。
func proxyServerFor(server, scheme string) string {
	if !strings.Contains(server, "=") {
		return strings.TrimSpace(server)
	}
	for _, part := range strings.Split(server, ";") {
		pieces := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pieces) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(pieces[0]), scheme) {
			return strings.TrimSpace(pieces[1])
		}
	}
	return ""
}

// bypassMatch 判定主机是否命中 ProxyOverride 绕过清单。
//
// 支持三种形态：`*.suffix` 后缀 / `prefix.*` 前缀 / 精确域名（含子域）；`<local>` = 无点主机名。
func bypassMatch(host, override string) bool {
	lowered := strings.ToLower(strings.TrimSpace(host))
	if lowered == "" || override == "" {
		return false
	}
	for _, raw := range strings.Split(override, ";") {
		pattern := strings.ToLower(strings.TrimSpace(raw))
		switch {
		case pattern == "":
			continue
		case pattern == "<local>":
			if !strings.Contains(lowered, ".") {
				return true
			}
		case strings.HasPrefix(pattern, "*"):
			if strings.HasSuffix(lowered, pattern[1:]) {
				return true
			}
		case strings.HasSuffix(pattern, "*"):
			if strings.HasPrefix(lowered, pattern[:len(pattern)-1]) {
				return true
			}
		case lowered == pattern || strings.HasSuffix(lowered, "."+pattern):
			return true
		}
	}
	return false
}
