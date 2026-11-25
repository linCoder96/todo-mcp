<script setup lang="ts">
import { computed, ref } from "vue";
import { PRIORITY_LABELS, type Task } from "../types";
import TaskEditor from "./TaskEditor.vue";

// 单行任务（design.md §5 第 1/3/4/5 项）：
// 勾选完成、删除原位二次确认、点击行展开行内编辑；
// 行内显示完成勾选框、标题、优先级文字、到期时间（逾期标红，纯 CSS 类切换）
const props = defineProps<{ task: Task }>();
const emit = defineEmits<{
  toggle: [task: Task];
  remove: [id: string];
  save: [id: string, patch: { title: string; priority: number; dueAt: string | null }];
}>();

const editing = ref(false);
const confirmingDelete = ref(false);
let confirmTimer: ReturnType<typeof setTimeout> | null = null;

const isOverdue = computed(
  () =>
    props.task.status === 0 &&
    props.task.dueAt !== null &&
    new Date(props.task.dueAt).getTime() < Date.now(),
);

const dueLabel = computed(() => {
  if (props.task.dueAt === null) return "";
  const date = new Date(props.task.dueAt);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
});

function onDeleteClick(): void {
  if (!confirmingDelete.value) {
    confirmingDelete.value = true;
    if (confirmTimer !== null) clearTimeout(confirmTimer);
    // 短暂未确认则自动复位，避免误留"确认删除？"状态
    confirmTimer = setTimeout(() => {
      confirmingDelete.value = false;
    }, 3000);
    return;
  }
  if (confirmTimer !== null) clearTimeout(confirmTimer);
  confirmingDelete.value = false;
  emit("remove", props.task.id);
}

function onSave(patch: { title: string; priority: number; dueAt: string | null }): void {
  editing.value = false;
  emit("save", props.task.id, patch);
}
</script>

<template>
  <li class="task-item" :class="{ done: task.status === 1 }">
    <div class="task-row" @click="editing = true">
      <input
        type="checkbox"
        class="task-check"
        :checked="task.status === 1"
        @click.stop
        @change="emit('toggle', task)"
      />
      <span class="task-title" :title="task.title">{{ task.title }}</span>
      <span class="task-priority" :class="`priority-${task.priority}`">
        {{ PRIORITY_LABELS[task.priority] ?? task.priority }}
      </span>
      <span v-if="task.dueAt !== null" class="task-due" :class="{ overdue: isOverdue }">
        {{ dueLabel }}
      </span>
      <button
        type="button"
        class="task-delete"
        :class="{ confirming: confirmingDelete }"
        @click.stop="onDeleteClick"
      >
        {{ confirmingDelete ? "确认删除？" : "删除" }}
      </button>
    </div>
    <TaskEditor
      v-if="editing"
      :task="task"
      @save="onSave"
      @cancel="editing = false"
    />
  </li>
</template>
