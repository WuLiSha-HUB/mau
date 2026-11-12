//go:build !windows

package bricks

import (
	"net/http"
	"net/url"
)

// proxyForURL 走标准环境变量代理（非 Windows 无系统代理面）。
func proxyForURL(req *http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment(req)
}
