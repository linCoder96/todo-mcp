<script setup lang="ts">
import { reactive } from "vue";
import { PRIORITY_LABELS, type Task } from "../types";

// 行内编辑器（design.md §5 第 5 项）：
// 标题文本框 + 优先级下拉（无/低/中/高）+ 到期 datetime-local 原生控件；保存/取消。
// 不做弹窗编辑器；description 不在本期编辑范围（MVP 编辑仅标题/优先级/到期）。
const props = defineProps<{ task: Task }>();
const emit = defineEmits<{
  save: [patch: { title: string; priority: number; dueAt: string | null }];
  cancel: [];
}>();

function toLocalInput(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

const form = reactive({
  title: props.task.title,
  priority: props.task.priority,
  // datetime-local 本地时间串；保存时转 UTC RFC3339，清空表示"无到期"
  dueLocal: props.task.dueAt === null ? "" : toLocalInput(new Date(props.task.dueAt)),
});

function save(): void {
  const title = form.title.trim();
  if (!title) return; // 与后端校验一致：空白标题不提交
  emit("save", {
    title,
    priority: form.priority,
    dueAt: form.dueLocal === "" ? null : new Date(form.dueLocal).toISOString(),
  });
}
</script>

<template>
  <form class="task-editor" @submit.prevent="save">
    <input v-model="form.title" class="editor-title" type="text" />
    <select v-model.number="form.priority" class="editor-priority">
      <option v-for="(label, value) in PRIORITY_LABELS" :key="value" :value="Number(value)">
        优先级：{{ label }}
      </option>
    </select>
    <input v-model="form.dueLocal" class="editor-due" type="datetime-local" />
    <button type="submit" class="editor-save">保存</button>
    <button type="button" class="editor-cancel" @click="emit('cancel')">取消</button>
  </form>
</template>
