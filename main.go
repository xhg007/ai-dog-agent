package main

import (
	"embed"
	"fmt"
	"log"
	"log/slog"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"ai-dog-agent/internal/agent"
	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/logging"
	"ai-dog-agent/internal/mcp"
	"ai-dog-agent/internal/service"
	"ai-dog-agent/internal/store"
	"ai-dog-agent/internal/task"
	"ai-dog-agent/internal/workspace"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if err := run(); err != nil {
		// 此时 run 的 defer 已执行完（统计库/写入器/MCP 均已关闭），不会留下未落盘的 WAL。
		log.Fatal(err)
	}
}

// run 装配并启动应用。错误通过返回值上抛，而不是在各处 log.Fatal：
// log.Fatal 内部是 os.Exit，会跳过 defer，导致统计库与 MCP 子进程未正常关闭。
func run() error {
	cfgMgr, err := config.NewManager()
	if err != nil {
		return fmt.Errorf("初始化配置失败: %w", err)
	}
	cfgDir, err := config.Dir()
	if err != nil {
		return fmt.Errorf("获取配置目录失败: %w", err)
	}
	if err := logging.Setup(filepath.Join(cfgDir, "logs")); err != nil {
		return fmt.Errorf("初始化日志失败: %w", err)
	}

	// 工作区（从配置恢复上次使用）
	wsMgr := workspace.New()
	if cfgMgr.Get().WorkspacePath != "" {
		if err := wsMgr.SetRoot(cfgMgr.Get().WorkspacePath); err != nil {
			slog.Warn("恢复上次 workspace 失败", "err", err)
		}
	}

	bus := eventbus.New()
	backupRoot := filepath.Join(cfgDir, "backups")
	sessionDir := filepath.Join(cfgDir, "sessions")

	// 统计持久层：DuckDB 单文件 + 异步写入器。
	// 打开失败（文件损坏/被占用）不应阻止应用启动：降级为内存库，
	// 本次运行仍能记录与展示用量，只是重启后历史统计丢失。
	// 损坏文件原样保留，便于人工排查或删除。
	statDB, err := store.Open(filepath.Join(cfgDir, "stats.duckdb"))
	if err != nil {
		slog.Warn("统计数据库不可用，已降级为内存库（本次统计不会持久化）", "err", err)
		statDB, err = store.Open(":memory:")
		if err != nil {
			return fmt.Errorf("初始化内存统计库失败: %w", err)
		}
	}
	defer statDB.Close()
	writer := store.NewAsyncWriter(statDB, 512)
	defer writer.Close()

	apprMgr := approval.New(bus)
	taskMgr := task.New(statDB, writer, bus)
	// 外部 MCP Server：按配置懒连接；应用退出时关闭子进程
	mcpMgr := mcp.NewManager(apprMgr)
	defer mcpMgr.Close()
	chatSvc := agent.NewChatService(cfgMgr, wsMgr, bus, apprMgr, backupRoot, sessionDir, taskMgr, mcpMgr)
	cfgSvc := service.NewConfigService(cfgMgr, wsMgr)
	wsSvc := service.NewWorkspaceService(cfgMgr, wsMgr)
	apprSvc := service.NewApprovalService(apprMgr, wsMgr, backupRoot)
	mcpSvc := service.NewMCPService(wsMgr, mcpMgr)
	taskSvc := service.NewTaskService(taskMgr)

	app := application.New(application.Options{
		Name:        "ai-dog-agent",
		Description: "桌面端多 Agent 编码开发助手",
		Services: []application.Service{
			application.NewService(cfgSvc),
			application.NewService(wsSvc),
			application.NewService(apprSvc),
			application.NewService(taskSvc),
			application.NewService(mcpSvc),
			application.NewService(chatSvc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	bus.SetApp(app)

	// 注册自定义事件，供绑定生成器生成强类型 TS API。
	application.RegisterEvent[eventbus.Event](eventbus.EventName)

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "ai-dog-agent",
		Width:     1400,
		Height:    900,
		MinWidth:  900,
		MinHeight: 600,
		// 无边框窗口：标题栏由前端 TitleBar 组件提供
		//（拖拽区 --wails-draggable + 最小化/最大化/关闭按钮走 @wailsio/runtime Window API）
		Frameless: true,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(13, 17, 23),
		URL:              "/",
	})

	slog.Info("ai-dog-agent 启动")
	return app.Run()
}
