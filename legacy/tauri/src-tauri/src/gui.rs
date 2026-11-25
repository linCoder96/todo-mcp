//! GUI 模式装配（design.md §6）：Tauri Builder、主窗口、托盘菜单与事件、
//! 关闭窗口=隐藏到托盘、tauri-plugin-single-instance 防多开。
//!
//! Tauri IPC commands（commands/ 模块）在 setup 中注册，并管理进程内唯一的长连接。

use tauri::{
    menu::{Menu, MenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    Manager, WindowEvent,
};
use std::sync::Mutex;

use crate::{commands, db};

/// 显示并聚焦主窗口（托盘"显示"、防多开二次启动共用）。
fn show_main_window(app: &tauri::AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.unminimize();
        let _ = window.show();
        let _ = window.set_focus();
    }
}

/// 托盘左键单击：切换主窗口显示/隐藏（design.md §5 托盘交互）。
fn toggle_main_window(app: &tauri::AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        if window.is_visible().unwrap_or(false) {
            let _ = window.hide();
        } else {
            show_main_window(app);
        }
    }
}

/// GUI 模式主函数：阻塞直至进程退出（仅托盘菜单"退出"真退出）。
pub fn run() {
    tauri::Builder::default()
        // single-instance 必须最先注册：二次启动时聚焦已有窗口并退出新进程
        .plugin(tauri_plugin_single_instance::init(|app, _argv, _cwd| {
            show_main_window(app);
        }))
        .setup(|app| {
            // GUI 进程内唯一长连接：WAL / busy_timeout 由 db::open 统一配置
            let conn = db::open()?;
            app.manage(commands::DbState(Mutex::new(conn)));

            // 托盘右键菜单：显示 / 隐藏 / 退出（design.md §5）
            let show = MenuItem::with_id(app, "show", "显示主窗口", true, None::<&str>)?;
            let hide = MenuItem::with_id(app, "hide", "隐藏主窗口", true, None::<&str>)?;
            let quit = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show, &hide, &quit])?;

            let mut tray = TrayIconBuilder::with_id("main-tray")
                .tooltip("待办")
                .menu(&menu)
                // 左键不弹菜单：左键单击 = 切换显示/隐藏；右键弹菜单
                .show_menu_on_left_click(false)
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        toggle_main_window(tray.app_handle());
                    }
                });
            if let Some(icon) = app.default_window_icon() {
                tray = tray.icon(icon.clone());
            }
            tray.build(app)?;
            Ok(())
        })
        .on_menu_event(|app, event| {
            match event.id().as_ref() {
                "show" => show_main_window(app),
                "hide" => {
                    if let Some(window) = app.get_webview_window("main") {
                        let _ = window.hide();
                    }
                }
                // 仅托盘菜单"退出"真正退出进程（design.md §5：关闭窗口=隐藏）
                "quit" => app.exit(0),
                _ => {}
            }
        })
        .on_window_event(|window, event| {
            // 关闭按钮 = 隐藏到托盘：阻止默认销毁，保持常驻语义（design.md §5）
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .invoke_handler(tauri::generate_handler![
            commands::list_tasks,
            commands::create_task,
            commands::update_task,
            commands::complete_task,
            commands::reopen_task,
            commands::remove_task,
        ])
        .run(tauri::generate_context!())
        .expect("todo-mcp GUI 模式启动失败");
}
