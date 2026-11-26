// Package prompts 存放并渲染嵌入二进制的提示词模板。
//
// 模板文件位于本目录（*.tmpl），通过 go:embed 打包进二进制；
// 调用方只传数据，文本由模板负责，避免在 Go 代码里拼接长提示词。
package prompts

import (
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed *.tmpl
var promptFS embed.FS

var funcs = template.FuncMap{
	"join": strings.Join,
}

// 解析失败属于编译期资源错误，直接在启动时暴露。
var templates = template.Must(template.New("prompts").Funcs(funcs).ParseFS(promptFS, "*.tmpl"))

// Render 渲染指定模板（文件名，如 "orchestrator.tmpl"）。
func Render(name string, data any) (string, error) {
	var sb strings.Builder
	if err := templates.ExecuteTemplate(&sb, name, data); err != nil {
		return "", fmt.Errorf("渲染提示词 %s 失败: %w", name, err)
	}
	return sb.String(), nil
}

// Names 返回已嵌入的模板名（测试/诊断用）。
func Names() []string {
	list := templates.Templates()
	out := make([]string, 0, len(list))
	for _, t := range list {
		out = append(out, t.Name())
	}
	return out
}
