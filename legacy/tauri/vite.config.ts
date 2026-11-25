import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

// Tauri 前端构建配置（design.md §6：vue 插件 + @tauri-apps/cli）
export default defineConfig({
  plugins: [vue()],
  // 防止 Tauri 干扰清屏与错误提示
  clearScreen: false,
  server: {
    port: 5173,
    strictPort: true,
    watch: {
      // Rust 侧变更由 cargo 监听，前端不再重复监视
      ignored: ["**/src-tauri/**"],
    },
  },
  build: {
    target: "chrome105",
  },
});
