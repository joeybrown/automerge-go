# Directory Reorganization Plan: automerge-go

## Current State

Everything lives in a single flat package (`package automerge`) at the repo root.
There is no separation between the public API that consumers import and the internal
WASM/backend plumbing. The `internal/` directory exists but is empty.

```
automerge-go/
├── automerge.go          (package docs)
├── doc.go                (Doc type — core public API)
├── map.go                (Map type)
├── list.go               (List type)
├── text.go               (Text type)
├── counter.go            (Counter type)
├── value.go              (Value type)
├── kind.go               (Kind enum)
├── path.go               (Path navigation)
├── changes.go            (Change, ChangeHash)
├── sync_state.go         (SyncState, SyncMessage)
├── normalize.go          (Go↔automerge type conversion)
├── backend.go            (backend interface + internal types)
├── wazero_backend.go     (wazero implementation of backend)
├── *_test.go             (tests)
├── cmd/automerge-debug/  (CLI tool)
├── internal/             (empty)
└── wasi/                 (Rust WASI source)
```

---

## Public API Surface (what 3rd-party consumers need)

These are the types, functions, methods, and constants a library user interacts
with. Everything below MUST remain importable from the module's public package.

### Core Types

| Type | File today | Description |
|------|-----------|-------------|
| `Doc` | doc.go | The automerge document; all operations flow through it |
| `Value` | value.go | Dynamically-typed value read from a document |
| `Path` | path.go | Cursor into nested document structure |
| `Kind` | kind.go | Enum for value types |

### Collection / CRDT Types

| Type | File today | Description |
|------|-----------|-------------|
| `Map` | map.go | Collaborative key→value map |
| `List` | list.go | Collaborative ordered list |
| `Text` | text.go | Collaborative text string |
| `Counter` | counter.go | Collaborative increment-only counter |

### Change / History Types

| Type | File today | Description |
|------|-----------|-------------|
| `Change` | changes.go | A committed set of mutations |
| `ChangeHash` | changes.go | SHA-256 identifier for a change |
| `CommitOptions` | doc.go | Options bag for `Doc.Commit()` |

### Sync Types

| Type | File today | Description |
|------|-----------|-------------|
| `SyncState` | sync_state.go | Per-peer sync protocol state |
| `SyncMessage` | sync_state.go | Wire message for sync |

### Top-Level Functions

| Function | File today | Description |
|----------|-----------|-------------|
| `New() *Doc` | doc.go | Create empty document |
| `Load([]byte) (*Doc, error)` | doc.go | Load document from bytes |
| `NewActorID() string` | doc.go | Generate unique actor ID |
| `NewMap() *Map` | map.go | Create detached map |
| `NewList() *List` | list.go | Create detached list |
| `NewText(string) *Text` | text.go | Create detached text |
| `NewCounter(int64) *Counter` | counter.go | Create detached counter |
| `NewChangeHash(string) (ChangeHash, error)` | changes.go | Parse hex hash |
| `LoadChanges([]byte) ([]*Change, error)` | changes.go | Decode changes from bytes |
| `SaveChanges([]*Change) []byte` | changes.go | Encode changes to bytes |
| `NewSyncState(*Doc) *SyncState` | sync_state.go | Create sync state for a peer |
| `LoadSyncState(*Doc, []byte) (*SyncState, error)` | sync_state.go | Resume saved sync state |
| `LoadSyncMessage([]byte) (*SyncMessage, error)` | sync_state.go | Decode sync message |
| `As[T any](*Value, ...error) (T, error)` | normalize.go | Type-safe value extraction |

### Kind Constants

```
KindVoid, KindBool, KindBytes, KindCounter, KindFloat64,
KindInt64, KindUint64, KindNull, KindStr, KindTime,
KindUnknown, KindMap, KindList, KindText
```

---

## Internal Implementation (consumers must NOT access)

| Symbol / File | Why it's internal |
|---------------|------------------|
| `backend` interface | Abstraction for swapping cgo/wazero — users never call it |
| `wazeroBackend` struct | wazero runtime plumbing |
| `objHandle` type | Opaque WASM memory handle |
| `backendValue` struct | Intermediate value transfer between WASM↔Go |
| `changeInfo` struct | Backend-level change metadata |
| `tag*` constants | Wire format byte tags |
| `errEmptyCommit` | Sentinel error (used internally only) |
| `normalize()` func | Go→automerge coercion (called by public types, not by users) |
| `parseTags()` func | Struct tag parser (implementation detail of normalize) |
| `wasi/` directory | Rust source for the WASM module |
| `cmd/automerge-debug/` | Developer-only CLI tool |

---

## Proposed Directory Structure

```
automerge-go/
├── go.mod                       (module github.com/automerge/automerge-go)
│
├── automerge.go                 (package-level godoc, re-exports nothing new)
├── doc.go                       (Doc, CommitOptions, New, Load, NewActorID)
├── map.go                       (Map, NewMap)
├── list.go                      (List, NewList)
├── text.go                      (Text, NewText)
├── counter.go                   (Counter, NewCounter)
├── value.go                     (Value)
├── kind.go                      (Kind, Kind* constants)
├── path.go                      (Path)
├── changes.go                   (Change, ChangeHash, LoadChanges, SaveChanges, NewChangeHash)
├── sync_state.go                (SyncState, SyncMessage, NewSyncState, LoadSyncState, LoadSyncMessage)
├── normalize.go                 (As[T], normalize, parseTags)
│
├── internal/
│   ├── backend/
│   │   ├── backend.go           (backend interface, objHandle, backendValue, changeInfo, tag* consts, errEmptyCommit)
│   │   └── backend_test.go      (unit tests for backend contract, if any)
│   │
│   └── wazero/
│       ├── wazero.go            (wazeroBackend — implements internal/backend.backend)
│       ├── wazero_test.go       (wazero-specific tests)
│       └── automerge.wasm       (embedded WASM binary, once built)
│
├── cmd/
│   └── automerge-debug/
│       └── main.go              (developer CLI — unchanged)
│
├── wasi/                        (Rust source — unchanged, used only at build time)
│   ├── Cargo.toml
│   └── src/
│
└── *_test.go                    (integration / public-API tests stay at root)
```

