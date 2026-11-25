// 进程入口（design-viva.md §3.2）：单 exe 双模式分发。
// - 含 --mcp：MCP 模式，不调用任何 Viva app/ui API，不建窗口/托盘/GPU；
// - 无参数：GUI 模式，窗口 + 托盘常驻。
package main

import (
	"os"

	"todo-mcp/app/gui"
	appmcp "todo-mcp/app/mcp"
)

// isMCPMode 判断启动参数是否命中 --mcp（可由入口测试直接验证，不启动 GUI/MCP）。
func isMCPMode(args []string) bool {
	for _, arg := range args {
		if arg == "--mcp" {
			return true
		}
	}
	return false
}

func main() {
	if isMCPMode(os.Args[1:]) {
		os.Exit(appmcp.Run())
	}
	gui.Run()
}
