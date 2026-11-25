//! MCP 工具入参/出参 DTO（design.md §2.1/§4/§6）。
//!
//! - 入参 derive `Deserialize` + `schemars::JsonSchema`：字段类型与文档注释
//!   自动生成 inputSchema（design.md §2.1）；
//! - 出参统一 serde camelCase，与前端 `src/types.ts` 一一对应；
//! - 时间字段：存储层为 UTC epoch 毫秒，对外一律 RFC3339 UTC 字符串
//!   （design.md §4 统一约定）；入参 `due_at` 接受任意合法 RFC3339（含
//!   时区偏移），入库前归一化为 UTC epoch 毫秒。
//!
//! 说明：design.md §2 选型表写"schemars 随 rmcp server feature 引入"，但
//! rmcp 3.11 未公开 re-export schemars，故按同版本线（1.x）声明直接依赖，
//! Cargo 会与 rmcp 的 schemars 统一为同一版本。

use serde::{Deserialize, Serialize};

use crate::db::task_repo::Task;

/// 任务对象出参（design.md §4.6，各工具与 GUI commands 共用）。
#[derive(Debug, Clone, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct TaskDto {
    /// UUIDv7
    pub id: String,
    /// 标题
    pub title: String,
    /// 详情；null = 无描述
    pub description: Option<String>,
    /// 优先级：0=无 1=低 2=中 3=高
    pub priority: i64,
    /// 到期时间 UTC RFC3339；null = 无到期
    pub due_at: Option<String>,
    /// 状态：0=进行中 1=已完成
    pub status: i64,
    /// 完成时间 UTC RFC3339；未完成为 null
    pub completed_at: Option<String>,
    /// 创建时间 UTC RFC3339
    pub created_at: String,
    /// 最后更新时间 UTC RFC3339
    pub updated_at: String,
}

impl From<Task> for TaskDto {
    fn from(task: Task) -> Self {
        TaskDto {
            id: task.id,
            title: task.title,
            description: task.description,
            priority: task.priority,
            due_at: task.due_at.map(epoch_ms_to_rfc3339),
            status: task.status,
            completed_at: task.completed_at.map(epoch_ms_to_rfc3339),
            created_at: epoch_ms_to_rfc3339(task.created_at),
            updated_at: epoch_ms_to_rfc3339(task.updated_at),
        }
    }
}

/// list_tasks 出参：total 为当前筛选条件下总数（design.md §4.2）。
#[derive(Debug, Clone, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct TaskPageDto {
    pub total: u64,
    pub tasks: Vec<TaskDto>,
}

/// create_task 入参（design.md §4.1 schema）。
#[derive(Debug, Clone, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct CreateTaskInput {
    /// 任务标题（必填，1~500 字符）
    pub title: String,
    /// 任务详情（可选，最长 5000 字符）
    pub description: Option<String>,
    /// 优先级：0=无 1=低 2=中 3=高（缺省 0）
    #[serde(default)]
    pub priority: Option<i64>,
    /// 到期时间，RFC3339（可选）
    pub due_at: Option<String>,
}

/// update_task 入参（design.md §4.3 schema）。
///
/// `description` / `due_at` 为双层 Option：字段缺席 = 不动；显式 null = 清空。
#[derive(Debug, Clone, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct UpdateTaskInput {
    /// 任务 id（必填）
    pub id: String,
    /// 新标题（可选，1~500 字符）
    pub title: Option<String>,
    /// 新详情；传 null 清空（可选）
    pub description: Option<Option<String>>,
    /// 新优先级（可选）
    pub priority: Option<i64>,
    /// 新到期时间 RFC3339；传 null 清除到期（可选）
    pub due_at: Option<Option<String>>,
}

/// UTC epoch 毫秒 → RFC3339 UTC 字符串（如 `2026-11-25T01:00:00Z`）。
/// 非整秒时带毫秒小数（`...:SS.mmmZ`）。
pub fn epoch_ms_to_rfc3339(epoch_ms: i64) -> String {
    let secs = epoch_ms.div_euclid(1000);
    let millis = epoch_ms.rem_euclid(1000);
    let days = secs.div_euclid(86_400);
    let second_of_day = secs.rem_euclid(86_400);
    let (year, month, day) = civil_from_days(days);
    let hour = second_of_day / 3_600;
    let minute = (second_of_day % 3_600) / 60;
    let second = second_of_day % 60;
    if millis == 0 {
        format!("{year:04}-{month:02}-{day:02}T{hour:02}:{minute:02}:{second:02}Z")
    } else {
        format!("{year:04}-{month:02}-{day:02}T{hour:02}:{minute:02}:{second:02}.{millis:03}Z")
    }
}

