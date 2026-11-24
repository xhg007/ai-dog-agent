# Wails v3 — Dialogs Reference

## Table of Contents
1. [Message Dialogs (Go)](#message-dialogs-go)
2. [File Dialogs (Go)](#file-dialogs-go)
3. [Custom Dialogs (Go)](#custom-dialogs-go)
4. [Dialogs from JS](#dialogs-from-js)
5. [API Summary](#api-summary)

---

## Message Dialogs (Go)

Builder API — chain setters, then `Show()`.

```go
// Info
app.Dialog.Info().
    SetTitle("Done").
    SetMessage("Saved!").
    Show()

// Error
app.Dialog.Error().
    SetTitle("Error").
    SetMessage("Something failed.").
    Show()

// Warning
app.Dialog.Warning().
    SetTitle("Caution").
    SetMessage("Cannot undo this.").
    Show()
```

### Question dialog with buttons

```go
dlg := app.Dialog.Question().
    SetTitle("Confirm").
    SetMessage("Delete this item?")

yes := dlg.AddButton("Yes")
yes.OnClick(func() { deleteItem() })

no := dlg.AddButton("No")
dlg.SetDefaultButton(yes)    // activated on Enter
dlg.SetCancelButton(no)      // activated on Escape
dlg.Show()
```

### All message dialog setters

| Setter | Type | Description |
|---|---|---|
| `SetTitle(s)` | `string` | Dialog window title |
| `SetMessage(s)` | `string` | Body text |
| `SetIcon(icon)` | `[]byte` | Custom icon bytes |
| `AddButton(label)` | `string` | Returns a button for `OnClick` binding |
| `SetDefaultButton(btn)` | button | Enter key |
| `SetCancelButton(btn)` | button | Escape key |
| `Show()` | | Displays the dialog |

---

## File Dialogs (Go)

### Open file

```go
path, err := app.Dialog.OpenFile().
    SetTitle("Select File").
    AddFilter("Images", "*.png;*.jpg").
    PromptForSingleSelection()
if err != nil { /* cancelled or failed */ }
```

### Save file

```go
path, err := app.Dialog.SaveFile().
    SetTitle("Save As").
    SetFilename("document.pdf").
    AddFilter("PDF", "*.pdf").
    PromptForSingleSelection()
```

### Open folder

```go
path, err := app.Dialog.OpenFile().
    SetTitle("Select Folder").
    CanChooseDirectories(true).
    CanChooseFiles(false).
    PromptForSingleSelection()
```

### Multiple selection

```go
paths, err := app.Dialog.OpenFile().
    SetTitle("Select Files").
    AllowsMultipleSelection(true).
    PromptForMultipleSelection()
```

### File dialog setters

| Setter | Type | Description |
|---|---|---|
| `SetTitle(s)` | `string` | Dialog title |
| `SetFilename(s)` / `SetDefaultFilename` | `string` | Pre-filled filename (save dialogs) |
| `AddFilter(name, pattern)` | `string, string` | e.g. `AddFilter("Images", "*.png;*.jpg")` |
| `CanChooseDirectories(b)` | `bool` | Allow selecting folders |
| `CanChooseFiles(b)` | `bool` | Allow selecting files |
| `AllowsMultipleSelection(b)` | `bool` | Multi-select |
| `CanCreateDirectories(b)` | `bool` | Allow creating folders |
| `ShowHiddenFiles(b)` | `bool` | Show hidden files |
| `SetInitialPath(p)` / `SetStartDirectory` | `string` | Starting directory |
| `PromptForSingleSelection()` | → `(string, error)` | Show, return one path |
| `PromptForMultipleSelection()` | → `([]string, error)` | Show, return many paths |

---

## Custom Dialogs (Go)

Compose fully custom dialogs from primitives — message dialog + arbitrary buttons:

```go
dlg := app.Dialog.Custom().
    SetTitle("Export Data").
    SetMessage("Choose an export format:")

csvBtn := dlg.AddButton("Export as CSV")
csvBtn.OnClick(func() { exportCSV() })

jsonBtn := dlg.AddButton("Export as JSON")
jsonBtn.OnClick(func() { exportJSON() })

cancel := dlg.AddButton("Cancel")
dlg.SetCancelButton(cancel)

dlg.Show()
```

Use custom dialogs when the standard Info/Warning/Error/Question presets don't fit — any number of buttons, each with its own handler.

---

## Dialogs from JS

The frontend runtime exposes dialogs without a Go round-trip:

```javascript
import { Dialogs } from '@wailsio/runtime'

// Info / Warning / Error — same options shape
await Dialogs.Info({ Title: 'Success', Message: 'Operation completed!' })

// Question — returns clicked button's label
const result = await Dialogs.Question({
    Title: 'Confirm Delete',
    Message: 'Are you sure?',
    Buttons: [
        { Label: 'Delete', IsDefault: false },
        { Label: 'Cancel', IsDefault: true }
    ]
})

// File open — string or string[] when multi-select
const file = await Dialogs.OpenFile({
    Title: 'Select Image',
    Filters: [{ DisplayName: 'Images', Pattern: '*.png;*.jpg;*.jpeg' }]
})

// File save
const path = await Dialogs.SaveFile({
    Title: 'Save As',
    DefaultFilename: 'document.pdf',
    Filters: [{ DisplayName: 'PDF', Pattern: '*.pdf' }]
})
```

Always check falsy results — user cancellation resolves without a path.

---

## API Summary

| Where | API | Returns |
|---|---|---|
| Go | `app.Dialog.Info()/Warning()/Error()/Question()/Custom()` | builder → `Show()` |
| Go | `app.Dialog.OpenFile()` builder | `(path, error)` via `PromptForSingleSelection()` |
| Go | `app.Dialog.SaveFile()` builder | `(path, error)` |
| JS | `Dialogs.Info/Warning/Error/Question(options)` | `Promise` (clicked label for Question) |
| JS | `Dialogs.OpenFile(options)` | `Promise<string \| string[]>` |
| JS | `Dialogs.SaveFile(options)` | `Promise<string>` |

> ⚠️ Dialog builders are one-shot: configure → `Show()`/`Prompt...()`. Don't reuse a built dialog.
