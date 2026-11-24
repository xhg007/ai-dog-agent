# Wails v3 — Application API Reference (Go side)

## Table of Contents
1. [Creating an Application](#creating-an-application)
2. [Core Methods](#core-methods)
3. [Event System](#event-system)
4. [Menus](#menus)
5. [System Tray](#system-tray)
6. [Other Managers](#other-managers)
7. [Platform-Specific Options](#platform-specific-options)

Window management details → `references/windows.md`; dialogs → `references/dialogs.md`.

---

## Creating an Application

```go
import "github.com/wailsapp/wails/v3/pkg/application"

app := application.New(application.Options{
    Name:        "My App",
    Description: "My awesome application",
    Services: []application.Service{
        application.NewService(&MyService{}),
    },
    Mac: application.MacOptions{
        ApplicationShouldTerminateAfterLastWindowClosed: true,
    },
})
```

---

## Core Methods

### `app.Run() error`

Start the application event loop. Blocks until the app quits.

```go
if err := app.Run(); err != nil {
    log.Fatal(err)
}
```

### `app.Quit()`

Gracefully shut down the application (all services get `ServiceShutdown` called).

```go
menu.Add("Quit").OnClick(func(ctx *application.Context) {
    app.Quit()
})
```

### `app.Config() Options`

Returns the current application options.

```go
config := app.Config()
fmt.Println("App name:", config.Name)
```

### Lifecycle callbacks

```go
app.OnShutdown(func() {
    fmt.Println("App shutting down — doing cleanup")
})
```

---

## Event System

Events enable pub/sub communication between Go services, windows, and the frontend.

### `app.Event.Emit(name, data...)`

Broadcast to all listeners (Go and JS).

```go
app.Event.Emit("user-logged-in", map[string]interface{}{
    "username":  "john",
    "timestamp": time.Now(),
})
```

### `app.Event.On(name, callback)`

Listen for custom events (Go side).

```go
app.Event.On("user-logged-in", func(e *application.CustomEvent) {
    data := e.Data.(map[string]interface{})
    fmt.Println("User logged in:", data["username"])
})
```

### System/common events

Application and window events live under `application events.Common.*` constants:

```go
import "github.com/wailsapp/wails/v3/pkg/events"

// Common application events:
events.Common.ApplicationStarted
events.Common.ApplicationShutdown
// Common window events:
events.Common.WindowFocus
events.Common.WindowBlur
events.Common.WindowClosing
events.Common.WindowDidResize
```

### Hooks vs Listeners (critical)

- **Listeners** (`window.OnWindowEvent(event, fn)`) are passive — they cannot cancel or block an event.
- **Hooks** (`window.RegisterHook(event, fn)`) run before listeners and can call `e.Cancel()` to prevent the default behavior. This is the **only** way to intercept e.g. `WindowClosing`.

```go
window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
    e.Cancel()   // prevent the window from closing
})
```

`OnWindowEvent` / `RegisterHook` return an unsubscribe function.

---

## Menus

### Application Menu

```go
menu := app.NewMenu()
menu.AddRole(application.AppMenu)                     // macOS standard app menu (macOS only)

fileMenu := menu.AddMenu("File")
fileMenu.Add("New File").
    SetAccelerator("CmdOrCtrl+N").
    OnClick(func(ctx *application.Context) { newFile() })
fileMenu.Add("Open...").SetAccelerator("CmdOrCtrl+O")
fileMenu.AddSeparator()

editMenu := menu.AddMenu("Edit")
editMenu.AddRole(application.EditMenu)                // standard edit roles (Mac)

app.SetMenu(menu)
```

### Context Menu

```go
ctxMenu := app.NewMenu()
ctxMenu.Add("Copy").OnClick(func(ctx *application.Context) { doCopy() })
ctxMenu.Add("Paste")

// Attach to an element (WML-style data attribute or programmatic)
window.SetContextMenu("my-element", ctxMenu)
```

### Menu roles

`application.AppMenu`, `application.EditMenu`, `application.WindowMenu` provide platform-standard menus (mostly macOS).

---

## System Tray

```go
tray := app.NewSystemTray()
tray.SetLabel("My App")                 // Windows/Linux: tray label
tray.SetIcon(iconBytes)                 // tray icon
tray.SetMenu(myMenu)                    // attach a menu

app.SetSystemTray(tray)
```

---

## Other Managers

```go
app.Window      // WebviewWindow management
app.Menu        // Application menu
app.Dialog      // Native dialogs
app.Event       // Event pub/sub
app.Clipboard   // Clipboard read/write
app.Screen      // Screen info
app.SystemTray  // System tray icon & menu
app.Browser     // Open URLs in system browser
app.Env         // Environment info
```

**Examples:**
```go
// Clipboard
app.Clipboard.SetText("Copied!")
text, _ := app.Clipboard.Text()

// Screen
screens := app.Screen.GetAll()
primary := app.Screen.GetPrimary()

// Browser
app.Browser.OpenURL("https://wails.io")

// Environment info (struct with debug/build/platform info)
env := app.Env()

// Dock / taskbar
app.Dock().Show()           // macOS dock / Windows taskbar / Linux
app.Dock().Hide()
```

---

## Raw Message Handling

For special cases requiring direct frontend→backend communication bypassing the binding system. Only use if bindings are a confirmed performance bottleneck.

```go
app := application.New(application.Options{
    Name: "My App",
    RawMessageHandler: func(window application.Window, message string) {
        fmt.Printf("Raw msg from %s: %s\n", window.Name(), message)
        // Respond via event
        window.EmitEvent("raw-response", processMessage(message))
    },
})
```

**Frontend (JS):**
```javascript
import { System } from '@wailsio/runtime'
System.invoke(JSON.stringify({ action: "ping" }))

Events.On('raw-response', (e) => console.log(e.data))
```

---

## Platform-Specific Options

### macOS

```go
Mac: application.MacOptions{
    ActivationPolicy: application.ActivationPolicyRegular,
    ApplicationShouldTerminateAfterLastWindowClosed: true,
}
```

### Windows

```go
Windows: application.WindowsOptions{
    WndClass:                      "MyAppClass",
    WebviewUserDataPath:           "",   // default: %APPDATA%\[BinaryName.exe]
    WebviewBrowserPath:            "",   // default: system WebView2
    DisableQuitOnLastWindowClosed: false,
    EnabledFeatures:               []string{"msWebView2EnableDraggableRegions"},
    AdditionalBrowserArgs:         []string{"--remote-debugging-port=9222"},
}
```

### Linux

```go
Linux: application.LinuxOptions{
    ProgramName:                   "my-app",
    DisableQuitOnLastWindowClosed: false,
}
```

---

## Complete Example

```go
package main

import (
    "fmt"
    "log/slog"

    "github.com/wailsapp/wails/v3/pkg/application"
)

type GreetService struct {
    app *application.Application
}

func (g *GreetService) Greet(name string) string {
    return "Hello, " + name + "!"
}

func (g *GreetService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
    slog.Info("GreetService starting up")
    return nil
}

func (g *GreetService) ServiceShutdown() error {
    slog.Info("GreetService shutting down")
    return nil
}

func main() {
    app := application.New(application.Options{
        Name:        "My Application",
        Description: "A demo Wails app",
        Mac: application.MacOptions{
            ApplicationShouldTerminateAfterLastWindowClosed: true,
        },
    })

    greetSvc := &GreetService{app: app}
    app.RegisterService(application.NewService(greetSvc))

    // Subscribe to shutdown for cleanup
    app.OnShutdown(func() {
        fmt.Println("Goodbye!")
    })

    // Create main window
    window := app.Window.NewWithOptions(application.WebviewWindowOptions{
        Title:            "My App",
        Width:            1024,
        Height:           768,
        BackgroundColour: application.NewRGB(255, 255, 255),
    })
    window.Center()
    window.Show()

    if err := app.Run(); err != nil {
        slog.Error("App error", "error", err)
    }
}
```
