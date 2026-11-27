package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/workspace"
)

func newProviderService(t *testing.T, baseURL string) (*ConfigService, string) {
	t.Helper()
	mgr, err := config.NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatalf("创建配置: %v", err)
	}
	cfg := mgr.Get()
	cfg.Providers = []config.ProviderConfig{{
		ID: "p1", Name: "Test", BaseURL: baseURL, APIKey: "sk-test",
	}}
	if err := mgr.Save(cfg); err != nil {
		t.Fatalf("保存配置: %v", err)
	}
	return NewConfigService(mgr, workspace.New()), "p1"
}

func TestDetectProviderModels(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"b"},{"id":"a"},{"id":"b"}]}`)
	}))
	defer srv.Close()

	svc, id := newProviderService(t, srv.URL+"/v1")
	got, err := svc.DetectProviderModels(id)
	if err != nil {
		t.Fatalf("检测模型: %v", err)
	}
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("模型列表 = %v，期望去重并按字母排序 [a b]", got)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("请求路径 = %q，期望 /v1/models", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestDetectProviderModelsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()

	svc, id := newProviderService(t, srv.URL)
	if _, err := svc.DetectProviderModels(id); err == nil {
		t.Fatal("非 2xx 响应应返回错误")
	} else if !strings.Contains(err.Error(), "401") {
		t.Fatalf("错误信息应包含状态码: %v", err)
	}
}

func TestDetectProviderModelsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	svc, id := newProviderService(t, srv.URL)
	if _, err := svc.DetectProviderModels(id); err == nil {
		t.Fatal("空模型列表应返回错误")
	}
}

func TestDetectProviderModelsUnknownProvider(t *testing.T) {
	mgr, err := config.NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatalf("创建配置: %v", err)
	}
	svc := NewConfigService(mgr, workspace.New())
	if _, err := svc.DetectProviderModels("missing"); err == nil {
		t.Fatal("不存在的供应商应返回错误")
	}
}
