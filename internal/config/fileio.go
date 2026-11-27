package config

// 配置文件统一使用 YAML（.yml）。旧版 JSON 配置在首次加载时自动迁移：
// 解析成功后写入 YAML，并把原文件重命名为 .bak 以便回滚。

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// readYAMLFile 读取 YAML 到 dst；文件不存在或为空返回 false（视为未配置）。
func readYAMLFile(path string, dst any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return false, nil
	}
	if err := yaml.Unmarshal(data, dst); err != nil {
		return false, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return true, nil
}

// writeYAMLAtomic 以 0600 权限原子写入 YAML（空切片写为 []，便于人工编辑）。
func writeYAMLAtomic(path string, v any) error {
	switch t := v.(type) {
	case []MCPServerConfig:
		if t == nil {
			v = []MCPServerConfig{}
		}
	case []SkillConfig:
		if t == nil {
			v = []SkillConfig{}
		}
	case []ModelConfig:
		if t == nil {
			v = []ModelConfig{}
		}
	case []ProviderConfig:
		if t == nil {
			v = []ProviderConfig{}
		}
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("序列化 %s 失败: %w", filepath.Base(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换 %s 失败: %w", filepath.Base(path), err)
	}
	return nil
}

// migrateJSONToYAML 把旧 JSON 配置迁移为 YAML：
//   - 目标已存在或旧文件不存在时不做任何事；
//   - 解析失败返回错误（不静默丢配置）；
//   - 写入成功后旧文件重命名为 .bak。
func migrateJSONToYAML(targetPath, legacyPath string, dst any) (bool, error) {
	if _, err := os.Stat(targetPath); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	data, err := os.ReadFile(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取旧配置 %s 失败: %w", legacyPath, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return false, fmt.Errorf("迁移旧配置 %s 失败: %w", legacyPath, err)
	}
	if err := writeYAMLAtomic(targetPath, dst); err != nil {
		return false, err
	}
	if err := os.Rename(legacyPath, legacyPath+".bak"); err != nil {
		slog.Warn("旧配置已迁移，但备份重命名失败", "file", legacyPath, "err", err)
	}
	return true, nil
}
