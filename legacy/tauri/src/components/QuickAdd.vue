<script setup lang="ts">
import { ref } from "vue";

// 快捷新建输入框（design.md §5 第 2 项）：回车即创建，Esc 清空
const emit = defineEmits<{ create: [title: string] }>();
const title = ref("");

function submit(): void {
  const value = title.value.trim();
  if (!value) return;
  emit("create", value);
  title.value = "";
}

function clear(): void {
  title.value = "";
}
</script>

<template>
  <section class="quick-add-row">
    <input
      v-model="title"
      class="quick-add"
      type="text"
      placeholder="快捷新建任务：输入标题后回车（Esc 清空）"
      @keydown.enter="submit"
      @keydown.esc="clear"
    />
  </section>
</template>
