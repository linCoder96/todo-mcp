//! todo-mcp 库入口：按 Tauri 2 官方模板以 lib 形式组织代码，`main.rs` 仅做
//! 参数分发（design.md §1.1 单 exe 双模式）。
//!
//! 模块职责（design.md §6）：
//! - [`gui`]：GUI 模式装配（窗口 + 托盘 + 防多开）；
//! - [`mcp`]：MCP 模式入口与工具定义；
//! - [`db`]：SQLite 连接管理 / 迁移 / 任务 CRUD（GUI 与 MCP 共同底座）。

pub mod commands;
pub mod db;
pub mod gui;
pub mod mcp;
