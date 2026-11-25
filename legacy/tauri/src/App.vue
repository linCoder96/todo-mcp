<script setup lang="ts">
import FilterTabs from "./components/FilterTabs.vue";
import QuickAdd from "./components/QuickAdd.vue";
import TaskList from "./components/TaskList.vue";
import { useTasks } from "./composables/useTasks";

// 布局骨架（design.md §6 App.vue）：筛选 tab + 快捷新建 + 列表。
// 全部状态由 useTasks 单一 composable 驱动（design.md §5：无全局状态库）。
const { tasks, filter, loading, error, createTask, toggleTask, removeTask, saveTask, setFilter } =
  useTasks();
</script>

<template>
  <main class="app">
    <header class="app-header">
      <h1 class="app-title">待办</h1>
    </header>
    <QuickAdd @create="createTask" />
    <FilterTabs :model-value="filter" @update:model-value="setFilter" />
    <p v-if="error" class="app-error" role="alert">{{ error }}</p>
    <p v-if="loading && tasks.length === 0" class="app-hint">加载中…</p>
    <TaskList
      :tasks="tasks"
      @toggle="toggleTask"
      @remove="removeTask"
      @save="saveTask"
    />
    <p v-if="!loading && tasks.length === 0" class="app-hint">暂无任务</p>
  </main>
</template>
