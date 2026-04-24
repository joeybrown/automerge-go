# Plan: Add RichText and Cursor Support to automerge-go

## Overview

Add rich text marks (formatting) and cursor (stable position tracking) support
to `joeybrown/automerge-go`, following the same patterns established in
`joeybrown/automerge-wazero-example`.

The existing automerge-go stack has three layers:
1. **Rust WASI** (`wasi/src/*.rs`) — wraps automerge-rs 0.8.0 via C-ABI WASM exports
2. **Go wazero bridge** (`internal/wazero/wazero.go`) — calls WASM exports via wazero
3. **Go public API** (`*.go` in root) — user-facing types and methods

automerge-rs 0.8.0 already provides `mark()`, `unmark()`, `marks()`,
`get_cursor()`, and `get_cursor_position()`. We just need to plumb them
through all three layers.

## Changes

### Layer 1: Rust WASI Exports

#### `wasi/src/richtext_ops.rs` (new file)
WASM exports for rich text marks, using the same `resolve_obj` handle-based
approach as existing ops (not the wazero-example's `get_text_obj_id` approach):

| Export | Signature | Returns |
|--------|-----------|---------|
| `am_mark` | `(obj_handle, name_ptr, name_len, value_ptr, value_len, start, end, expand) → i32` | 0=ok, negative=error |
| `am_unmark` | `(obj_handle, name_ptr, name_len, start, end, expand) → i32` | 0=ok, negative=error |
| `am_marks_len` | `(obj_handle) → u32` | JSON byte length |
| `am_marks` | `(obj_handle, ptr_out) → i32` | Writes JSON, returns 0=ok |

Key difference from wazero-example: accepts `obj_handle: u32` to identify the
Text object via the existing handle table, rather than relying on a single
global text object.

#### `wasi/src/cursor_ops.rs` (new file)
WASM exports for cursors:

| Export | Signature | Returns |
|--------|-----------|---------|
| `am_get_cursor` | `(obj_handle, index) → i32` | Positive=cursor string len, negative=error |
| `am_get_cursor_str` | `(ptr_out) → i32` | Copies last cursor to buffer |
| `am_lookup_cursor` | `(obj_handle, cursor_ptr, cursor_len) → i32` | >=0 = index, negative=error |

Key difference from wazero-example: uses `obj_handle` instead of path strings,
consistent with how all other ops in automerge-go work.

#### `wasi/src/lib.rs`
Add `mod richtext_ops;` and `mod cursor_ops;`.

### Layer 2: Backend Interface + wazero Bridge

#### `internal/backend/backend.go`
Add methods to the `Backend` interface:

```go
// Rich text marks
Mark(ctx context.Context, obj ObjHandle, name string, value string, start, end uint, expand uint8) error
Unmark(ctx context.Context, obj ObjHandle, name string, start, end uint, expand uint8) error
Marks(ctx context.Context, obj ObjHandle) (string, error)

// Cursors
GetCursor(ctx context.Context, obj ObjHandle, index uint) (string, error)
LookupCursor(ctx context.Context, obj ObjHandle, cursor string) (uint, error)
```

#### `internal/wazero/wazero.go`
Implement the five new Backend methods following the existing FFI patterns
(alloc/writeBytes/call/readBytes/free).

### Layer 3: Go Public API

#### `text.go` — extend existing `*Text` type
Add methods directly to `*Text`:

```go
func (t *Text) Mark(name string, value any, start, end int, expand ExpandMark) error
func (t *Text) Unmark(name string, start, end int, expand ExpandMark) error
func (t *Text) Marks() ([]Mark, error)
func (t *Text) GetCursor(index int) (*Cursor, error)
func (t *Text) LookupCursor(c *Cursor) (int, error)
```

#### New types (in `text.go` or separate files)

```go
type Mark struct {
    Name  string
    Value string
    Start uint
    End   uint
}

type ExpandMark uint8
const (
    ExpandNone   ExpandMark = 0
    ExpandBefore ExpandMark = 1
    ExpandAfter  ExpandMark = 2
    ExpandBoth   ExpandMark = 3
)

type Cursor struct {
    bytes string // opaque cursor string from automerge
}
```

## File Summary

| File | Action |
|------|--------|
| `wasi/src/richtext_ops.rs` | Create |
| `wasi/src/cursor_ops.rs` | Create |
| `wasi/src/lib.rs` | Edit (add mod declarations) |
| `internal/backend/backend.go` | Edit (add interface methods) |
| `internal/wazero/wazero.go` | Edit (add implementations) |
| `text.go` | Edit (add Mark/Unmark/Marks/GetCursor/LookupCursor methods) |
| `kind.go` | No change needed |
| `value.go` | No change needed |

## Testing

After implementation, rebuild the WASM binary and run the existing test suite
plus new tests for mark/cursor operations.
