// 文件附件的 MCP 工具（attach_file / attach_file_ref / remove_file，2026-11-26）。
// 两种模式：
//   - attach_file：把本地文件**拷贝**进应用数据目录（与源文件解耦）；
//   - attach_file_ref：只记录源文件的绝对路径（**复制引用地址**，不拷贝文件；
//     源文件移动/删除后不可用，展示时标注「引用」）。
//
// 校验走 addTypedTool 的 validate 钩子 + store 层权威校验；错误出口与既有
// 工具一致（invalid_params / businessError）。
package mcp

import (
	"context"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"todo-mcp/app/store"
)

// attachFileSchema 对应 attach_file 入参。
const attachFileSchema = `{
  "type": "object",
  "properties": {
    "id":   { "type": "string", "description": "任务 id（必填）" },
    "path": { "type": "string", "maxLength": 1000, "description": "文件的绝对路径（必填；拷贝进应用数据目录，单文件 ≤ 50 MiB，每任务 ≤ 20 个）" }
  },
  "required": ["id", "path"],
  "additionalProperties": false
}`

// attachFileRefSchema 对应 attach_file_ref 入参（复制引用地址，不拷贝文件）。
const attachFileRefSchema = `{
  "type": "object",
  "properties": {
    "id":   { "type": "string", "description": "任务 id（必填）" },
    "path": { "type": "string", "maxLength": 1000, "description": "源文件绝对路径（必填；只存引用地址不拷贝，源文件须存在，每任务 ≤ 20 个）" }
  },
  "required": ["id", "path"],
  "additionalProperties": false
}`

// removeFileSchema 对应 remove_file 入参。
const removeFileSchema = `{
  "type": "object",
  "properties": {
    "id":      { "type": "string", "description": "任务 id（必填）" },
    "file_id": { "type": "string", "description": "文件 id（必填，即任务出参 files[].id）" }
  },
  "required": ["id", "file_id"],
  "additionalProperties": false
}`

type attachFileInput struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

func validateAttachPath(in attachFileInput) error {
	if in.ID == "" {
		return invalidParams("任务 id 不能为空")
	}
	if in.Path == "" {
		return invalidParams("文件路径不能为空")
	}
	if !filepath.IsAbs(in.Path) {
		return invalidParams("文件路径必须是绝对路径，收到 %q", in.Path)
	}
	return nil
}

type removeFileInput struct {
	ID     string `json:"id"`
	FileID string `json:"file_id"`
}

// attachFileOut 对应 attach_file / attach_file_ref 出参（任务快照 + 新文件）。
type attachFileOut struct {
	Task taskOut `json:"task"`
	File fileOut `json:"file"`
}

// removeFileOut 对应 remove_file 出参（删除后的任务快照）。
type removeFileOut struct {
	Task taskOut `json:"task"`
}

// registerFileTools 注册三个文件附件工具。
func registerFileTools(server *mcp.Server, repo *store.TaskRepo) {
	addTypedTool(server, "attach_file", "给任务添加一个文件附件（文件拷贝进应用数据目录，与源文件解耦）",
		attachFileSchema, attachFileSchema,
		validateAttachPath,
		func(_ context.Context, in attachFileInput) (attachFileOut, error) {
			file, err := repo.AddFileCopy(in.ID, in.Path)
			if err != nil {
				return attachFileOut{}, businessError("添加文件失败", err)
			}
			task, err := repo.Get(in.ID)
			if err != nil {
				return attachFileOut{}, businessError("添加文件失败", err)
			}
			return attachFileOut{Task: newTaskOut(task), File: newFileOuts([]store.TaskFile{*file})[0]}, nil
		})

	addTypedTool(server, "attach_file_ref", "给任务添加一个文件引用（只记录源文件路径，不拷贝文件；源文件须存在）",
		attachFileRefSchema, attachFileRefSchema,
		validateAttachPath,
		func(_ context.Context, in attachFileInput) (attachFileOut, error) {
			file, err := repo.AddFileRef(in.ID, in.Path)
			if err != nil {
				return attachFileOut{}, businessError("添加文件引用失败", err)
			}
			task, err := repo.Get(in.ID)
			if err != nil {
				return attachFileOut{}, businessError("添加文件引用失败", err)
			}
			return attachFileOut{Task: newTaskOut(task), File: newFileOuts([]store.TaskFile{*file})[0]}, nil
		})

	addTypedTool(server, "remove_file", "删除任务的一个文件附件（copy 模式连文件一并删除；ref 模式不碰源文件）",
		removeFileSchema, removeFileSchema,
		func(in removeFileInput) error {
			if in.ID == "" {
				return invalidParams("任务 id 不能为空")
			}
			if in.FileID == "" {
				return invalidParams("文件 id 不能为空")
			}
			return nil
		},
		func(_ context.Context, in removeFileInput) (removeFileOut, error) {
			if err := repo.RemoveFile(in.ID, in.FileID); err != nil {
				return removeFileOut{}, businessError("删除文件失败", err)
			}
			task, err := repo.Get(in.ID)
			if err != nil {
				return removeFileOut{}, businessError("删除文件失败", err)
			}
			return removeFileOut{Task: newTaskOut(task)}, nil
		})
}
