//! MCP 工具定义（design.md §2.1/§6 模块职责边界）。
//!
//! M1 现状：仅含占位工具 `ping`，用于验证 rmcp stdio 链路；五个业务工具
//! （create/list/update/complete/remove_task）随 M4 接入——届时本文件 =
//! 协议适配 + 参数校验 + 调 `db::task_repo` + 组装出参，不得出现 SQL。

use rmcp::model::{CallToolResult, ContentBlock, ErrorData};
use rmcp::{tool, tool_router};

/// MCP 工具集。
///
/// 采用合并形式 `#[tool_router(server_handler)]` 免写独立 `ServerHandler`
/// impl（design.md §2.1 两种装配方式中的 MVP 选择）：宏自动生成工具路由
/// 并实现 ServerHandler（server name/version 取 crate 元数据）。
#[derive(Clone, Default)]
pub struct TodoTools;

#[tool_router(server_handler)]
impl TodoTools {
    /// 连通性探测：返回 pong，验证 stdio 通路（M1 验收：initialize +
    /// tools/list 经 MCP Inspector 完成）。
    #[tool(description = "连通性探测：返回 pong")]
    fn ping(&self) -> Result<CallToolResult, ErrorData> {
        Ok(CallToolResult::success(vec![ContentBlock::text("pong")]))
    }
}
