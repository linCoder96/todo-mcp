//! 进程入口：解析 argv 分发双模式（design.md §1.1）。
//!
//! - `--mcp`：MCP 模式，不初始化 Tauri / WebView2，仅 rmcp stdio server；
//! - 无参数：GUI 模式，Tauri 主窗口 + 系统托盘常驻。
//!
//! 注意：GUI 模式进程常驻（托盘），不从此处 return；MCP 模式由 mcp::run()
//! 返回退出码，进程随之结束（agent 退出 → stdin EOF → 自然退出）。

fn main() {
    let mcp_mode = std::env::args().skip(1).any(|arg| arg == "--mcp");
    if mcp_mode {
        std::process::exit(todo_mcp_lib::mcp::run());
    }
    todo_mcp_lib::gui::run();
}
