// Package mcp 实现 MCP stdio 模式（design-viva.md §6/§8.1）。
//
// stdout 是 JSON-RPC 协议通道，本包日志一律走 stderr；不调用任何 GUI API。
package mcp

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"todo-mcp/app/store"
)

// Run 启动 stdio MCP server，返回进程退出码。
// 进程生命周期由宿主 agent 管理：stdin EOF → Run 返回 → 进程退出。
func Run() int {
	logger := log.New(os.Stderr, "[todo-mcp] ", log.LstdFlags)

	db, err := store.Open()
	if err != nil {
		logger.Printf("打开数据库失败: %v", err)
		return 1
	}
	defer db.Close()

	server := mcp.NewServer(&mcp.Implementation{Name: "todo", Version: "v0.1.0"}, nil)
	registerTools(server, store.NewTaskRepo(db))

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		logger.Printf("MCP 服务异常退出: %v", err)
		return 1
	}
	return 0
}
