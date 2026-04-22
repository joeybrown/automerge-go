# Migration Plan: automerge-go from cgo to wazero

## Goal

Replace the cgo/C-FFI backend of `automerge-go` with a wazero/WASI backend while
preserving the existing public API as closely as possible, so that the change can
be proposed as an upstream merge to `github.com/automerge/automerge-go`.

## Background

### Current Architecture (cgo)

```
Go public API  →  cgo bridge (unsafe, C pointers)  →  automerge-c static libs (per-platform)
```

- Four platform-specific static libraries built from Rust via `cargo`/`cross`
- `runtime.SetFinalizer` + `runtime.KeepAlive` to prevent GC of C allocations
- Direct pointer passing between Go and C memory spaces
- Requires C compiler and platform-specific libs at build time

### Target Architecture (wazero)

```
Go public API  →  wazero FFI bridge (pure Go)  →  automerge WASI module (.wasm)
```

- Single `.wasm` binary, embeddable via `//go:embed`
- No C compiler required; pure Go build
- Memory-safe WASM sandbox; all data copied across boundary
- Proven patterns exist in `automerge-wazero-example`

---

## Public API Surface to Preserve

The existing automerge-go API that consumers depend on:

### Core Types
| Type | Description | Preservation |
|------|-------------|--------------|
| `*Doc` | Mutable document root (thread-safe via mutex) | Keep, adapt internals |
| `*Value` | Read-only dynamically-typed value wrapper | Keep |
| `*Map` | Mutable string→value mapping | Keep |
| `*List` | Mutable indexed array | Keep |
| `*Text` | Mutable unicode text | Keep |
| `*Counter` | Increment-only int64 | Keep |
| `*Change` / `ChangeHash` | Immutable change records | Keep (may need serialization bridge) |
| `*SyncState` / `*SyncMessage` | Sync protocol types | Keep |
| `*Path` | Cursor-based navigation | Keep |
| `Kind` enum | Type discriminator | Keep |

### Key Functions & Methods
| Function | Notes |
|----------|-------|
| `New() *Doc` | Will create wazero runtime internally |
| `Load([]byte) (*Doc, error)` | Same |
| `Doc.Save() []byte` | Map to `am_save` |
| `Doc.Fork(...)` | Map to `am_fork` |
| `Doc.Merge(*Doc)` | Map to `am_merge` (serialize other doc, pass bytes) |
| `Doc.Root() *Value` | Need WASM-side introspection |
| `Doc.RootMap() *Map` | Same |
| `Doc.Path(...) *Path` | Keep Go-side path resolution |
| `Doc.Commit(...)` | Map to commit export |
| `Doc.Changes(...)` / `Doc.Apply(...)` | History exports |
| `Doc.Heads()` | Map to `am_history_heads` |
| `Doc.ActorID()` / `Doc.SetActorID()` | Map to `am_get_actor` / `am_set_actor` |
| `Doc.SaveIncremental()` / `Doc.LoadIncremental()` | Need WASM exports |
| `NewSyncState()` | Map to `am_sync_state_init` |
| `SyncState.GenerateMessage()` | Map to `am_sync_gen` |
| `SyncState.ReceiveMessage()` | Map to `am_sync_recv` |
| `As[T](*Value)` | Keep; Go-side generic conversion |
| `Map.Get/Set/Delete/Keys/Len/Values/GoMap` | Map to `am_map_*` |
| `List.Get/Set/Append/Insert/Delete/Len/Values` | Map to `am_list_*` |
| `Text.Get/Set/Splice/Len` | Map to `am_text_*` |
| `Counter.Get/Inc` | Map to `am_counter_*` |

### API Compatibility Challenges

1. **`context.Context` parameter**: The current cgo API does NOT take `context.Context`.
   The wazero-example API does. We need to decide:
   - **Option A**: Add `context.Context` to all methods (breaking change, but idiomatic for wazero)
   - **Option B**: Store a context internally or use `context.Background()` (hides cancellation)
   - **Recommendation**: Option A for new internal methods; provide backward-compatible
     wrappers that use `context.Background()` during transition, with a note that the
     context-accepting variants are preferred.

