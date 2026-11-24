# Wails v3 — Platform Features Reference

## Table of Contents
1. [Auto Start](#auto-start)
2. [Clipboard](#clipboard)
3. [Browser Integration](#browser-integration)
4. [Drag and Drop (Files)](#drag-and-drop-files)
5. [Notifications](#notifications)
6. [Screens](#screens)
7. [Environment Info](#environment-info)
8. [Dock & Taskbar](#dock--taskbar)
9. [Keyboard Shortcuts (In-Window)](#keyboard-shortcuts-in-window)
10. [Global Shortcuts](#global-shortcuts)

---

## Auto Start

Register the app to launch on OS login.

```go
import "github.com/wailsapp/wails/v3/pkg/application"

// Requires services: pass application context at startup
func (s *MyService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
    // ...
    return nil
}

// Via the app instance (auto-start service)
app.Autostart().Enable()    // register for login
app.Autostart().Disable()   // unregister
```

Notes:
- Windows: registry `Run` key; macOS: LaunchAgent / `SMAppService`; Linux: `.desktop` file in autostart dir
- Check current state before toggling to avoid redundant writes
- Registering requires appropriate platform entitlements on macOS (sandboxed apps)

---

## Clipboard

```go
// Go side
app.Clipboard.SetText("Copied!")
text, err := app.Clipboard.Text()
```

```javascript
// JS side
import { Clipboard } from '@wailsio/runtime'
await Clipboard.SetText('Hello from Wails!')
const text = await Clipboard.Text()
```

---

## Browser Integration

Open URLs in the user's default browser (never embed an external browser view for this):

```go
// Go
app.Browser.OpenURL("https://wails.io")
```

```javascript
// JS
import { Browser } from '@wailsio/runtime'
await Browser.OpenURL('https://wails.io')
await Browser.OpenURL(new URL('https://wails.io'))
```

Or declaratively via WML:
```html
<a href="#" wml-openurl="https://wails.io">Visit Wails</a>
```

---

## Drag and Drop (Files)

1. Enable on the window:
```go
window := app.Window.NewWithOptions(application.WebviewWindowOptions{
    EnableDragAndDrop: true,
    // ...
})
```

2. Mark drop zones in CSS (`--wails-drop-target`):
```css
.drop-zone {
    --wails-drop-target: drop;          /* accepts file drops */
}
```

3. Listen for the DOM drop event and read `event.dataTransfer.files`:

```javascript
const dropZone = document.querySelector('.drop-zone')
dropZone.addEventListener('drop', (event) => {
    event.preventDefault()
    for (const file of event.dataTransfer.files) {
        console.log('Dropped:', file.name, file.path)   // file.path has the absolute path
    }
})
dropZone.addEventListener('dragover', (event) => event.preventDefault())
```

Use `file.path` (absolute path) to hand the file to Go bindings for processing.

Platform notes:
- **Windows**: fully supported via WebView2
- **macOS**: supported; drop zones must cover the desired area
- **Linux**: requires WebKitGTK support

---

## Notifications

Native desktop notifications.

```go
// Go side
notification := app.Notification.Notification()
notification.Name = "My App"
notification.Title = "Task Complete"
notification.Body = "Your export has finished."
notification.Send()
```

```javascript
// JS side
import { Notification } from '@wailsio/runtime'
await Notification.Send({
    name: 'My App',
    title: 'Task Complete',
    body: 'Your export has finished.',
})
```

Platform notes:
- **Windows**: uses AppUserModelID — the app must be installed/registered for notifications to render reliably; toast notifications via Windows notification API
- **macOS**: requires code-signed app; user notification permission prompt on first send
- **Linux**: uses `libnotify`/`dbus`

Click handlers / actions are platform-dependent; keep to name/title/body for portable behavior.

---

## Screens

```go
// Go
screens := app.Screen.GetAll()          // []Screen
primary := app.Screen.GetPrimary()      // *Screen
current := window.GetScreen()           // screen containing this window
```

```javascript
// JS
import { Screens } from '@wailsio/runtime'
const all = await Screens.GetAll()
const primary = await Screens.GetPrimary()
const current = await Screens.GetCurrent()
```

**Screen struct (JS naming):**
```typescript
interface Screen {
    ID: string
    Name: string
    ScaleFactor: number
    X: number; Y: number
    Size: { Width: number, Height: number }
    Bounds: { X: number, Y: number, Width: number, Height: number }
    WorkArea: { X: number, Y: number, Width: number, Height: number }
    IsPrimary: boolean
    Rotation: number
}
```

Typical use: position popups inside the work area of the screen the current window is on.

---

## Environment Info

Build & runtime environment details:

```go
// Go
env := app.Env()   // struct with platform/debug/version info
```

```javascript
// JS
import { System } from '@wailsio/runtime'
// environment info available via System module / raw bridge
```

Use for feature gating by platform, debug builds, version reporting.

---

## Dock & Taskbar

```go
// macOS Dock / Windows taskbar / Linux equivalent
app.Dock().Show()
app.Dock().Hide()      // e.g. tray-only app mode
```

macOS specifics:
```go
// Activation policy
application.SetActivationPolicy(application.ActivationPolicyAccessory)   // no dock icon
application.SetActivationPolicy(application.ActivationPolicyRegular)     // normal app
```

---

## Keyboard Shortcuts (In-Window)

Shortcuts that work while the app window has focus.

### Via menu accelerators (recommended)

```go
menu := app.NewMenu()
fileMenu := menu.AddMenu("File")
fileMenu.Add("Save").
    SetAccelerator("CmdOrCtrl+S").        // platform-aware: Cmd on Mac, Ctrl elsewhere
    OnClick(func(ctx *application.Context) { save() })
app.SetMenu(menu)
```

### Accelerator key format

| Modifier | Examples |
|---|---|
| Platform-agnostic primary | `CmdOrCtrl` (Cmd on macOS, Ctrl on Win/Linux) |
| Explicit | `Cmd`, `Ctrl`, `Alt`, `Shift`, `Super` |
| Keys | `A`–`Z`, `0`–`9`, `F1`–`F24`, `Up`, `Down`, `Left`, `Right`, `Enter`, `Escape`, `Tab`, `Backspace`, `Delete`, `Space` |

Examples: `CmdOrCtrl+Shift+P`, `Alt+F4`, `Ctrl+,`

### Via JS (DOM-level)

```javascript
document.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault()
        // call a binding
    }
})
```

---

## Global Shortcuts

Work system-wide, even when the app is unfocused.

```go
// Go side
err := app.GlobalShortcut.SetShortcut("Ctrl+Shift+X", func() {
    fmt.Println("Global shortcut triggered")
})
if err != nil {
    // invalid accelerator or already registered by another app
}

// List / remove
app.GlobalShortcut.Shortcuts()                 // registered accelerators
app.GlobalShortcut.RemoveShortcut("Ctrl+Shift+X")
app.GlobalShortcut.RemoveAllShortcuts()
```

Platform notes:
- **Windows**: uses `RegisterHotKey` — conflicts with other apps' registered combos fail with an error
- **macOS**: requires accessibility/input monitoring permission; some combos are system-reserved
- **Linux**: uses X11 global shortcuts — Wayland has no standard global shortcut API (may not work)
- Prefer `CmdOrCtrl` style strings for portability; always handle registration errors