### What goes where

| Directory | Package name | Visibility | Contents |
|-----------|-------------|------------|----------|
| `/` (root) | `automerge` | **Public** | All types and functions from the "Public API Surface" section above. This is what `import "github.com/automerge/automerge-go"` gives you. |
| `internal/backend/` | `backend` | **Internal** | The `backend` interface, `objHandle`, `backendValue`, `changeInfo`, byte-tag constants, and `errEmptyCommit`. Anything that describes the contract between the public API layer and the engine. |
| `internal/wazero/` | `wazero` | **Internal** | The `wazeroBackend` implementation. WASM compilation, memory management, FFI calls. The embedded `.wasm` binary lives here too. |
| `cmd/automerge-debug/` | `main` | N/A | Developer tool, not importable. |
| `wasi/` | N/A (Rust) | N/A | Build-time only. `cargo build --target wasm32-wasip1` produces the `.wasm` that gets embedded into `internal/wazero/`. |

---

## Migration Steps

### Step 1 — Create `internal/backend/` package

1. Create `internal/backend/backend.go`.
2. Move into it (and export):
   - `Backend` interface (rename from `backend` → `Backend` since it crosses packages now)
   - `ObjHandle` type (rename from `objHandle`)
   - `BackendValue` struct (rename from `backendValue`)
   - `ChangeInfo` struct (rename from `changeInfo`)
   - All `Tag*` constants (rename from `tag*`)
   - `ErrEmptyCommit` (rename from `errEmptyCommit`)
   - `RootObjHandle`, `InvalidObjHandle` constants
3. Update all root-package files to `import "github.com/automerge/automerge-go/internal/backend"` and reference `backend.Backend`, `backend.ObjHandle`, etc.

### Step 2 — Create `internal/wazero/` package

1. Create `internal/wazero/wazero.go`.
2. Move the `wazeroBackend` struct and all its methods from `wazero_backend.go`.
3. Rename to `Backend` (exported within `internal/wazero`).
4. Have it implement `backend.Backend`.
5. Move WASM compilation / `//go:embed` into this package.
6. Update `doc.go` (the `New()` / `Load()` functions) to call `wazero.NewBackend(ctx)` instead of `newWazeroBackend(ctx)`.

### Step 3 — Move `normalize` helpers if desired

`normalize()` and `parseTags()` are internal helpers, but they operate purely on
Go reflection and the public `Kind`/`Value` types — they don't touch the backend.
They can stay in the root package as unexported functions, or optionally move to
`internal/convert/` if the root package feels too large later. **Recommendation:
leave them at root for now** since they're referenced pervasively by `Map`, `List`,
`Text`, etc.

### Step 4 — Update tests

- Root-level `*_test.go` files stay at root — they test the public API.
- Any tests that exercise backend internals directly move to `internal/backend/` or `internal/wazero/`.
- `benchmark_test.go` and `concurrency_test.go` stay at root (they use the public API).

### Step 5 — Verify

1. `go build ./...` — everything compiles.
2. `go test ./...` — all tests pass.
3. `go vet ./...` — no issues.
4. Consumers can `import "github.com/automerge/automerge-go"` and access exactly the public API surface listed above — nothing more, nothing less.
5. Consumers **cannot** import `internal/backend` or `internal/wazero` (Go enforces this).

---

## What Does NOT Change

- **Module path**: stays `github.com/automerge/automerge-go`
- **Public package name**: stays `automerge`
- **All public type names, method signatures, function signatures**: byte-for-byte identical
- **`wasi/` directory**: untouched (Rust build source)
- **`cmd/automerge-debug/`**: untouched
- **`go.mod` dependencies**: unchanged

## Risks & Notes

- **Circular imports**: The root `automerge` package will depend on `internal/backend` for the interface. `internal/wazero` will also depend on `internal/backend`. Neither internal package should import the root — if they need root types (like `Kind`, `ChangeHash`), those definitions either stay in root or a small `internal/types` package is introduced. `Kind` and `ChangeHash` are used in the `Backend` interface signatures, so they may need to live in `internal/backend` or a shared `internal/types` package, with type aliases in the root package for public consumption.

- **Type alias strategy for shared types**: If `Kind` and `ChangeHash` must appear in both the `Backend` interface and the public API, define them in `internal/backend` (or `internal/types`) and add type aliases in the root:
  ```go
  // In root kind.go
  type Kind = backend.Kind
  const KindMap = backend.KindMap
  // etc.
  ```

- **No `pkg/` directory needed**: In idiomatic Go, a `pkg/` directory is [generally discouraged](https://go.dev/blog/package-names) for library modules. The public API lives directly at the module root. `pkg/` is sometimes used in application repos with mixed `cmd/` and library code, but for a library-first module like this, the root package IS the public API. If you strongly prefer `pkg/`, the public types could live in `pkg/automerge/` but this would change the import path to `github.com/automerge/automerge-go/pkg/automerge`, which is non-standard and verbose.
