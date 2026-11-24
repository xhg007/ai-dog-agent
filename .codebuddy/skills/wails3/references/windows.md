# Wails v3 — Windows Reference

## Table of Contents
1. [Window Basics](#window-basics)
2. [Window Options](#window-options)
3. [Window Events (Hooks vs Listeners)](#window-events)
4. [Frameless Windows](#frameless-windows)
5. [Multiple Windows](#multiple-windows)
6. [Permissions](#permissions)

---

## Window Basics

### Creating Windows

```go
// Simple
window := app.Window.New()

// With options
window := app.Window.NewWithOptions(application.WebviewWindowOptions{
    Title:  "Main Window",
    Width:  1024,
    Height: 768,
    URL:    "http://wails.localhost/",
})
window.Show()
```

### Common Methods

```go
window.Show()
window.Hide()
window.Close()              // there is NO Destroy() method
window.Center()             // note: Center, not Centre
window.SetTitle("New Title")
window.SetSize(1280, 720)
window.SetPosition(100, 200)
window.Minimise() / window.Maximise() / window.Restore()
window.Fullscreen() / window.UnFullscreen() / window.ToggleFullscreen()   // no SetFullscreen(bool)
window.SetAlwaysOnTop(true)
window.SetResizable(true)
window.Focus()
window.EmitEvent("my-event", data)   // emit event to this window's JS only
```

### Naming windows

Set `Name` in options to look up later:
```go
win := app.Window.GetByName("settings")
if win != nil { win.Show() }

for _, w := range app.Window.GetAll() {
    fmt.Println(w.Name())
}
```

---

## Window Options

`application.WebviewWindowOptions` — full field reference:

| Field | Type | Default | Description |
|---|---|---|---|
| `Name` | `string` | `""` | Unique name; used with `GetByName` / `wml-target-window` |
| `Title` | `string` | `""` | Window title |
| `Width` / `Height` | `int` | 1024 / 768 | Initial size |
| `MinWidth` / `MinHeight` | `int` | 0 | Minimum size (0 = OS default) |
| `MaxWidth` / `MaxHeight` | `int` | 0 | Maximum size |
| `Frameless` | `bool` | `false` | Remove native title bar/frame |
| `DisableResize` | `bool` | `false` | Prevent user resizing (**no `Resizable` field** — it's inverted) |
| `AlwaysOnTop` | `bool` | `false` | Keep above other windows |
| `StartState` | `WindowState` | `Normal` | `Normal`, `Minimised`, `Maximised`, `Fullscreen` |
| `InitialPosition` | `WindowPosition` | `Center` | `Center`, `TopLeft`, `BottomRight` |
| `X` / `Y` | `int` | 0 | Explicit position |
| `URL` | `string` | `http://wails.localhost/` | URL to load |
| `HTML` | `string` | `""` | Inline HTML to load |
| `BackgroundColour` | `RGBA` | white | Background color |
| `Mac` | `Mac.Window` | | macOS-specific options (see below) |
| `Windows` | `Windows.Window` | | Windows-specific options (see below) |
| `Linux` | `Linux.Window` | | Linux-specific options |
| `HideOnClose` | `bool` | `false` | Hide instead of close |
| `EnableDragAndDrop` | `bool` | `false` | Enable file drag-and-drop into the window |

**macOS-specific (`application.Mac.Window`):**
```go
Mac: application.Mac.Window{
    TitleBar: application.MacTitleBarHidden,   // or MacTitleBarDefault,
                                               // MacTitleBarHiddenInset, MacTitleBarHiddenInsetUnified
    Appearance: application.MacAppearanceNSAppearanceNameDarkAqua,
    HideTitle: false,
    HideTitleBar: false,
    TransparentWindowAppearance: false,
    WindowType: application.MacWindowTypeTitled,
    DisableShadow: false,
}
```

**Windows-specific (`application.Windows.Window`):**
```go
Windows: application.Windows.Window{
    DisableTheme:  false,
    Theme:         windows.SystemDefault,       // Dark, Light, SystemDefault
    ThemeChangedCallback: nil,
    DisableIcon:   false,
    RoundedCorners: true,
}
```

**Linux-specific (`application.Linux.Window`):**
```go
Linux: application.Linux.Window{
    WindowType: gtk.GTK_WINDOW_TOPLEVEL,
    GtkStartupId: "",
    GtkApplicationId: "",
}
```

---

## Window Events

Two mechanisms — know the difference:

### Listeners (passive)

`window.OnWindowEvent(event, fn)` — runs after hooks; **cannot cancel**.

```go
import "github.com/wailsapp/wails/v3/pkg/events"

unsub := window.OnWindowEvent(events.Common.WindowDidResize, func(e *application.WindowEvent) {
    w, h := window.Size()   // event carries NO coordinates — query the window
    fmt.Println("Resized to", w, "x", h)
})
// unsub() to stop listening
```

### Hooks (can cancel)

`window.RegisterHook(event, fn)` — runs **before** listeners; can call `e.Cancel()` to prevent default behavior. This is the **only** way to intercept `WindowClosing`.

```go
window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
    e.Cancel()   // keep the window open (e.g. hide-to-tray pattern)
})
```

### Common events (`events.Common.*`)

| Event | Hook-cancellable | Notes |
|---|---|---|
| `WindowClosing` | ✅ | block to implement confirm / hide-to-tray |
| `WindowFocus` / `WindowBlur` | ❌ | |
| `WindowDidResize` | ❌ | no coordinates; query `window.Size()` |
| `WindowDidMove` | ❌ | query `window.Position()` |
| `WindowMinimise` / `WindowDeminimise` | ❌ | |
| `WindowMaximise` / `WindowUnMaximise` | ❌ | |
| `WindowFullscreen` / `WindowUnFullscreen` | ❌ | |
| `WindowZoomIn` / `WindowZoomOut` / `WindowZoomReset` | ❌ | |
| `WindowRuntimeReady` | ❌ | safe to emit events to the frontend after this |

### Waiting for runtime ready

Before emitting events to a window's frontend, wait for the runtime:

```go
app.Event.OnApplicationEvent(events.Common.WindowRuntimeReady, func(e *application.ApplicationEvent) {
    window.EmitEvent("initial-data", loadData())
})
```

---

## Frameless Windows

`Frameless: true` removes the native frame — you provide drag regions and controls via CSS.

### Drag regions

The runtime automatically handles elements with these CSS properties:

```css
.titlebar {
    --wails-draggable: drag;      /* this area drags the window */
}

.titlebar button {
    --wails-draggable: no-drag;   /* children excluded from dragging */
}
```

Requires the side-effect runtime import (`import '@wailsio/runtime'`).

### Custom window controls (WML)

```html
<div class="titlebar" style="--wails-draggable: drag">
    <span>My App</span>
    <button wml-window="Minimise">—</button>
    <button wml-window="ToggleMaximise">▢</button>
    <button wml-window="Close" wml-confirm="Close window?">✕</button>
</div>
```

### macOS titlebar styles

```go
Mac: application.Mac.Window{
    TitleBar: application.MacTitleBarHiddenInset,   // traffic lights overlay content
}
```
Presets: `MacTitleBarDefault`, `MacTitleBarHidden`, `MacTitleBarHiddenInset`, `MacTitleBarHiddenInsetUnified`.

---

## Multiple Windows

Each window loads its own URL/HTML; all share the same services.

```go
mainWin := app.Window.NewWithOptions(application.WebviewWindowOptions{
    Name:  "main",
    Title: "Main",
    Width: 1024,
})

settingsWin := app.Window.NewWithOptions(application.WebviewWindowOptions{
    Name:   "settings",
    Title:  "Settings",
    Width:  600,
    Height: 400,
    Hidden: true,                 // create hidden; show on demand
})
```

### Cross-window control (Go)

```go
settings := app.Window.GetByName("settings")
settings.Show()
settings.Hide()
```

### Cross-window events

```go
mainWin.EmitEvent("refresh", nil)     // only mainWin's frontend
app.Event.Emit("global-refresh")      // all windows
```

### Modals

```go
child := app.Window.NewWithOptions(application.WebviewWindowOptions{
    Title: "Dialog",
    Width: 400, Height: 300,
    HideOnClose: true,
})
mainWin.AttachModal(child)   // there is NO Parent option field
```

### Opening a window from another window's JS

```javascript
// runtime.md → Window.Get(name)
import { Window } from '@wailsio/runtime'
const settings = Window.Get('settings')
await settings.Show()
```

Or declaratively via WML:
```html
<button wml-window="Show" wml-target-window="settings">Show Settings</button>
```

---

## Permissions

Permissions control **which window methods the JS runtime may call** (a security allowlist — not the same as the generator-only `//wails:internal` directive).

Permissions are declared per-window in `WebviewWindowOptions.Permissions` using `application.Permission*` constants, with `Allow` / `Deny` lists:

```go
window := app.Window.NewWithOptions(application.WebviewWindowOptions{
    Name: "main",
    Permissions: application.WindowPermissions{
        Allow: []application.WindowPermission{
            // allowlisted window methods
        },
        Deny: []application.WindowPermission{
            // explicitly blocked methods
        },
    },
})
```

Concepts:
- **Allow**: if set, only listed methods are callable from JS
- **Deny**: explicit blocklist, takes precedence
- Unlisted methods follow the default policy

> ⚠️ Do not confuse window **permissions** (runtime access control) with `//wails:internal` / `//wails:ignore` (which only affect the generated SDK).