/// RFC3339 → UTC epoch 毫秒。接受 `Z` / `±HH:MM` / `±HHMM` / `±HH` 时区偏移，
/// 小数秒截断到毫秒；不符合 RFC3339 时返回人类可读中文错误。
pub fn rfc3339_to_epoch_ms(input: &str) -> Result<i64, String> {
    let invalid = || {
        format!(
            "无效的时间 {input:?}：应为 RFC3339，例如 2026-11-25T01:00:00Z 或 2026-11-25T09:00:00+08:00"
        )
    };
    let bytes = input.as_bytes();
    if bytes.len() < 20 {
        return Err(invalid());
    }
    if bytes[4] != b'-'
        || bytes[7] != b'-'
        || (bytes[10] != b'T' && bytes[10] != b't')
        || bytes[13] != b':'
        || bytes[16] != b':'
    {
        return Err(invalid());
    }
    let two = |r: std::ops::Range<usize>| -> Result<i64, String> {
        std::str::from_utf8(&bytes[r])
            .ok()
            .and_then(|s| s.parse::<i64>().ok())
            .ok_or_else(invalid)
    };
    let year = two(0..4)?;
    let month = two(5..7)?;
    let day = two(8..10)?;
    let hour = two(11..13)?;
    let minute = two(14..16)?;
    let second = two(17..19)?;

    if !(1..=12).contains(&month) {
        return Err(invalid());
    }
    let leap = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);
    let days_in_month = match month {
        1 | 3 | 5 | 7 | 8 | 10 | 12 => 31,
        4 | 6 | 9 | 11 => 30,
        _ => {
            if leap {
                29
            } else {
                28
            }
        }
    };
    if !(1..=days_in_month).contains(&day) {
        return Err(invalid());
    }
    if hour > 23 || minute > 59 || second > 59 {
        return Err(invalid());
    }

    // 可选小数秒（1~9 位，截断到毫秒）
    let mut index = 19;
    let mut extra_ms = 0_i64;
    if bytes.get(index) == Some(&b'.') {
        let start = index + 1;
        let mut end = start;
        while end < bytes.len() && bytes[end].is_ascii_digit() {
            end += 1;
        }
        if end == start {
            return Err(invalid());
        }
        let first_three: String = input[start..end.min(start + 3)].to_string();
        extra_ms = format!("{first_three:0<3}")
            .parse::<i64>()
            .map_err(|_| invalid())?;
        index = end;
    }

    // 时区偏移
    let offset_secs = match bytes.get(index) {
        Some(b'Z') | Some(b'z') => {
            index += 1;
            0_i64
        }
        Some(&sign @ (b'+' | b'-')) => {
            let sign = if sign == b'+' { 1_i64 } else { -1 };
            let rest = bytes.get(index + 1..).ok_or_else(invalid)?;
            let pair = |r: std::ops::Range<usize>| -> Result<i64, String> {
                std::str::from_utf8(rest.get(r.clone()).ok_or_else(invalid)?)
                    .ok()
                    .and_then(|s| s.parse::<i64>().ok())
                    .ok_or_else(invalid)
            };
            let (offset_hour, offset_minute) = match rest.len() {
                5 => {
                    if rest[2] != b':' {
                        return Err(invalid());
                    }
                    (pair(0..2)?, pair(3..5)?)
                }
                4 => (pair(0..2)?, pair(2..4)?),
                2 => (pair(0..2)?, 0),
                _ => return Err(invalid()),
            };
            if offset_hour > 23 || offset_minute > 59 {
                return Err(invalid());
            }
            index += 1 + rest.len();
            sign * (offset_hour * 3_600 + offset_minute * 60)
        }
        _ => return Err(invalid()),
    };
    if index != bytes.len() {
        return Err(invalid());
    }

    let days = days_from_civil(year, month, day);
    let secs = days * 86_400 + hour * 3_600 + minute * 60 + second - offset_secs;
    Ok(secs * 1000 + extra_ms)
}

/// Howard Hinnant `days_from_civil`：公历日期 → 自 1970-01-01 起的天数。
fn days_from_civil(year: i64, month: i64, day: i64) -> i64 {
    let shifted_year = if month <= 2 { year - 1 } else { year };
    let era = if shifted_year >= 0 {
        shifted_year
    } else {
        shifted_year - 399
    } / 400;
    let year_of_era = shifted_year - era * 400;
    let month_index = (month + 9) % 12; // 3 月 = 0
    let day_of_year = (153 * month_index + 2) / 5 + day - 1;
    let day_of_era = year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year;
    era * 146_097 + day_of_era - 719_468
}

