// 图片附件的两个 MCP 工具（attach_image / remove_image，2026-11-26）。
// 图片文件由 store 层拷入数据库目录 images/<任务id>/；出参不暴露磁盘路径。
// 校验走 addTypedTool 的 validate 钩子 + store 层权威校验；错误出口与既有
// 工具一致（invalid_params / businessError）。
package mcp

import (
	"context"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"todo-mcp/app/store"
)

// attachImageSchema 对应 attach_image 入参。
const attachImageSchema = `{
  "type": "object",
  "properties": {
    "id":        { "type": "string", "description": "任务 id（必填）" },
    "path":      { "type": "string", "maxLength": 1000, "description": "图片文件的绝对路径（必填；支持 png / jpg / gif，单张 ≤ 20 MiB，每任务 ≤ 20 张）" }
  },
  "required": ["id", "path"],
  "additionalProperties": false
}`

// removeImageSchema 对应 remove_image 入参。
const removeImageSchema = `{
  "type": "object",
  "properties": {
    "id":       { "type": "string", "description": "任务 id（必填）" },
    "image_id": { "type": "string", "description": "图片 id（必填，即任务出参 images[].id）" }
  },
  "required": ["id", "image_id"],
  "additionalProperties": false
}`

// attachImageInput / removeImageInput 对应两个工具的入参。
type attachImageInput struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type removeImageInput struct {
	ID      string `json:"id"`
	ImageID string `json:"image_id"`
}

// attachImageOut 对应 attach_image 出参（任务快照 + 新图片）。
type attachImageOut struct {
	Task  taskOut  `json:"task"`
	Image imageOut `json:"image"`
}

// removeImageOut 对应 remove_image 出参（删除后的任务快照）。
type removeImageOut struct {
	Task taskOut `json:"task"`
}

// registerImageTools 注册两个图片工具。
func registerImageTools(server *mcp.Server, repo *store.TaskRepo) {
	addTypedTool(server, "attach_image", "给任务添加一张图片附件（文件拷贝进应用数据目录，与源文件解耦）",
		attachImageSchema, attachImageSchema,
		func(in attachImageInput) error {
			if in.ID == "" {
				return invalidParams("任务 id 不能为空")
			}
			if in.Path == "" {
				return invalidParams("图片路径不能为空")
			}
			if !filepath.IsAbs(in.Path) {
				return invalidParams("图片路径必须是绝对路径，收到 %q", in.Path)
			}
			return nil
		},
		func(_ context.Context, in attachImageInput) (attachImageOut, error) {
			image, err := repo.AddImageFile(in.ID, in.Path)
			if err != nil {
				return attachImageOut{}, businessError("添加图片失败", err)
			}
			task, err := repo.Get(in.ID)
			if err != nil {
				return attachImageOut{}, businessError("添加图片失败", err)
			}
			return attachImageOut{Task: newTaskOut(task), Image: newImageOuts([]store.TaskImage{*image})[0]}, nil
		})

	addTypedTool(server, "remove_image", "删除任务的一张图片附件（元数据与文件一并删除）",
		removeImageSchema, removeImageSchema,
		func(in removeImageInput) error {
			if in.ID == "" {
				return invalidParams("任务 id 不能为空")
			}
			if in.ImageID == "" {
				return invalidParams("图片 id 不能为空")
			}
			return nil
		},
		func(_ context.Context, in removeImageInput) (removeImageOut, error) {
			if err := repo.RemoveImage(in.ID, in.ImageID); err != nil {
				return removeImageOut{}, businessError("删除图片失败", err)
			}
			task, err := repo.Get(in.ID)
			if err != nil {
				return removeImageOut{}, businessError("删除图片失败", err)
			}
			return removeImageOut{Task: newTaskOut(task)}, nil
		})
}
