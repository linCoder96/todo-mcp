package main

import "testing"

// 入口参数分发测试：只验证分支判断，不启动 GUI 或 MCP server
// （design-viva.md §8.1 main_test.go 约定）。
func TestIsMCPMode(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"--mcp"}, true},
		{[]string{"--other", "--mcp"}, true},
		{[]string{"--mcp=false"}, false},
	}
	for _, tc := range cases {
		if got := isMCPMode(tc.args); got != tc.want {
			t.Errorf("isMCPMode(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