/// Howard Hinnant `civil_from_days`：天数 → 公历 (年, 月, 日)。
fn civil_from_days(days: i64) -> (i64, i64, i64) {
    let shifted = days + 719_468;
    let era = if shifted >= 0 {
        shifted
    } else {
        shifted - 146_096
    } / 146_097;
    let day_of_era = shifted - era * 146_097;
    let year_of_era =
        (day_of_era - day_of_era / 1_460 + day_of_era / 36_524 - day_of_era / 146_096) / 365;
    let year = year_of_era + era * 400;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let month_index = (5 * day_of_year + 2) / 153;
    let day = day_of_year - (153 * month_index + 2) / 5 + 1;
    let month = if month_index < 10 {
        month_index + 3
    } else {
        month_index - 9
    };
    (
        if month <= 2 { year + 1 } else { year },
        month,
        day,
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rfc3339_roundtrip_utc() {
        let ms = 1_795_568_400_000_i64; // 2026-11-25T01:00:00Z
        let text = epoch_ms_to_rfc3339(ms);
        assert_eq!(text, "2026-11-25T01:00:00Z");
        assert_eq!(rfc3339_to_epoch_ms(&text).unwrap(), ms);
    }

    #[test]
    fn rfc3339_roundtrip_with_millis() {
        let ms = 1_795_568_400_123_i64;
        let text = epoch_ms_to_rfc3339(ms);
        assert_eq!(text, "2026-11-25T01:00:00.123Z");
        assert_eq!(rfc3339_to_epoch_ms(&text).unwrap(), ms);
    }

    #[test]
    fn rfc3339_accepts_timezone_offsets() {
        // 2026-11-25T09:00:00+08:00 == 2026-11-25T01:00:00Z
        assert_eq!(
            rfc3339_to_epoch_ms("2026-11-25T09:00:00+08:00").unwrap(),
            rfc3339_to_epoch_ms("2026-11-25T01:00:00Z").unwrap()
        );
        // 紧凑与分钟偏移形式（RFC3339 时刻本身仍带 HH:MM:SS）
        assert_eq!(
            rfc3339_to_epoch_ms("2026-11-25T09:00:00+0800").unwrap(),
            rfc3339_to_epoch_ms("2026-11-25T01:00:00Z").unwrap()
        );
        assert_eq!(
            rfc3339_to_epoch_ms("2026-11-25T02:30:00+01:30").unwrap(),
            rfc3339_to_epoch_ms("2026-11-25T01:00:00Z").unwrap()
        );
        // 小数秒截断到毫秒
        assert_eq!(
            rfc3339_to_epoch_ms("2026-11-25T01:00:00.999999Z").unwrap(),
            1_795_568_400_999_i64
        );
        // 小写 t / z
        assert_eq!(
            rfc3339_to_epoch_ms("2026-11-25t01:00:00z").unwrap(),
            1_795_568_400_000_i64
        );
    }

    #[test]
    fn rfc3339_rejects_invalid_input() {
        for bad in [
            "",
            "2026-11-25",
            "2026-11-25 01:00:00Z",       // 空格分隔不接受（RFC3339 用 T/t）
            "2026-13-01T00:00:00Z",       // 月份越界
            "2026-02-30T00:00:00Z",       // 日越界
            "2026-11-25T24:00:00Z",       // 小时越界
            "2026-11-25T01:00Z",          // 缺秒
            "2026-11-25T01:00:00",        // 缺时区
            "2026-11-25T01:00:00Z ",      // 尾随空白
            "2026-02-29T00:00:00Z",       // 平年 2 月 29 日
        ] {
            assert!(
                rfc3339_to_epoch_ms(bad).is_err(),
                "应拒绝非法输入: {bad:?}"
            );
        }
        // 闰年 2 月 29 日合法
        assert!(rfc3339_to_epoch_ms("2028-02-29T00:00:00Z").is_ok());
    }

    #[test]
    fn epoch_ms_boundary_dates() {
        // Unix 纪元起点
        assert_eq!(epoch_ms_to_rfc3339(0), "1970-01-01T00:00:00Z");
        assert_eq!(rfc3339_to_epoch_ms("1970-01-01T00:00:00Z").unwrap(), 0);
        // 负时间（1969-12-31T23:59:59.500Z）
        let text = epoch_ms_to_rfc3339(-500);
        assert_eq!(text, "1969-12-31T23:59:59.500Z");
        assert_eq!(rfc3339_to_epoch_ms(&text).unwrap(), -500);
        // 闰日：2028-02-29T12:00:00Z
        let ms = rfc3339_to_epoch_ms("2028-02-29T12:00:00Z").unwrap();
        assert_eq!(epoch_ms_to_rfc3339(ms), "2028-02-29T12:00:00Z");
    }
}
