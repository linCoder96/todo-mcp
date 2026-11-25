/**
 * Task DTO（design.md §4.6 任务对象出参结构）。
 * 与后端 serde camelCase 出参字段一一对应；时间为 UTC RFC3339 字符串。
 */
export interface Task {
  /** UUIDv7 */
  id: string;
  /** 标题 */
  title: string;
  /** 详情；null = 无描述 */
  description: string | null;
  /** 优先级：0=无 1=低 2=中 3=高 */
  priority: number;
  /** 到期时间 UTC RFC3339；null = 无到期 */
  dueAt: string | null;
  /** 状态：0=进行中 1=已完成 */
  status: number;
  /** 完成时间 UTC RFC3339；未完成为 null */
  completedAt: string | null;
  /** 创建时间 UTC RFC3339 */
  createdAt: string;
  /** 最后更新时间 UTC RFC3339 */
  updatedAt: string;
}

/** list_tasks 出参：total 为当前筛选条件下总数（design.md §4.2） */
export interface TaskPage {
  total: number;
  tasks: Task[];
}

/** 筛选 tab：全部 / 进行中 / 已完成（design.md §5 第 6 项） */
export type TaskFilter = "all" | "active" | "done";

/** 优先级文案（0=无 1=低 2=中 3=高） */
export const PRIORITY_LABELS: Record<number, string> = {
  0: "无",
  1: "低",
  2: "中",
  3: "高",
};

/** GUI 单次分页大小；与后端 list 钳制上限一致 */
export const PAGE_SIZE = 200;
