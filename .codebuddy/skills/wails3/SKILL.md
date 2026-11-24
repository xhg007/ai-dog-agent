---
name: wails3
description: Build cross-platform desktop apps with Go + Web technologies using Wails v3. Use this skill whenever the user is working with Wails v3 — creating a new project, running or building the app, generating TypeScript/JavaScript bindings from Go code, registering services, using models/enums bindings, the event system (hooks vs listeners), windows (frameless, multiple, permissions), dialogs, frontend runtime API (@wailsio/runtime, WML), menus/tray, autostart, clipboard, notifications, screens, environment, global shortcuts, or the build system (Taskfile). Trigger on any mentions of wails3, wails v3, go desktop app, bindings generation, @wailsio/runtime, wml, ServiceStartup, or service registration.
---

# Wails v3 Skill

Wails v3 lets you build native desktop apps using Go for the backend and any web framework for the frontend. The Go–JavaScript bridge handles all IPC automatically — no HTTP, no boilerplate.

## Quick Reference

| Topic | Reference File |
|---|---|
| CLI commands, build system, Taskfile | `references/cli.md` |
| Services, bindings, models, enums, directives, error handling | `references/bindings.md` |
| Frontend Runtime API (`@wailsio/runtime`, WML, typed events) | `references/runtime.md` |
| Application API (Go side): app, events, menus/tray, screens, env | `references/application.md` |
| Windows: options, events, frameless, multiple, permissions | `references/windows.md` |
| Dialogs: message / file / custom (Go + JS) | `references/dialogs.md` |
| Platform features: autostart, clipboard, browser, drag-drop, notifications, dock, keyboard shortcuts | `references/features.md` |

Read only the reference file(s) relevant to the user's question.

---

## Installation

```bash
# Requires Go 1.24+, npm (optional), platform deps (see below)
go install -v github.com/wailsapp/wails/v3/cmd/wails3@latest
wails3 doctor   # verifies all dependencies
```

**Platform dependencies:**
- **macOS**: `xcode-select --install`
- **Windows**: WebView2 Runtime (usually pre-installed)
- **Linux**: `gcc`, `gtk3`, `webkit2gtk` — run `wails3 doctor` for distro-specific instructions

---

## Project Lifecycle

### 1. Create project
```bash
wails3 init -n myapp                              # vanilla template
wails3 init -n myapp -t svelte                    # specific template
wails3 init -n myapp -git github.com/user/myapp   # with git setup
```

### 2. Develop (live reload)
```bash
wails3 dev   # hot-reloads frontend; rebuilds Go on save; regenerates bindings
```

### 3. Build & Run
```bash
wails3 build       # build → ./bin/<app> (embedded frontend, stripped symbols)
./bin/myapp        # run the binary
```

### 4. Package for distribution
```bash
wails3 package     # .app (Mac), NSIS installer (Win), AppImage/deb/rpm (Linux)
```

---

## Core Concepts

### Services (Go backend)

Any exported Go struct registered as a service gets its exported methods automatically bound to JavaScript.

```go
type GreetService struct{}

func (g *GreetService) Greet(name string) string {
    return "Hello, " + name + "!"
}

// main.go
app := application.New(application.Options{
    Services: []application.Service{
        application.NewService(&GreetService{}),
    },
})
```

Rules:
- Only **exported methods** (PascalCase) are bound
- Methods may return `value`, `error`, or `(value, error)`
- Services are **singletons** — use mutexes for shared state (all windows share them)
- Implement `ServiceStartup(ctx, opts) error` / `ServiceShutdown() error` for lifecycle hooks

### Generating Bindings

```bash
wails3 generate bindings          # JavaScript (default output: frontend/bindings/)
wails3 generate bindings -ts      # TypeScript
wails3 generate bindings -d ./src/api   # custom output directory
wails3 generate bindings -ts -time-type=Date  # convert time.Time to JS Date (default keeps RFC3339Nano string)
```

Bindings regenerate automatically during `wails3 dev`.

```javascript
import { Greet } from './bindings/changeme/greetservice'
const message = await Greet("World")   // Promise — always await
```

Go errors become JS exceptions. `Call.RuntimeError` (from `@wailsio/runtime`) means the Go method returned an error; other error types mean the call itself failed. Structured errors: thrown error's `.cause` holds the JSON of Go struct errors. Wrap calls in `try/catch`.

### Events (Go ↔ JavaScript)

```go
app.Event.Emit("data-updated", map[string]any{"count": 42})
```
```javascript
import { Events } from '@wailsio/runtime'
const unsub = Events.On('data-updated', (e) => console.log(e.data))
// cleanup: unsub()
```

