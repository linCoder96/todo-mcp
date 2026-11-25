//! MCP 模式入口（design.md §1.1/§2.1）：
//! 不初始化 Tauri / WebView2，仅构建 tokio current_thread 单线程 runtime，
//! 以 rmcp stdio transport 拉起 [`tools::TodoTools`] 并阻塞等待退出。
//! 进程生命周期由宿主 agent 管理：agent 退出 → stdin EOF → 本进程自然退出。
//!
//! ## stdout 纪律（design.md §2.1，code review 检查项）
//!
//! MCP 模式下 stdout 是 JSON-RPC 协议通道，**严禁任何 `println!`/日志打到
//! stdout**；本模块一切日志只允许走 stderr。

pub mod dto;
pub mod tools;

use rmcp::ServiceExt;
use tokio::runtime::Builder;

/// MCP 模式主函数：阻塞直至服务退出，返回进程退出码。
pub fn run() -> i32 {
    // 单线程 runtime：stdio server 无并行计算，current_thread 更省内存（design.md §2.1）
    let runtime = match Builder::new_current_thread().enable_all().build() {
        Ok(runtime) => runtime,
        Err(error) => {
            eprintln!("[todo-mcp] 构建 tokio runtime 失败: {error}");
            return 1;
        }
    };

    let outcome = runtime.block_on(async {
        // serve() 会挂起等待对端 initialize：手动在终端裸跑没有任何输出是
        // 正常现象，调试一律走 MCP Inspector 或真实 agent（design.md §2.1）
        let service = tools::TodoTools
            .serve(rmcp::transport::stdio())
            .await
            .map_err(|error| error.to_string())?;
        service.waiting().await.map_err(|error| error.to_string())
    });

    match outcome {
        Ok(_) => 0,
        Err(error) => {
            eprintln!("[todo-mcp] MCP 服务异常退出: {error}");
            1
        }
    }
}
