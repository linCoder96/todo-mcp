# todo-mcp 的 MCP 模式:安装与部署教程

todo-mcp 是一个**单 exe 双模式**的 Windows 桌面应用:

| 启动方式 | 模式 | 说明 |
|---|---|---|
| `todo-mcp.exe` | GUI | 窗口 + 托盘常驻的待办客户端 |
| `todo-mcp.exe --mcp` | MCP | stdio MCP 服务器,供 AI 客户端(Agent)调用 |

两种模式**读写同一个数据库**(`%APPDATA%\todo-mcp\todo.db`,SQLite WAL):GUI 里看到的任务,AI 客户端也能查到;AI 建的任务,约 2 秒内自动出现在 GUI 窗口里。这就是本应用的核心用法——让 AI 与你操作同一份待办数据。

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

- MCP 服务器名:`todo`,版本 `v0.1.0`,传输为 stdio(换行分隔的 JSON-RPC)。
- stdout 是协议通道,**日志一律走 stderr**(`[todo-mcp]` 前缀),排错时在客户端的日志里找。
- MCP 进程的生命周期由宿主客户端管理:启动、重启、stdin 关闭(EOF)即退出。
- 单实例锁只作用于 GUI 模式;MCP 是独立进程,**可与 GUI 同时运行**(这正是设计场景)。

---

## 1. 安装

### 1.1 获取 exe

**方式 A:从源码构建**(当前唯一分发方式)

```sh
git clone <本仓库> && cd spaceXXXL
viva build          # 产物:build/todo-mcp.exe(Windows amd64,约 16 MB)
```

没有 `viva` 命令时,普通 `go build -o build/todo-mcp.exe .` 也可产出功能等价的 exe。

**方式 B:直接使用仓库里的 `build/todo-mcp.exe`**(刚构建好的)。

### 1.2 放到稳定目录(推荐)

MCP 客户端的配置要写 exe 的**绝对路径**,所以建议把它放到一个不随仓库变动的位置,例如:

```text
D:\apps\todo-mcp\todo-mcp.exe
```

这样以后重新构建时,把新 exe 覆盖过去即可,**客户端配置永远不用改**(升级 = 替换 exe,GUI 与 MCP 两模式同版本)。

### 1.3 前置条件

- Windows 10 及以上(本应用仅提供 windows/amd64 构建)。
- MCP 客户端一个:Claude Desktop、ZCode、Cursor、Windsurf 等任意支持 stdio MCP 的客户端。
- Node.js(可选,仅在用 MCP Inspector 预检时需要)。

---

## 2. 部署到客户端

以下配置里的 exe 路径按你的实际安装位置替换。JSON 里 Windows 路径的反斜杠必须写成 `\\`(或用正斜杠 `/`)。

### 2.1 Claude Desktop

编辑 `%APPDATA%\Claude\claude_desktop_config.json`(不存在则新建):

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

保存后**完全退出并重启** Claude Desktop(托盘右键退出,不是关窗口),左下角工具图标里应出现 `todo` 及其工具列表。

### 2.2 ZCode

ZCode 的 MCP 配置有两个作用域,都用 `mcp.servers` 字段:

- **用户级**(所有工作区可用):`~/.zcode/cli/config.json`,Windows 上即 `C:\Users\<你>\.zcode\cli\config.json`
- **工作区级**(只在本项目可用,可随仓库共享):`<repo>/.zcode/config.json`

```json
{
  "mcp": {
    "servers": {
      "todo": {
        "type": "stdio",
        "command": "D:/apps/todo-mcp/todo-mcp.exe",
        "args": ["--mcp"]
      }
    }
  }
}
```

ZCode 配置的几个硬规则:

1. **`command` 必须是字符串,其余参数进 `args` 数组**。不要写 OpenCode 风格的 `"command": ["...", "--mcp"]`,那会让设置页崩溃或服务器被静默丢弃。
2. **字段名严格**:`env`(环境变量)、`enabled`、`timeoutMs`;出现未知字段整条服务器配置会被丢弃。
3. **不展开 `${...}` 模板变量**,必须写绝对路径。
4. 改完后重启会话,在 **设置 → MCP** 页确认 `todo` 显示为已连接;失败时错误信息直接显示在该条目上。

### 2.3 其它 stdio 客户端(Cursor / Windsurf / 通用)

绝大多数客户端用的 JSON 形状与 Claude Desktop 相同(`mcpServers` 键 + `command`/`args`):

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

具体配置文件位置参考对应客户端的文档;只要支持「stdio + command」就一定能接。

---

## 3. 部署后验证

### 3.1 看工具列表

任一客户端里让 AI 调用 `list_tasks`,或直接看客户端的 MCP 工具面板,应看到 **16 个工具**(见第 4 节清单)。

### 3.2 MCP Inspector 预检(不依赖任何客户端)

```sh
npx @modelcontextprotocol/inspector
```

浏览器打开 Inspector 后:Transport 选 **STDIO**,Command 填 exe 完整路径,Arguments 填 `--mcp`,点 Connect。`tools/list` 应返回 16 个工具;随便 `tools/call` 一条 `create_task` 即可验证写入(注意:默认连的是真实库,测完删掉测试任务)。

### 3.3 命令行冒烟测试(零依赖)

对 exe 直接说 JSON-RPC(Git Bash / WSL):

```sh
{ printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
  sleep 1
  printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  sleep 1
} | "D:/apps/todo-mcp/todo-mcp.exe" --mcp
```

> 中间的 `sleep` 是必要的:stdin 立刻 EOF 会让服务器在处理完输入前就退出。真实客户端保持连接打开,不受此影响。