2. **Detached objects**: Current API supports `NewMap()`, `NewList()`, etc. that create
   objects not yet attached to a doc. The WASM module is single-doc per instance, so
   detached objects would need to be handled Go-side (buffered) or the WASM module
   would need multi-object support.

3. **`*Value` introspection**: Current `*Value` wraps a C `AMitem*` and lazily reads
   typed data. With WASM, values must be eagerly copied out since WASM memory is not
   directly addressable from Go. This means `*Value` becomes a snapshot rather than
   a live reference.

4. **`runtime.SetFinalizer` lifecycle**: Current code ties Go GC to C memory. With
   wazero, the WASM module manages its own memory, but we still need to track
   allocations (sync peer IDs, etc.).

5. **Thread safety**: Current `*Doc` uses a mutex around the C doc pointer. With wazero,
   the WASM module instance is single-threaded. The mutex remains necessary, and all
   WASM calls must be serialized.

6. **Generics (`As[T]`)**: The `As[T](*Value)` generic function and `normalize.go` type
   conversion logic are pure Go — they should transfer directly with minimal changes.

---

## Phased Work Plan

### Phase 0: Rust WASI Module — Expand Exports

The existing `automerge_wasi` module in `automerge-wazero-example` covers many
operations but was designed for a demo app, not as a full replacement for automerge-c.
We need to expand it to cover the full automerge-go surface.

**Missing WASI exports needed** (comparing automerge.h C API to current 57 WASI exports):

| Category | Missing Exports | Priority |
|----------|----------------|----------|
| Document | `am_commit`, `am_save_incremental`, `am_load_incremental` | High |
| Changes | `am_changes`, `am_apply_changes`, `am_change_hash`, `am_change_message`, `am_change_timestamp`, `am_load_changes`, `am_save_changes` | High |
| Object access | Generic `am_get(obj_id, key/index)` with obj_id parameter (not just root), nested object traversal | Critical |
| Map/List with obj_id | Current exports hardcode ROOT; need arbitrary obj_id support for nested structures | Critical |
| Value types | Full type discriminator on get (currently returns strings; need int64, uint64, float64, bool, bytes, time, counter, null) | Critical |
| Fork | `am_fork`, `am_fork_at` | Medium |
| Marks/Cursor | Already implemented in wazero-example | Low (M2+) |

**Key architectural decision**: The current WASI module uses a single global doc with
hardcoded paths (e.g., `ROOT["content"]` for text). The automerge-go API needs
arbitrary nested object access via `ObjId`. Two approaches:

- **Option A — ObjId as integer handle**: The WASM module maintains a table of ObjIds.
  Go refers to objects by handle (u32). WASM module resolves handle → ObjId internally.
  This is the cleanest approach.
- **Option B — ObjId as serialized bytes**: Pass ObjId bytes across the boundary.
  More complex marshaling but no server-side state table.
- **Recommendation**: Option A (handle table). This is how the C API works (opaque pointers).

#### Deliverables
- [ ] Extended Rust WASI module with full automerge-go API coverage
- [ ] ObjId handle table for nested object access
- [ ] Typed value returns (not just strings)
- [ ] Commit/changes/incremental-save exports
- [ ] Build `.wasm` binary and verify with unit tests

---

### Phase 1: Backend Abstraction Layer in automerge-go

Introduce an internal interface that abstracts the backend, allowing the cgo
implementation to be swapped for wazero without changing the public API.

