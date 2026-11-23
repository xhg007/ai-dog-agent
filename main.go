package main

import (
	"embed"
	"log"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"ai-dog-agent/internal/agent"
	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/logging"
	"ai-dog-agent/internal/service"
	"ai-dog-agent/internal/store"
	"ai-dog-agent/internal/task"
	"ai-dog-agent/internal/workspace"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// 配置与日志
	cfgMgr, err := config.NewManager()
	if err != nil {
		log.Fatalf("初始化配置失败: %v", err)
	}
	cfgDir, _ := config.Dir()
	if err := logging.Setup(cfgDir + "/logs"); err != nil {
		log.Fatalf("初始化日志失败: %v", err)
	}
	slog := log.Default()

	// 工作区（从配置恢复上次使用）
	wsMgr := workspace.New()
	if cfgMgr.Get().WorkspacePath != "" {
		if err := wsMgr.SetRoot(cfgMgr.Get().WorkspacePath); err != nil {
			slog.Printf("恢复上次 workspace 失败: %v", err)
		}
	}

	// 服务装配
	bus := eventbus.New()
	backupRoot := filepath.Join(cfgDir, "backups")
	sessionDir := filepath.Join(cfgDir, "sessions")

	// 统计持久层（M5 FR-5.2）：DuckDB 单文件 + 异步写入器
	statDB, err := store.Open(filepath.Join(cfgDir, "stats.duckdb"))
	if err != nil {
		log.Fatalf("初始化统计数据库失败: %v", err)
	}
	defer statDB.Close()
	writer := store.NewAsyncWriter(statDB, 512)
	defer writer.Close()

	apprMgr := approval.New(bus)
	taskMgr := task.New(statDB, writer, bus)
	chatSvc := agent.NewChatService(cfgMgr, wsMgr, bus, apprMgr, backupRoot, sessionDir, taskMgr)
	cfgSvc := service.NewConfigService(cfgMgr, wsMgr)
	wsSvc := service.NewWorkspaceService(cfgMgr, wsMgr)
	apprSvc := service.NewApprovalService(apprMgr, wsMgr, backupRoot)
	taskSvc := service.NewTaskService(taskMgr)

	app := application.New(application.Options{
		Name:        "ai-dog-agent",
		Description: "桌面端多 Agent 编码开发助手",
		Services: []application.Service{
			application.NewService(cfgSvc),
			application.NewService(wsSvc),
			application.NewService(apprSvc),
			application.NewService(taskSvc),
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
		Title:  "ai-dog-agent",
		Width:  1400,
		Height: 900,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(13, 17, 23),
		URL:              "/",
	})

	slog.Println("ai-dog-agent 启动")
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