预期输出(节选,已实测):

```json
{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"logging":{},"tools":{"listChanged":true}},"protocolVersion":"2025-03-26","serverInfo":{"name":"todo","version":"v0.1.0"}}}
{"jsonrpc":"2.0","id":2,"result":{...,"tools":[{"name":"archive_task",...}, {"name":"create_task",...}, ...]}}
```

---

## 4. 工具清单(16 个)

### 任务(7 个)

| 工具 | 用途 |
|---|---|
| `create_task` | 创建任务:`title` 必填;`description`、`priority`(0 无 / 1 低 / 2 中 / 3 高)、`due_at`(RFC3339)、自定义字段 `values` 可选 |
| `list_tasks` | 查询:状态/归档筛选、`filter`(任意字段条件,≤10 条 AND)、`sort`(≤3 键)、`group_by`(分组);分页 `limit`(≤200)/`offset` |
| `update_task` | 部分更新:只改出现的字段;`description`/`due_at` 传 `null` 表示清空 |
| `complete_task` | 标记完成(记 `completed_at`) |
| `archive_task` | 归档一条**已完成**的任务(进已归档视图;幂等) |
| `unarchive_task` | 取消归档 |
| `remove_task` | 物理删除,不可恢复 |

### 自定义字段(4 个)

| 工具 | 用途 |
|---|---|
| `create_field` | 创建自定义字段(文本/数字/日期/单选/多选;类型创建后不可变) |
| `list_fields` | 列出全部字段(内置 + 自定义;内置字段 `builtin=true` 不可删改) |
| `update_field` | 改名/改选项/改位置 |
| `remove_field` | 删除自定义字段(级联删除其全部值) |

自定义字段值在任务的 `values` 里传:**键 = 字段名**,值按类型(文本=string、数字=number、日期=RFC3339、单选=string、多选=string 数组);传 `null` 清空该字段值。

### 附件(5 个)

| 工具 | 用途 |
|---|---|
| `attach_image` | 添加图片附件(文件拷贝进应用数据目录,与源文件解耦) |
| `remove_image` | 删除图片附件(元数据与文件一并删除) |
| `attach_file` | 添加文件附件(拷贝进应用数据目录) |
| `attach_file_ref` | 只记录源文件路径,不拷贝;**源文件须存在** |
| `remove_file` | 删除文件附件(copy 连文件删;ref 不碰源文件) |

图片与文件附件在 GUI 里通过任务行上的「图 N」「文 N」徽标打开详情抽屉查看。

---

## 5. 运行机制与数据

- **数据位置**:`%APPDATA%\todo-mcp\todo.db`(SQLite,附件在同级 `images/`、`files/`、`docs/` 目录)。首次启动自动建库建表。
- **并发安全**:数据库以 WAL 模式打开(busy_timeout=5000ms),GUI 与 MCP 两进程同时读写安全;GUI 每 2 秒探测外部写入并自动刷新,因此 **AI 建的任务约 2 秒内出现在 GUI**;GUI 的改动对 AI 的下一次调用即时可见。
- **想给 MCP 一个隔离的测试库**:在服务器配置里加环境变量即可,例如 ZCode 里 `"env": {"APPDATA": "D:\\tmp\\todo-test"}`,该 MCP 实例就操作 `D:\tmp\todo-test\todo-mcp\todo.db`,与 GUI 的库互不干扰。同一客户端配多个不同名的服务器条目即可同时连真实库与测试库。
- **升级**:重新 `viva build` 后用新 exe 覆盖安装目录里的旧文件;客户端配置不用动。GUI 与 MCP 版本永远一致(同一个 exe)。
- **出错表现**:参数校验失败返回 JSON-RPC `invalid_params`(-32602);业务失败(如 id 不存在)返回 `isError=true` 的工具结果,`message` 为中文原因。

---

## 6. 故障排查

| 症状 | 原因与处理 |
|---|---|
| 客户端里 `todo` 显示 failed: `spawn ... ENOENT` | `command` 路径不对。用绝对路径;JSON 里反斜杠要转义成 `\\`,或改用 `/` |
| 配置了但服务器列表里根本没有 `todo` | JSON 语法错误,或字段名不合法(ZCode 严格 schema,未知字段整条丢弃)。校验 JSON;`command` 必须是字符串 |
| 连接超时 | 正常启动远小于 30 秒;若发生,检查 exe 是否被杀毒软件拦截,或在配置里加 `timeoutMs` 放宽 |
| MCP 建了任务,GUI 里看不到 | GUI 在后台每 2 秒同步,等 2 秒或点一下窗口触发聚焦刷新;确认两个进程连的是同一个 `%APPDATA%`(没有配 `env.APPDATA` 重定向) |
| 想看服务端日志 | 日志在 stderr,到客户端的 MCP 日志里搜 `[todo-mcp]`;或直接在终端跑第 3.3 节的冒烟命令 |
| `打开数据库失败` | `%APPDATA%\todo-mcp\todo.db` 被占用或损坏;确认没有旧的 MCP 进程残留;必要时关掉所有 todo-mcp 进程后重启客户端 |
| GUI 双击打不开,但 MCP 正常 | GUI 模式有单实例锁(已有实例常驻托盘);看托盘,或结束既有 `todo-mcp.exe` 进程后再开 |

---

## 7. 卸载

1. 在 MCP 客户端配置里删除 `todo` 服务器条目。
2. 删除安装目录(如 `D:\apps\todo-mcp\`)。
3. 想连数据一起清掉,再删 `%APPDATA%\todo-mcp\`(数据库、附件、归档知识文档都在里面;删前先备份)。
