// Ported from github.com/fpt/go-dev-mcp (MIT License).
// Adapted: adds an explicit timeout, status validation and Chinese errors.
package devtools

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// httpTimeout 单次文档抓取超时。桌面端不能让模型调用无限挂起。
const httpTimeout = 20 * time.Second

// httpClient 包级客户端，测试可替换 Transport 或整个变量。
var httpClient = &http.Client{Timeout: httpTimeout}

// httpGet 抓取 URL 并返回响应体；非 200 视为错误。调用方负责 Close。
func httpGet(rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "ai-dog-agent/devtools")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络请求失败（请检查网络或代理）: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("文档源返回状态 %d: %s", resp.StatusCode, rawURL)
	}
	return resp.Body, nil
}
