package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"ai-dog-agent/internal/config"
)

// DetectProviderModels 调用供应商的 GET /models（OpenAI 兼容），返回可用模型 ID（去重排序）。
// 只读探测，不修改配置；前端让用户勾选后再加入模型列表。
func (c *ConfigService) DetectProviderModels(providerID string) ([]string, error) {
	cfg := c.mgr.Get()
	var provider config.ProviderConfig
	found := false
	for _, p := range cfg.Providers {
		if p.ID == providerID {
			provider, found = p, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("供应商不存在: %s", providerID)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("供应商 %q 未配置 BaseURL", provider.Name)
	}

	req, err := http.NewRequest(http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	if provider.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 %s 失败: %w", baseURL+"/models", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("检测模型失败（HTTP %d）: %s", resp.StatusCode, snippet(body))
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("解析模型列表失败: %w", err)
	}

	seen := make(map[string]bool, len(payload.Data))
	out := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("供应商未返回任何模型（HTTP %d）", resp.StatusCode)
	}
	sort.Strings(out)
	return out, nil
}

// snippet 截断响应体用于错误提示（按 rune 截断，避免切断中文）。
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	r := []rune(s)
	if len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}