```go
// internal/backend/backend.go
type Backend interface {
    // Document lifecycle
    Init(ctx context.Context) error
    Close(ctx context.Context) error
    Save(ctx context.Context) ([]byte, error)
    Load(ctx context.Context, data []byte) error

    // Actor
    GetActorID(ctx context.Context) (string, error)
    SetActorID(ctx context.Context, id string) error

    // Object access (via handle)
    RootObjHandle() ObjHandle
    Put(ctx context.Context, obj ObjHandle, key string, value any) (ObjHandle, error)
    PutAtIndex(ctx context.Context, obj ObjHandle, index uint, value any) (ObjHandle, error)
    Get(ctx context.Context, obj ObjHandle, key string) (Value, error)
    GetAtIndex(ctx context.Context, obj ObjHandle, index uint) (Value, error)
    Delete(ctx context.Context, obj ObjHandle, key string) error
    DeleteAtIndex(ctx context.Context, obj ObjHandle, index uint) error
    Keys(ctx context.Context, obj ObjHandle) ([]string, error)
    Len(ctx context.Context, obj ObjHandle) (uint, error)

    // Text
    TextSplice(ctx context.Context, obj ObjHandle, pos uint, del int, text string) error
    TextGet(ctx context.Context, obj ObjHandle) (string, error)
    TextLen(ctx context.Context, obj ObjHandle) (uint, error)

    // Counter
    CounterGet(ctx context.Context, obj ObjHandle) (int64, error)
    CounterIncrement(ctx context.Context, obj ObjHandle, delta int64) error

    // List-specific
    ListInsert(ctx context.Context, obj ObjHandle, index uint, value any) (ObjHandle, error)
    ListAppend(ctx context.Context, obj ObjHandle, value any) (ObjHandle, error)

    // Changes
    Commit(ctx context.Context, msg string, timestamp time.Time) ([]byte, error)
    Changes(ctx context.Context, since [][]byte) ([][]byte, error)
    ApplyChanges(ctx context.Context, changes [][]byte) error
    Heads(ctx context.Context) ([][]byte, error)
    SaveIncremental(ctx context.Context) ([]byte, error)
    LoadIncremental(ctx context.Context, data []byte) error

    // Merge
    Merge(ctx context.Context, otherSaved []byte) error
    Fork(ctx context.Context) (Backend, error)
    ForkAt(ctx context.Context, heads [][]byte) (Backend, error)

    // Sync
    SyncInit(ctx context.Context) (SyncHandle, error)
    SyncFree(ctx context.Context, h SyncHandle) error
    SyncGenerateMessage(ctx context.Context, h SyncHandle) ([]byte, bool, error)
    SyncReceiveMessage(ctx context.Context, h SyncHandle, msg []byte) error
}
```

#### Deliverables
- [ ] Define `Backend` interface in `internal/backend/`
- [ ] Implement `wazeroBackend` that wraps the wazero FFI calls
- [ ] Refactor `*Doc`, `*Map`, `*List`, `*Text`, `*Counter` to use `Backend` instead of direct C calls
- [ ] `*Value` becomes a snapshot struct holding kind + Go-native value (no C pointers)
- [ ] Keep all existing tests passing (initially with cgo backend if needed for comparison)

---

### Phase 2: Wire Up wazero Backend

Replace the cgo calls with wazero backend calls throughout the codebase.

#### Key File Changes

| File | Change |
|------|--------|
| `result.go` | Remove cgo linking directives; remove `wrap()`, `must()`, C result handling |
| `automerge.go` / `doc.go` | `Doc` holds `Backend` instead of `*C.AMdoc`; `New()`/`Load()` instantiate wazero runtime |
| `map.go` | `Map` holds `Backend` + `ObjHandle` instead of `*C.AMobjId` |
| `list.go` | Same pattern |
| `text.go` | Same pattern |
| `counter.go` | Same pattern |
| `value.go` | `Value` is now a pure Go struct (kind + native val); remove `*item` dependency |
| `item.go` | Remove entirely (was the C↔Go value bridge) |
| `sync_state.go` | `SyncState` holds `Backend` + `SyncHandle` |
| `changes.go` | Use Backend changes/apply methods |
| `path.go` | Keep mostly as-is; resolve paths via Backend.Get chain |
| `normalize.go` | Keep as-is (pure Go) |
| `go.mod` | Add `github.com/tetratelabs/wazero`; remove C build requirements |

#### WASM Embedding Strategy
- Embed the `.wasm` binary using `//go:embed automerge.wasm` in a dedicated file
- Each `Doc` gets its own wazero module instance (isolation)
- Module compilation is shared (compile once, instantiate many)

