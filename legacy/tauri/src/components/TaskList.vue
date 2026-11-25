<script setup lang="ts">
import type { Task } from "../types";
import TaskItem from "./TaskItem.vue";

// 任务列表（design.md §5 第 1 项）：排序由后端固定排序保证，前端只按序渲染
defineProps<{ tasks: Task[] }>();
const emit = defineEmits<{
  toggle: [task: Task];
  remove: [id: string];
  save: [id: string, patch: { title: string; priority: number; dueAt: string | null }];
}>();
</script>

<template>
  <ul class="task-list">
    <TaskItem
      v-for="task in tasks"
      :key="task.id"
      :task="task"
      @toggle="emit('toggle', $event)"
      @remove="emit('remove', $event)"
      @save="(id, patch) => emit('save', id, patch)"
    />
  </ul>
</template>
