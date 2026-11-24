# Wails v3 — Services & Bindings Reference

## Table of Contents
1. [Service Architecture](#service-architecture)
2. [Service Lifecycle](#service-lifecycle)
3. [Generating Bindings](#generating-bindings)
4. [Methods](#methods)
5. [Models (Data Types)](#models-data-types)
6. [Enums](#enums)
7. [Advanced: Directives](#advanced-directives)
8. [Error Handling](#error-handling)
9. [Best Practices](#best-practices)

---

## Service Architecture

A **service** is a plain Go struct whose exported methods are automatically callable from JavaScript. Services are:

- Lifecycle-aware (`ServiceStartup` / `ServiceShutdown`)
- Singletons (one instance per application)
- Shared across all windows — use mutexes for mutable state
- Independently testable with no Wails dependency

### Registering Services

```go
app := application.New(application.Options{
    Services: []application.Service{
        application.NewService(&GreetService{}),
    },
})
```

**Custom service name** (useful with multiple instances of the same type):
```go
application.NewServiceWithOptions(&MyService{}, application.ServiceOptions{
    Name: "CustomName",
})
```

**HTTP route on a service** (implements `http.Handler`):
```go
application.NewServiceWithOptions(&FileService{root: "./files"}, application.ServiceOptions{
    Route: "/files",   // → http://wails.localhost/files/...
})
```

**Register after app creation** (e.g. service needs the app reference):
```go
app.RegisterService(application.NewService(NewNotificationService(app)))
```

**Service-to-service dependencies** — wire up before creating the app:
```go
userService := &UserService{}
orderService := NewOrderService(userService)
app := application.New(application.Options{
    Services: []application.Service{
        application.NewService(userService),
        application.NewService(orderService),
    },
})
```

### State Management

All windows share service instances — always protect mutable state:

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

## Service Lifecycle

### `ServiceStartup(ctx, opts) error`

Called when the app starts; services start in **registration order**.

```go
func (u *UserService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
    db, err := sql.Open("sqlite3", "app.db")
    if err != nil { return fmt.Errorf("open db: %w", err) }  // non-nil error ABORTS app startup
    u.db = db

    ctx, u.cancel = context.WithCancel(ctx)   // use ctx for goroutine cancellation
    go u.backgroundSync(ctx)
    return nil
}
```

### `ServiceShutdown() error`

Called at shutdown, in **reverse registration order**. Returning an error logs a warning but does not prevent shutdown; app context is already cancelled.

```go
func (u *UserService) ServiceShutdown() error {
    if u.cancel != nil { u.cancel() }
    if u.db != nil { return u.db.Close() }
    return nil
}
```

---

## Generating Bindings

```bash
wails3 generate bindings              # JavaScript (default output: frontend/bindings/)
wails3 generate bindings -ts          # TypeScript
wails3 generate bindings -ts -time-type=Date   # time.Time → Date (default: RFC3339Nano string)
wails3 generate bindings -d ./src/api # custom output
wails3 generate bindings -clean       # clean output dir first
```

Also useful: `-i` (interfaces instead of classes), `-names` (method names instead of IDs), `-noindex`, `-models <name>`, `-index <name>`, `-dry`, `-tags <build tags>`. Bindings regenerate automatically during `wails3 dev`.

**Output structure:**
```
frontend/bindings/
└── myapp/
    ├── greetservice.$(EXT)    ← one file per service
    ├── models.$(EXT)          ← shared models (structs referenced by multiple services)
    ├── events.$(EXT)          ← typed events (if registered)
    ├── enums.$(EXT)           ← enum values (if defined)
    └── index.$(EXT)           ← re-exports all services
```

---

## Methods

The bindings generator converts **public methods** of registered services into callable JS functions. When Go signatures change, re-run the generator — bindings and docs are updated together.

### Method visibility rules

- Method must be **exported** (uppercase name)
- Parameters and return values must be **serializable** (see type mapping)
- A return value of `error` alone → returns `Promise<void>`; `(value, error)` → `Promise<T>`
- `error` results are thrown as JS exceptions (see [Error Handling](#error-handling))
- Prefer **objects over many parameters**: pass/return structs, they're serializable
- **Always validate input** on the Go side — never trust JS input

### Call types

Every generated method calls the bridge under the hood:

```javascript
import { Call } from '@wailsio/runtime'
// generated code ≈
export function Greet(name) {
    return Call.ByID('main.GreetService.Greet', name)        // by method ID (default)
    // or Call.ByName('main.GreetService.Greet', name)        // with -names flag
}
```

`Call.ByName` costs an extra lookup — generated code defaults to `ByID` for performance.

### Async is required

All calls return `Promise` — always `await`:

```typescript
import { Greet } from './bindings/changeme/greetservice'

const message: string = await Greet("World")
```

---

## Models (Data Types)

The generator automatically extracts every **struct referenced by bound methods** (parameters or return values) into `models.$(EXT)` in the bindings directory. Structs used by only one service stay in that service's file.

### Structs → Classes/Interfaces

```go
type Person struct {
    Name    string    `json:"name"`
    Age     int       `json:"age"`
    Email   string    `json:"email"`
}
```

**Default: classes** — generated with constructor taking a partial object:
```typescript
import { Person } from './bindings/myapp/models'

const person = new Person({ name: "Alice", age: 30, email: "a@b.c" })
```

**`-i` flag: interfaces** — plain object shapes, no constructor:
```typescript
const person: Person = { name: "Alice", age: 30, email: "a@b.c" }
```

### Field naming

JSON tags control field names in JS. Without tags, Go field names are used as-is (PascalCase):
```go
type Config struct {
    ServerPort int    `json:"serverPort"`   // → config.serverPort
    Timeout    int                            // → config.Timeout
}
```

### Struct embedding

Embedded structs are flattened into the parent model:

```go
type Base struct {
    ID   string
}
type User struct {
    Base           // → user.id (flattened)
    Name string
}
```

### time.Time

- Default: `time.Time` ↔ `string` (RFC3339Nano)
- `-time-type=Date` flag: generates conversion helpers that produce JS `Date` objects

### Nested & collection types

| Go | TypeScript |
|---|---|
| `[]Person` | `Person[]` |
| `map[string]Person` | `{ [_: string]: Person }` (plain object, **not** JS `Map`) |
| `*Person` | `Person \| null` |
| `[]byte` | `string` (base64) |
| `time.Time` | `string` / `Date` (per flags) |

### Circular references

Structs referencing each other are extracted to `models.$(EXT)` to avoid duplicated type definitions — shared between all services that use them.

### Unsupported field types

`chan`, `func`, unexported fields, complex interfaces — not serializable, excluded from models. For stateful resources, return an ID/handle instead:

```go
var openFiles = make(map[string]*os.File)

func (s *FileService) OpenFile(path string) (string, error) { /* return id */ }
func (s *FileService) ReadFile(id string) ([]byte, error)   { /* use id */ }
```

---

## Enums

The generator converts **exported constants of bindable types** (int-based and string-based) into enum objects, emitted to `enums.$(EXT)`.

### Integer-based enums

```go
type Status int

const (
    StatusActive    Status = iota  // 0
    StatusInactive                 // 1
    StatusPending                  // 2
)
```

```typescript
// enums.ts (generated)
export enum Status {
    Active = 0,     // name: trailing "Status" prefix stripped
    Inactive = 1,
    Pending = 2,
}
```

Usage:
```typescript
import { Status } from './bindings/myapp/enums'

function check(s: Status) {
    if (s === Status.Active) { /* ... */ }
}
```

### String-based enums

```go
type Color string

const (
    ColorRed   Color = "red"
    ColorGreen Color = "green"
)
```
```typescript
export enum Color {
    Red = "red",
    Green = "green",
}
```

### Naming rule

Constant names sharing the enum type's name as a prefix get the prefix stripped (`StatusActive` → `Active`). Constants without the prefix keep their full name.

### Typed enum parameters

Enums work seamlessly as bound-method params/returns — pass `Status.Active` and Go receives `Status(0)`.

---

## Advanced: Directives

Source-code comments that control what the bindings generator emits.

### `//wails:event`

Registers a **typed event** so the generator emits strongly-typed event helpers to `events.$(EXT)`:

```go
//wails:event UserLogin(user UserData)
```
```typescript
// generated events.ts
import { Events } from '@wailsio/runtime'
import { UserData } from './models'

export const UserLogin = Events.NewEventType<UserData>('UserLogin')
```

Usage:
```typescript
import { UserLogin } from './bindings/myapp/events'

UserLogin.On((event) => console.log(event.data.name))          // fully typed
const cancelled = await UserLogin.Emit({ name: 'alice' })      // true if a hook cancelled it
```

### `//wails:service`

Explicitly registers a service (alternative to `application.NewService`):

```go
//wails:service CalculatorService
```

### `//wails:internal`

Excludes the method/struct from the **generated SDK**.

> ⚠️ **This is a generator-only directive** — it does NOT restrict runtime access or act as a security boundary. For real access control, validate inside your methods.

### `//wails:ignore`

Same scope as `//wails:internal` — excludes an item from generated bindings.

> ⚠️ Same caveat: affects the generated SDK only, not runtime behavior.

### `//wails:inline`

Marks a model type to be **inlined into each referencing service file** instead of being extracted to `models.$(EXT)`. Use for types used by only one service, to keep generated files smaller.

```go
//wails:inline
type Config struct {
    Host string
    Port int
}
```

---

## Error Handling

Go errors propagate automatically as JavaScript exceptions. Use `Call`'s error classes from `@wailsio/runtime` to distinguish failure causes:

```typescript
import { Call } from '@wailsio/runtime'

try {
    const user = await GetUser(-1)
} catch (err) {
    if (err instanceof Call.RuntimeError) {
        // Go method executed and returned an error → handle business error
        console.error("Go error:", err.message)
    } else {
        // call failed at the bridge layer (service missing, marshalling, etc.)
        console.error("Call failed:", err)
    }
}
```

**Structured errors** — throwing a Go struct error gives the JS side a parsed `.cause`:

```go
type ValidationError struct {
    Field  string `json:"field"`
    Reason string `json:"reason"`
}

func (e *ValidationError) Error() string {
    return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func (s *UserService) CreateUser(u User) error {
    if u.Name == "" {
        return &ValidationError{Field: "name", Reason: "required"}
    }
    return nil
}
```

```typescript
try {
    await CreateUser({})
} catch (err) {
    if (err instanceof Call.RuntimeError && err.cause) {
        const v = err.cause as ValidationError
        console.log(`Invalid field: ${v.field} (${v.reason})`)
    }
}
```

Notes:
- Returning plain `error` → JS gets `Error` with the error message
- Returning custom error struct → `err.cause` contains the JSON-decoded struct
- Always handle rejections — an unawaited failed call fails silently

---

## Best Practices

### ✅ Do
- Single responsibility — one purpose per service
- Constructor injection for dependencies
- Mutexes for shared state (services are singletons shared by all windows)
- Return `error` instead of panicking; return structured error types for programmatic handling
- Use `ServiceStartup`/`ServiceShutdown` for resource lifecycle
- Batch operations into one call with slices/objects
- Validate all inputs on the Go side
- Prefer `Call.RuntimeError` checks over generic catch-all error handling

### ❌ Don't
- Don't block `ServiceStartup` — start goroutines for long-running work
- Don't expose channels/functions/complex interfaces through bindings
- Don't rely on `//wails:internal`/`//wails:ignore` for security — generator-only
- Don't create circular service dependencies
- Don't use `Call.ByName` in hot paths (`ByID` is faster)

### Performance Tips

```javascript
// ❌ Slow: N round trips
for (const item of items) { await ProcessItem(item) }
// ✅ Fast: 1 round trip
await ProcessItems(items)
```

Use events for long-running operations instead of blocking calls:
```go
func (s *MyService) ProcessLargeFile(path string) {
    go func() {
        for progress := range process(path) {
            s.app.Event.Emit("progress", progress)
        }
        s.app.Event.Emit("process-done", nil)
    }()
}
```
