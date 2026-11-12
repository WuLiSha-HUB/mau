//go:build windows

package main

import (
	"testing"

	"cathome5/host/config"
)

// TestAutoStartOn 配置真值判定——true/1 为开，其余（含空）为关（小写归一）。
func TestAutoStartOn(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{" true ", true},
		{"false", false},
		{"0", false},
		{"", false},
		{"yes", false},
	}
	schema := &config.Schema{}
	for _, testCase := range cases {
		var store config.Store
		if testCase.value != "" {
			store.Set(autoStartKey, testCase.value)
		}
		if got := autoStartOn(&store, schema); got != testCase.want {
			t.Fatalf("autoStartOn(%q) = %v，期望 %v", testCase.value, got, testCase.want)
		}
	}
}

// TestAutoStartOnNilStore nil 存储安全回落 false（托盘初始化早期防御）。
func TestAutoStartOnNilStore(t *testing.T) {
	if autoStartOn(nil, &config.Schema{}) {
		t.Fatal("nil store 应为 false")
	}
}