#### Deliverables
- [ ] Remove all cgo code (`result.go` C directives, `item.go`, C pointer handling)
- [ ] Remove `automerge.h` and `deps/` directory (static libs no longer needed)
- [ ] Embed `.wasm` binary
- [ ] All existing tests pass with wazero backend
- [ ] Verify no `import "C"` remains

---

### Phase 3: Test Parity and Edge Cases

Ensure the wazero backend matches the cgo backend behavior exactly.

#### Test Categories
| Category | Files | Focus |
|----------|-------|-------|
| Core CRUD | `automerge_test.go` | Doc create/load/save, map/list/text/counter ops |
| Type conversion | `normalize_test.go` | Struct marshaling, numeric overflow, tag parsing |
| Path navigation | `path_test.go` | Nested access, auto-creation, void handling |
| Sync protocol | `automerge_test.go` | SyncState round-trip, multi-peer convergence |
| Examples | `example_test.go` | All doc examples still work |
| Concurrency | New tests | Mutex correctness with wazero single-threaded instance |
| Performance | New benchmarks | Startup cost, operation throughput vs cgo baseline |

#### Deliverables
- [ ] 100% existing test pass rate
- [ ] Concurrency stress tests
- [ ] Benchmark comparison (cgo vs wazero)
- [ ] Document any behavioral differences

---

### Phase 4: Cleanup and Upstream Preparation

Prepare the change for upstream merge proposal.

#### Deliverables
- [ ] Update `go.mod` (module path stays `github.com/automerge/automerge-go`)
- [ ] Update `README.md` — remove cgo/C compiler requirements, document wazero
- [ ] Update `doc.go` package documentation
- [ ] Remove `deps/` directory and build scripts
- [ ] Remove `automerge.h`
- [ ] Remove `cmd/automerge-debug/` if it depends on C internals (or migrate it)
- [ ] Ensure `go vet`, `staticcheck`, tests all clean
- [ ] Write migration guide for existing users (if any API changes)
- [ ] Tag as pre-release for testing

---

## Risk Assessment

| Risk | Impact | Mitigation |
|------|--------|------------|
| WASM startup overhead (~100ms per Doc) | Performance regression | Compile module once, share across instances; lazy init |
| WASM call overhead per operation | Slower than direct cgo | Batch operations where possible; benchmark to quantify |
| Missing WASI exports for edge cases | Blocked features | Phase 0 comprehensive audit against automerge.h |
| ObjId handle table complexity | Bugs in nested object access | Thorough testing; align with C API's opaque pointer model |
| Detached object semantics | API incompatibility | Buffer detached objects Go-side; attach on write |
| `context.Context` API change | Breaking change | Keep non-context versions as wrappers initially |
| WASM binary size (~800KB embedded) | Larger Go binary | Acceptable tradeoff for portability; compress if needed |
| Upstream acceptance | Merge rejected | Maintain API compat; show clear benefits (portability, no C dep) |

---

## Decision Log

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Object reference model | Handle table (u32 IDs) | Matches C API's opaque pointer pattern; clean WASM boundary |
| WASM embedding | `//go:embed` | Single binary distribution; no external file dependency |
| Module instances | One per Doc | Isolation; matches current mutex-per-Doc model |
| context.Context | Add to backend interface; wrap for compat | Wazero requires context; backward compat via wrappers |
| Backend interface | Internal (unexported) | Not part of public API; flexibility to change |
| Value semantics | Eager snapshot | WASM memory not directly addressable; copy on read |

---

## Estimated Effort by Phase

| Phase | Scope |
|-------|-------|
| Phase 0 | Extend Rust WASI module — largest single piece of new code |
| Phase 1 | Backend abstraction — moderate refactor, mostly mechanical |
| Phase 2 | Wire up wazero — systematic replacement of cgo calls |
| Phase 3 | Testing — thorough but bounded |
| Phase 4 | Cleanup — straightforward |

Phases 0 and 2 are the heaviest. Phase 1 is the most architecturally important.
