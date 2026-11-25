<script setup lang="ts">
import type { TaskFilter } from "../types";

// 筛选 tab：全部 / 进行中 / 已完成（design.md §5 第 6 项），切换即查询
defineProps<{ modelValue: TaskFilter }>();
const emit = defineEmits<{ "update:modelValue": [value: TaskFilter] }>();

const tabs: Array<{ value: TaskFilter; label: string }> = [
  { value: "all", label: "全部" },
  { value: "active", label: "进行中" },
  { value: "done", label: "已完成" },
];
</script>

<template>
  <nav class="filter-tabs">
    <button
      v-for="tab in tabs"
      :key="tab.value"
      type="button"
      class="filter-tab"
      :class="{ active: modelValue === tab.value }"
      @click="emit('update:modelValue', tab.value)"
    >
      {{ tab.label }}
    </button>
  </nav>
</template>
