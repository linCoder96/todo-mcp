import { invoke } from "@tauri-apps/api/core";
import { getCurrentWebviewWindow } from "@tauri-apps/api/webviewWindow";
import { onMounted, onUnmounted, ref } from "vue";
import { PAGE_SIZE, type Task, type TaskFilter, type TaskPage } from "../types";

/**
 * 唯一状态源（design.md §6）：invoke 封装 + 列表/筛选状态 + 聚焦重查。
 * 无全局状态库（Pinia 不引入）；数据刷新策略见 design.md §4：
 * 窗口聚焦事件触发一次全量重查，本进程操作后同样重查。
 */
export function useTasks() {
  const tasks = ref<Task[]>([]);
  const filter = ref<TaskFilter>("all");
  const loading = ref(false);
  const error = ref<string | null>(null);

  function statusParam(): number | null {
    if (filter.value === "active") return 0;
    if (filter.value === "done") return 1;
    return null;
  }

  /** 全量重查：后端单页上限 200，超出时按 offset 翻页取全（design.md §4.2 钳制） */
  async function refresh(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      const status = statusParam();
      let page = await invoke<TaskPage>("list_tasks", { status, offset: 0 });
      const collected = [...page.tasks];
      while (collected.length < page.total && page.tasks.length > 0) {
        page = await invoke<TaskPage>("list_tasks", {
          status,
          offset: collected.length,
        });
        collected.push(...page.tasks);
      }
      tasks.value = collected;
    } catch (e) {
      error.value = String(e);
    } finally {
      loading.value = false;
    }
  }

  /** 快捷新建 / 编辑器新建：默认 priority=0、无到期（design.md §5 第 2 项） */
  async function createTask(title: string): Promise<void> {
    error.value = null;
    try {
      await invoke("create_task", { input: { title } });
      await refresh();
    } catch (e) {
      error.value = String(e);
    }
  }

  /** 勾选完成 / 取消勾选（可撤销完成，design.md §5 第 3 项） */
  async function toggleTask(task: Task): Promise<void> {
    error.value = null;
    try {
      if (task.status === 0) {
        await invoke("complete_task", { id: task.id });
      } else {
        await invoke("reopen_task", { id: task.id });
      }
      await refresh();
    } catch (e) {
      error.value = String(e);
    }
  }

  /** 删除（物理删除，二次确认在组件层，design.md §5 第 4 项） */
  async function removeTask(id: string): Promise<void> {
    error.value = null;
    try {
      await invoke("remove_task", { id });
      await refresh();
    } catch (e) {
      error.value = String(e);
    }
  }

  /** 行内编辑保存：标题/优先级/到期；dueAt=null 清除到期（design.md §5 第 5 项） */
  async function saveTask(
    id: string,
    patch: { title: string; priority: number; dueAt: string | null },
  ): Promise<void> {
    error.value = null;
    try {
      await invoke("update_task", { input: { id, ...patch } });
      await refresh();
    } catch (e) {
      error.value = String(e);
    }
  }

  /** 切换筛选 tab 即查询（design.md §5 第 6 项） */
  async function setFilter(value: TaskFilter): Promise<void> {
    if (filter.value === value) return;
    filter.value = value;
    await refresh();
  }

  let unlistenFocus: (() => void) | null = null;

  onMounted(async () => {
    await refresh();
    // 窗口聚焦触发一次全量重查：WAL 下 MCP 写提交后立即可见（design.md §4 并发可见性）
    unlistenFocus = await getCurrentWebviewWindow().listen("tauri://focus", () => {
      void refresh();
    });
  });
  onUnmounted(() => {
    unlistenFocus?.();
  });

  return {
    tasks,
    filter,
    loading,
    error,
    refresh,
    createTask,
    toggleTask,
    removeTask,
    saveTask,
    setFilter,
  };
}