**Critical distinction:**
- `window.OnWindowEvent(event, fn)` — passive listener, **cannot cancel** events
- `window.RegisterHook(event, fn)` — runs before listeners, **can call `e.Cancel()`** (only way to block e.g. `WindowClosing`)

Wait for `events.Common.WindowRuntimeReady` before emitting events to a window's frontend.

---

## Frequently-Wrong Facts (check before coding)

| Wrong assumption | Reality |
|---|---|
| `window.Destroy()` | Does not exist — use `window.Close()` |
| `window.SetFullscreen(bool)` | Does not exist — use `Fullscreen()` / `UnFullscreen()` / `ToggleFullscreen()` |
| `WebviewWindowOptions{Parent: ...}` / `Resizable` field | No `Parent` field — use `parent.AttachModal(child)`; field is `DisableResize` (inverted) |
| `window.Centre()` | Method is `Center()` |
| `map[K]V` binds to JS `Map` | Binds to plain object / `Record<K,V>` |
| `time.Time` always becomes `Date` | Default is `string` (RFC3339Nano); use `-time-type=Date` to get Date conversion helpers |
| `application.EventApplicationShutdown` | No such constant — shutdown cleanup uses `app.OnShutdown(func())` |
| `e.Cancel()` inside `OnWindowEvent` | No effect — only `RegisterHook` callbacks can cancel |
| `OnWindowEvent(WindowDidResize)` carries coords | Event carries no coordinates — query `window.Size()` / `window.Position()` |
| `//wails:internal` / `//wails:ignore` are security | They only affect the generated SDK, NOT runtime access control |
| `wails3 build -ldflags ...` | Only `--tags` is forwarded; everything else is configured in Taskfile / `build/config.yml` |
| `build/build.json` | Doesn't exist in v3 — project metadata lives in `build/config.yml` |
| macOS `TitleBarStyle` / `MacTitleBarStyleHidden` | Use exported presets: `application.MacTitleBarHidden`, `MacTitleBarDefault`, `MacTitleBarHiddenInset`, `MacTitleBarHiddenInsetUnified` |

---

## Type Mapping (Go → JS/TS)

| Go | JavaScript/TypeScript |
|---|---|
| `string`, `bool`, `int/uint*`, `float*`, `byte`, `rune` | `string` / `boolean` / `number` |
| `[]T`, `[N]T` | `T[]` |
| `map[string]T` / `map[K]V` | `{ [_: string]: T }` / `{ [_ in K]?: V }` (not JS `Map`) |
| `[]byte` | `string` (base64) |
| `struct` | `class` or `interface` (`-i` flag) |
| `time.Time` | `string` (default) or `Date` (`-time-type=Date`) |
| `*T` | `T \| null` |
| `any` / `interface{}` | `any` |
| `chan`, `func`, complex interfaces | ❌ Not supported (use events / IDs) |

---

## Common Patterns

**Batch operations** (reduce bridge overhead):
```javascript
// ❌ Slow: N calls
for (const item of items) { await ProcessItem(item) }
// ✅ Fast: 1 call
await ProcessItems(items)
```

**Long-running tasks** — emit progress events instead of blocking:
```go
func (s *MyService) ProcessFile(path string) {
    go func() {
        for progress := range processLines(path) {
            s.app.Event.Emit("progress", progress)
        }
        s.app.Event.Emit("done", nil)
    }()
}
```

**Thread-safe service state:**
```go
type CounterService struct {
    count int
    mu    sync.Mutex
}
func (c *CounterService) Increment() int {
    c.mu.Lock(); defer c.mu.Unlock()
    c.count++; return c.count
}
```

---

## WML (Wails Markup Language)

Declarative HTML attributes for common actions — no JavaScript needed:

```html
<button wml-event="save-clicked">Save</button>
<button wml-window="Minimise">Minimize</button>
<button wml-window="Close" wml-confirm="Close window?">Close</button>
<button wml-window="Show" wml-target-window="settings">Show Settings</button>
<a href="#" wml-openurl="https://wails.io">Visit Wails</a>
```

---

## Project Structure

```
myapp/
├── main.go              # App entry point, service registration, window setup
├── greetservice.go      # Example service
├── go.mod
├── build/
│   ├── config.yml       # Project metadata (name, identifier, version) — no build.json in v3
│   └── Taskfile.yml     # Platform build tasks (darwin:/windows:/linux: namespaces)
├── frontend/
│   ├── index.html
│   ├── main.js
│   ├── package.json
│   └── bindings/        # Auto-generated — do not edit manually
└── Taskfile.yml         # Top-level task runner
```

---

For detailed CLI flags, build system internals, Go Application API, windows, dialogs, or platform features — read the relevant reference file listed in the Quick Reference table above.
