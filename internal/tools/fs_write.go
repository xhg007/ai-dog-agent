package tools

import (
	"context"
	"errors"
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
	if err := writeFileAtomic(abs, []byte(content)); err != nil {
		return 0, fmt.Errorf("写入文件失败: %w", err)
	}
	return len(content), nil
}

// workspaceRel 计算 abs 相对 workspace 根的路径，用于备份目录内的镜像路径。
// 拒绝越界与根目录本身，保证 filepath.Join(backupDir, rel) 不会逃出 backupDir。
func workspaceRel(ws *workspace.Manager, abs string) (string, error) {
	root := ws.Root()
	if root == "" {
		return "", workspace.ErrNoWorkspace
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("计算 workspace 相对路径失败: %w", err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", workspace.ErrOutsideWorkspace, abs)
	}
	return rel, nil
}

// WriteFileWithBackup 同 WriteFileContent，但覆盖已有文件前先备份原内容到 backupDir。
func WriteFileWithBackup(ws *workspace.Manager, relPath, content, backupDir string) (int, error) {
	abs, err := ws.Resolve(relPath)
	if err != nil {
		return 0, err
	}
	if backupDir != "" {
		// 备份路径统一用 workspace 相对路径：relPath 可能是绝对路径，
		// 直接 Join 会产生畸形路径（C:\backups\C:\ws\a.txt）。
		rel, rerr := workspaceRel(ws, abs)
		if rerr != nil {
			return 0, rerr
		}
		if data, rerr := os.ReadFile(abs); rerr == nil {
			if berr := backupFile(backupDir, rel, data); berr != nil {
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
	if werr := writeFileAtomic(abs, []byte(updated)); werr != nil {
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
		rel, rerr := workspaceRel(ws, abs)
		if rerr != nil {
			return 0, rerr
		}
		if data, rerr := os.ReadFile(abs); rerr == nil {
			if berr := backupFile(backupDir, rel, data); berr != nil {
				return 0, fmt.Errorf("备份失败: %w", berr)
			}
		}
	}
	return EditFileContent(ws, relPath, oldStr, newStr)
}

// writeFileAtomic 同目录临时文件 + rename 原子替换，避免崩溃/断电把目标文件截断。
// 临时文件与目标同目录，保证 rename 不跨文件系统。
func writeFileAtomic(abs string, data []byte) error {
	dir := filepath.Dir(abs)
	tmp, err := os.CreateTemp(dir, ".aidog-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	remove := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		remove()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		remove()
		return err
	}
	if err := tmp.Close(); err != nil {
		remove()
		return err
	}

	// 保留原文件权限；新文件用 0644（与 os.WriteFile 行为一致）
	mode := os.FileMode(0o644)
	if info, err := os.Stat(abs); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		remove()
		return err
	}
	if err := os.Rename(tmpName, abs); err != nil {
		remove()
		return err
	}
	return nil
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
// relPath 先经 workspace 安全层解析，再用规范化后的相对路径定位备份，
// 避免绝对路径/越界路径读到 backupDir 之外的文件。
func RestoreFromBackup(ws *workspace.Manager, backupDir, relPath string) error {
	abs, err := ws.Resolve(relPath)
	if err != nil {
		return err
	}
	rel, err := workspaceRel(ws, abs)
	if err != nil {
		return err
	}
	src := filepath.Join(backupDir, rel)
	if !withinDir(backupDir, src) {
		return fmt.Errorf("回滚路径越出备份目录: %s", relPath)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("备份不存在: %w", err)
	}
	_, err = WriteFileContent(ws, rel, string(data))
	return err
}

// withinDir 判断 p 是否位于 dir 内（含相等），用于备份目录边界校验。
func withinDir(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
			if appr == nil {
				return "", errors.New("写操作缺少审批通道，已拒绝执行")
			}
			abs, rerr := ws.Resolve(args.Path)
			if rerr != nil {
				return "", rerr
			}
			oldData, _ := os.ReadFile(abs)
			preview := buildDiffPreview(string(oldData), args.Content, 60)
			ok, aerr := appr.RequestContext(ctx, sessionID, "write_file", args.Path, preview)
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
			if appr == nil {
				return "", errors.New("写操作缺少审批通道，已拒绝执行")
			}
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
			ok, aerr := appr.RequestContext(ctx, sessionID, "edit_file", args.Path, preview)
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
