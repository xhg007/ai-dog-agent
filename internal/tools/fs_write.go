package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/workspace"
)

// WriteFileContent 在 workspace 内写入文件（自动创建父目录），返回写入字节数。
func WriteFileContent(ws *workspace.Manager, relPath, content string) (int, error) {
	abs, err := ws.Resolve(relPath)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return 0, fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return 0, fmt.Errorf("写入文件失败: %w", err)
	}
	return len(content), nil
}

// WriteFileWithBackup 同 WriteFileContent，但覆盖已有文件前先备份原内容到 backupDir。
func WriteFileWithBackup(ws *workspace.Manager, relPath, content, backupDir string) (int, error) {
	abs, err := ws.Resolve(relPath)
	if err != nil {
		return 0, err
	}
	if backupDir != "" {
		if data, rerr := os.ReadFile(abs); rerr == nil {
			if berr := backupFile(backupDir, relPath, data); berr != nil {
				return 0, fmt.Errorf("备份失败: %w", berr)
			}
		}
	}
	return WriteFileContent(ws, relPath, content)
}

// EditFileContent 在 workspace 内做精确文本替换。
// 查找串必须恰好出现一次（多次匹配报错、零次报错），防止误替换。
func EditFileContent(ws *workspace.Manager, relPath, oldStr, newStr string) (int, error) {
	abs, err := ws.Resolve(relPath)
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return 0, fmt.Errorf("读取文件失败: %w", err)
	}
	content := string(data)
	count := strings.Count(content, oldStr)
	if count == 0 {
		return 0, fmt.Errorf("查找串在 %s 中不存在", relPath)
	}
	if count > 1 {
		return 0, fmt.Errorf("查找串在 %s 中匹配 %d 次，请提供更长的唯一上下文", relPath, count)
	}
	updated := strings.Replace(content, oldStr, newStr, 1)
	if werr := os.WriteFile(abs, []byte(updated), 0o644); werr != nil {
		return 0, fmt.Errorf("写回文件失败: %w", werr)
	}
	return 1, nil
}

// EditFileWithBackup 同 EditFileContent，替换前备份原文件到 backupDir。
func EditFileWithBackup(ws *workspace.Manager, relPath, oldStr, newStr, backupDir string) (int, error) {
	abs, err := ws.Resolve(relPath)
	if err != nil {
		return 0, err
	}
	if backupDir != "" {
		if data, rerr := os.ReadFile(abs); rerr == nil {
			if berr := backupFile(backupDir, relPath, data); berr != nil {
				return 0, fmt.Errorf("备份失败: %w", berr)
			}
		}
	}
	return EditFileContent(ws, relPath, oldStr, newStr)
}

// backupFile 将原始内容按相对路径镜像存入备份目录。
func backupFile(backupDir, relPath string, data []byte) error {
	dst := filepath.Join(backupDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// RestoreFromBackup 从备份目录恢复文件到 workspace。
func RestoreFromBackup(ws *workspace.Manager, backupDir, relPath string) error {
	src := filepath.Join(backupDir, filepath.FromSlash(relPath))
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("备份不存在: %w", err)
	}
	_, err = WriteFileContent(ws, relPath, string(data))
	return err
}

// buildDiffPreview 生成审批预览（简单行级 diff：旧行 - / 新行 +，截断）。
func buildDiffPreview(oldContent, newContent string, maxLines int) string {
	var sb strings.Builder
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	for _, l := range oldLines {
		fmt.Fprintf(&sb, "- %s\n", l)
	}
	sb.WriteString("~~~\n")
	for _, l := range newLines {
		fmt.Fprintf(&sb, "+ %s\n", l)
	}
	out := sb.String()
	lines := strings.Split(out, "\n")
	if len(lines) > maxLines {
		out = strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n…（共 %d 行，已截断）", len(lines))
	}
	return out
}

// WriteFileArgs write_file 参数。
type WriteFileArgs struct {
	Path    string `json:"path" jsonschema:"workspace 内的相对路径"`
	Content string `json:"content" jsonschema:"完整文件内容（UTF-8）"`
}

// EditFileArgs edit_file 参数。
type EditFileArgs struct {
	Path    string `json:"path" jsonschema:"workspace 内的相对路径"`
	OldText string `json:"oldText" jsonschema:"要替换的原文（必须在文件中唯一匹配）"`
	NewText string `json:"newText" jsonschema:"替换后的新文本"`
}

// WriteToolset 写操作工具集：write_file / edit_file，均经审批门 + 备份。
func WriteToolset(ws *workspace.Manager, backupDir string, appr *approval.Manager, sessionID string) []tool.BaseTool {
	writeTool, err := utils.InferTool("write_file",
		"在项目工作区内写入（覆盖）文件。写前需用户审批，原文件自动备份。",
		func(ctx context.Context, args WriteFileArgs) (string, error) {
			abs, rerr := ws.Resolve(args.Path)
			if rerr != nil {
				return "", rerr
			}
			oldData, _ := os.ReadFile(abs)
			preview := buildDiffPreview(string(oldData), args.Content, 60)
			ok, aerr := appr.Request(sessionID, "write_file", args.Path, preview)
			if aerr != nil {
				return "", aerr
			}
			_ = ok
			n, werr := WriteFileWithBackup(ws, args.Path, args.Content, backupDir)
			if werr != nil {
				return "", werr
			}
			return fmt.Sprintf("已写入 %s（%d 字节）", args.Path, n), nil
		})
	if err != nil {
		return []tool.BaseTool{}
	}

	editTool, err := utils.InferTool("edit_file",
		"对工作区内文件做精确文本替换（oldText 必须唯一匹配）。写前需用户审批，原文件自动备份。",
		func(ctx context.Context, args EditFileArgs) (string, error) {
			abs, rerr := ws.Resolve(args.Path)
			if rerr != nil {
				return "", rerr
			}
			oldData, rerr := os.ReadFile(abs)
			if rerr != nil {
				return "", fmt.Errorf("读取文件失败: %w", rerr)
			}
			content := string(oldData)
			if strings.Count(content, args.OldText) != 1 {
				return "", fmt.Errorf("oldText 在 %s 中必须恰好匹配一次（当前 %d 次）", args.Path, strings.Count(content, args.OldText))
			}
			preview := buildDiffPreview(args.OldText, args.NewText, 60)
			ok, aerr := appr.Request(sessionID, "edit_file", args.Path, preview)
			if aerr != nil {
				return "", aerr
			}
			_ = ok
			n, werr := EditFileWithBackup(ws, args.Path, args.OldText, args.NewText, backupDir)
			if werr != nil {
				return "", werr
			}
			return fmt.Sprintf("已编辑 %s（替换 %d 处）", args.Path, n), nil
		})
	if err != nil {
		return []tool.BaseTool{}
	}

	return []tool.BaseTool{writeTool, editTool}
}
