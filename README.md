# todo-mcp

单 exe 双模式的 Windows 桌面待办应用：**GUI 给人用，MCP 给 AI 用，两边读写同一份 SQLite 数据**。你在窗口里看到的任务，AI 客户端（Claude Desktop / Cursor / ZCode 等）通过 MCP 也能查到、能建、能改；AI 建的任务约 2 秒内自动出现在窗口里。

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Platform](https://img.shields.io/badge/Windows-10%2B-0078D6?logo=windows&logoColor=white)
![UI](https://img.shields.io/badge/UI-Viva%20GPU%20%E5%8E%9F%E7%94%9F%E7%BB%98%E5%88%B6-4C8BF5)
![Version](https://img.shields.io/badge/version-v0.1.0-green)

## 它解决什么问题

普通待办应用只有人能操作；接入 LLM 的待办又常常丢掉界面。todo-mcp 用「**单 exe 双模式**」把两边缝在一起：

| 启动方式 | 模式 | 说明 |
|---|---|---|
| `todo-mcp.exe` | GUI | 窗口 + 托盘常驻的待办客户端 |
| `todo-mcp.exe --mcp` | MCP | stdio MCP 服务器，供任意 AI 客户端调用 |

两种模式读写同一个数据库（`%APPDATA%\todo-mcp\todo.db`，SQLite WAL），谁写入对方都看得见：

```
        ┌────────────┐  stdio (JSON-RPC)   ┌──────────────┐
        │ AI 客户端   │◄──────────────────►│ todo-mcp.exe │
        │ (宿主进程)  │                     │   --mcp      │
        └────────────┘                     └──────┬───────┘
                                            SQLite (WAL)
        ┌────────────┐   每 2 秒探测外部写入    │
        │ todo-mcp   │◄─────────────────────────┘
        │   GUI      │      %APPDATA%\todo-mcp\todo.db
        └────────────┘
```

## 功能总览

### GUI（Viva 框架，GPU 原生绘制，无 WebView）

- **任务清单**：表格视图，列宽拖拽、行多选批量删除、行内编辑、逾期整行高亮、粘性分组头
- **自定义字段**：文本 / 单选 / 多选 / 数字 / 百分数 / 货币 / 进度 / 日期 8 类，弹窗内下拉选择类型；列的显示、排序（拖拽把手上移下移）、重命名、删除（二次确认）都在列设置面板完成
- **筛选 / 排序 / 分组**：多条件 AND 筛选（≤10 条）、多键排序、按任意字段分组，偏好落库重启不丢
- **归档与知识文档**：已完成任务归档后一键生成 Markdown 知识文档
- **附件**：图片 + 文件附件，行内「图 N」「文 N」徽标打开详情抽屉
- **仪表盘**：按状态 / 优先级 / 自定义字段等维度统计，柱状图呈现
- **体验细节**：主题跟随系统明暗、批量与删除操作二次确认、单实例锁 + 托盘常驻

### MCP（16 个工具，服务器名 `todo`）

| 分组 | 工具 |
|---|---|
| 任务（7） | `create_task` `list_tasks` `update_task` `complete_task` `archive_task` `unarchive_task` `remove_task` |
| 自定义字段（4） | `create_field` `list_fields` `update_field` `remove_field` |
| 附件（5） | `attach_image` `remove_image` `attach_file` `attach_file_ref` `remove_file` |

支持筛选（任意字段条件 AND）、排序、分组、分页；自定义字段值按类型编码（文本=字符串、数字=数值、日期=RFC3339、单选=字符串、多选=字符串数组）。

## 快速开始

### 构建

```sh
git clone https://github.com/linCoder96/todo-mcp.git && cd todo-mcp
viva build                # 产物：build/todo-mcp.exe（windows/amd64，约 16 MB）
```

没有 `viva` 命令时，`go build -o build/todo-mcp.exe .` 也可产出功能等价的 exe。

### 用 GUI

双击 `todo-mcp.exe` 即可，首次启动自动建库建表。数据都在 `%APPDATA%\todo-mcp\`（数据库、附件、归档文档）。

### 接入 AI 客户端

以 Claude Desktop 为例，编辑 `%APPDATA%\Claude\claude_desktop_config.json`（Windows 路径在 JSON 里要写成 `\\`）：

```json
{
  "mcpServers": {
    "todo": {
      "command": "D:\\apps\\todo-mcp\\todo-mcp.exe",
      "args": ["--mcp"]
    }
  }
}
```

重启客户端后即可让 AI 调用 `todo` 的 16 个工具。**ZCode / Cursor / Windsurf / MCP Inspector 的完整配置、部署后验证（含零依赖命令行冒烟测试）见 [docs/mcp.md](docs/mcp.md)**。

> 小技巧：给 MCP 配 `"env": {"APPDATA": "D:\\tmp\\todo-test"}` 可让它操作隔离的测试库，与 GUI 的真实库互不干扰。

## 开发

| 命令 | 作用 |
|---|---|
| `viva dev .` | 热重载运行：保存文件即重载窗口并保留状态 |
| `go test ./... -count=1` | 全部测试（GUI 用 driver 驱动真实页面，无窗口） |
| `go vet ./... && viva vet` | 静态检查（`viva vet` 能发现页面声明类错误） |
| `viva build` | 构建 `build/todo-mcp.exe` |

测试约定：纯单元测试不启动 Spring/GUI 上下文、外部依赖一律 Mock、断言成对（HTTP 状态 + 业务 code）……详见各包内测试文件与 `_test.go` 注释。

### 项目结构

```
├── main.go          # 进程入口：按 --mcp 参数分发到 MCP 或 GUI 模式
├── app/
│   ├── gui/         # Viva 页面（任务清单、仪表盘、弹窗、附件、同步…）
│   ├── mcp/         # MCP 服务器（stdio JSON-RPC，16 个工具的 schema 与分发）
│   └── store/       # SQLite 仓储层（GUI 与 MCP 共用同一套语义）
├── docs/mcp.md      # MCP 模式安装与部署教程（配置、验证、排障、卸载）
├── assets/          # 应用图标
└── legacy/          # 归档的旧 Tauri 工程（源码保留供迁移核对，生成物已忽略）
```

## 说明

- 仅提供 windows/amd64 构建（Windows 10 及以上）。
- MCP 通信协议为 stdio 换行分隔 JSON-RPC；参数校验失败返回 `-32602`，业务失败返回 `isError=true` 的中文原因。
- 仓库暂未附加开源许可证，默认保留所有权利。
