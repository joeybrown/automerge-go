# Phase 0: Extend Rust WASI Module for Full automerge-go Coverage

## STATUS: COMPLETE

All Rust modules built and tested (37/37 Rust tests pass).
WASM binary built (1014KB release, wasm32-wasip1).
Go smoke tests via wazero pass (14/14 tests pass).

## Overview

The existing `automerge_wasi` Rust crate in `automerge-wazero-example` has 57
WASI exports designed for a demo app. The automerge-go cgo binding calls **66
unique C functions** from `automerge.h`. This phase extends the WASI module to
cover every operation automerge-go needs.

The new WASI module will live in `joeybrown/automerge-go/wasi/` so the
automerge-go repo is self-contained.

---

## Gap Analysis: What automerge-go Needs vs What Exists

### Already Covered (with modifications needed)

These categories exist in the wazero-example but need changes to support
**arbitrary ObjId** instead of hardcoded ROOT:

| Category | Existing | Needs |
|----------|----------|-------|
| Map CRUD | `am_map_set/get/delete/keys/len` (ROOT only) | Must accept ObjId handle param |
| List CRUD | `am_list_push/insert/get/delete/len` (hardcoded list) | Must accept ObjId handle param |
| Text ops | `am_text_splice/get_text/get_text_len` (hardcoded content) | Must accept ObjId handle param |
| Counter | `am_counter_create/increment/get` (ROOT keys) | Must accept ObjId handle param |
| Document | `am_init/save/load/merge/fork` | `am_init` must not hardcode ROOT["content"] |
| Actor | `am_get_actor/set_actor` | OK as-is |
| Sync | `am_sync_state_init/free/gen/recv` | OK as-is, need encode/decode |
| History | `am_get_heads/get_changes/apply_changes` | OK as-is |
| Memory | `am_alloc/am_free` | OK as-is |

### Missing Entirely (Must Add)

| C function used by automerge-go | WASI export needed | Notes |
|---|---|---|
| `AMcreate` | `am_create` | Create doc (no hardcoded content obj) |
| `AMcommit` | `am_commit` | Explicit commit with message + timestamp |
| `AMemptyChange` | `am_empty_change` | Commit with no ops |
| `AMsaveIncremental` | `am_save_incremental` | Delta export |
| `AMloadIncremental` | `am_load_incremental` | Delta import |
| `AMgetChangeByHash` | `am_get_change_by_hash` | Lookup single change |
| `AMgetChanges` | Already exists as `am_get_changes` | ✅ |
| `AMapplyChanges` | Already exists as `am_apply_changes` | ✅ |
| `AMgetHeads` | Already exists as `am_get_heads` | ✅ |
| `AMfork` | Already exists as `am_fork` | Need fork-at-heads variant |
| `AMmerge` | Already exists as `am_merge` | ✅ |
| `AMclone` | `am_clone` | Non-forking copy |
| `AMobjObjType` | `am_obj_type` | Query object type by handle |
| `AMobjSize` | `am_obj_size` | Query object size by handle |
| `AMkeys` | `am_keys` | Keys iterator for any obj |
| `AMobjItems` | `am_obj_items` | Values iterator for any obj |
| `AMtext` | `am_text_get` | Already exists, needs ObjId |
| `AMspliceText` | `am_text_splice` | Already exists, needs ObjId |
| `AMmapGet` with typed returns | `am_map_get_*` | Must return typed values (not just strings) |
| `AMmapPut{Bool,Int,Uint,F64,Str,Bytes,Null,Counter,Timestamp,Object}` | `am_map_put` | Single typed-put function |
| `AMlistGet` with typed returns | `am_list_get` | Must return typed values |
| `AMlistPut{Bool,Int,Uint,F64,Str,Bytes,Null,Counter,Timestamp,Object}` | `am_list_put` | Single typed-put (with insert flag) |
| `AMlistRange` | `am_list_range` | Range-read list |
| `AMlistDelete` | Already exists | Needs ObjId |
| `AMlistIncrement` / `AMmapIncrement` | `am_increment` | Counter increment at pos/key |
| `AMsyncStateDecode/Encode` | `am_sync_state_save/load` | Persist sync state |
| `AMsyncMessageDecode/Encode` | `am_sync_message_decode/encode` | Wire format |
| `AMsyncMessageChanges/Heads` | Inspection of sync messages | Medium priority |
| `AMchangeHash/Message/Time/Seq/ActorId/Deps/RawBytes` | Change introspection | Via handle |
| `AMchangeLoadDocument` | `am_load_changes` | Load changes from bytes |

---

## Architecture: ObjId Handle Table

The most critical addition. Current WASI exports hardcode `ROOT` or a single
text object. automerge-go needs to address **any nested object** by handle.

### Design

```rust
// In state.rs — extend existing global state
thread_local! {
    static OBJ_TABLE: RefCell<ObjHandleTable> = RefCell::new(ObjHandleTable::new());
}

struct ObjHandleTable {
    entries: Vec<Option<ObjId>>,  // handle → ObjId
    next_free: u32,
}

impl ObjHandleTable {
    fn insert(&mut self, obj: ObjId) -> u32;     // returns handle
    fn get(&self, handle: u32) -> Option<&ObjId>; // lookup
    fn remove(&mut self, handle: u32);            // free slot
}
```

**Conventions:**
- Handle `0` = ROOT (always valid, never freed)
- Handle `1..N` = dynamically allocated ObjIds
- Handles returned from put-object / create operations
- Go side stores handles as `uint32`

### Typed Value Protocol

For returning typed values across the WASM boundary, use a **tag byte + payload**
encoding since WASM can only return scalars:

```
Tag (1 byte) | Payload (variable)

Tag values:
  0x00 = Null
  0x01 = Bool    — 1 byte (0/1)
  0x02 = Int64   — 8 bytes LE
  0x03 = Uint64  — 8 bytes LE
  0x04 = Float64 — 8 bytes LE
  0x05 = String  — remaining bytes are UTF-8
  0x06 = Bytes   — remaining bytes are raw
  0x07 = Counter — 8 bytes LE (i64)
  0x08 = Timestamp — 8 bytes LE (millis since epoch)
  0x09 = Map     — 4 bytes LE (ObjId handle)
  0x0A = List    — 4 bytes LE (ObjId handle)
  0x0B = Text    — 4 bytes LE (ObjId handle)
  0xFF = Void    — no payload
```

This replaces the current string-only returns and maps directly to automerge-go's
`Kind` enum.

---

## Implementation Steps

### Step 1: Create Rust Crate in automerge-go

Set up `joeybrown/automerge-go/wasi/` as a new Rust crate copying patterns from
the wazero-example but designed for the full API.

```
wasi/
├── Cargo.toml
├── src/
│   ├── lib.rs          — module declarations
│   ├── state.rs        — global doc + ObjHandleTable
│   ├── memory.rs       — am_alloc / am_free (copy from existing)
│   ├── document.rs     — create, save, load, merge, fork, clone
│   ├── commit.rs       — commit, empty_change, save/load_incremental
│   ├── actor.rs        — get/set actor
│   ├── obj.rs          — obj_type, obj_size, keys, items
│   ├── map_ops.rs      — typed put/get/delete on maps
│   ├── list_ops.rs     — typed put/get/delete/insert on lists, splice
│   ├── text_ops.rs     — splice_text, get text
│   ├── counter_ops.rs  — increment counter in map/list
│   ├── value.rs        — typed value encoding/decoding
│   ├── changes.rs      — change introspection, load/save changes
│   ├── sync.rs         — sync state + message handling
│   └── heads.rs        — heads, changes-since
```

### Step 2: ObjId Handle Table + Basic Document Ops

- `am_create() → i32` — create empty doc, return 0 on success
- `am_obj_root() → u32` — returns handle 0 (ROOT), for explicitness
- `am_save(ptr, len) → i32` — save doc
- `am_save_len() → u32` — get save size
- `am_load(ptr, len) → i32` — load doc from bytes
- `am_fork(heads_ptr, heads_count) → i32` — fork doc
- `am_merge(other_ptr, other_len) → i32` — merge saved doc
- `am_clone() → i32` — clone doc (new instance with same state)
- `am_get_actor(ptr) → i32` / `am_get_actor_len() → u32`
- `am_set_actor(ptr, len) → i32`

### Step 3: Typed Map Operations (with ObjId handle)

- `am_map_put(obj_handle, key_ptr, key_len, tag, val_ptr, val_len) → i32`
  - Tag byte determines type; for objects, val is obj_type enum
  - Returns new ObjId handle for object puts, or 0 for scalars
- `am_map_get(obj_handle, key_ptr, key_len, out_ptr) → i32`
  - Writes tagged value to out_ptr
- `am_map_get_len(obj_handle, key_ptr, key_len) → u32`
  - Size of tagged value for buffer allocation
- `am_map_delete(obj_handle, key_ptr, key_len) → i32`
- `am_map_keys(obj_handle, out_ptr) → i32` / `am_map_keys_len(obj_handle) → u32`

### Step 4: Typed List Operations (with ObjId handle)

- `am_list_put(obj_handle, index, insert_flag, tag, val_ptr, val_len) → i32`
  - `insert_flag` = 1 to insert before index, 0 to overwrite at index
  - Returns ObjId handle for object puts
- `am_list_get(obj_handle, index, out_ptr) → i32`
  - Writes tagged value to out_ptr
- `am_list_get_len(obj_handle, index) → u32`
- `am_list_delete(obj_handle, index) → i32`
- `am_list_len(obj_handle) → u32`
- `am_list_range(obj_handle, begin, end, out_ptr) → i32`
  - Returns concatenated tagged values

### Step 5: Text Operations (with ObjId handle)

- `am_text_splice(obj_handle, pos, del_count, insert_ptr, insert_len) → i32`
- `am_text_get(obj_handle, out_ptr) → i32` / `am_text_get_len(obj_handle) → u32`
- `am_text_len(obj_handle) → u32` — codepoint count

### Step 6: Counter Operations

- `am_counter_increment(obj_handle, key_or_index, is_map_flag, delta) → i32`
  - For maps: key_ptr/key_len identify the counter
  - For lists: index identifies the counter
- `am_counter_get(obj_handle, key_or_index, is_map_flag) → i64`

### Step 7: Commit and Incremental Save

- `am_commit(msg_ptr, msg_len, timestamp_millis) → i32`
  - Returns change hash (written to buffer); or use separate call
- `am_commit_hash(out_ptr) → i32` / `am_commit_hash_len() → u32`
- `am_empty_change(msg_ptr, msg_len, timestamp_millis) → i32`
- `am_save_incremental(out_ptr) → i32` / `am_save_incremental_len() → u32`
- `am_load_incremental(ptr, len) → i32`

### Step 8: Object Introspection

- `am_obj_type(obj_handle) → i32` — returns 1=Map, 2=List, 3=Text
- `am_obj_size(obj_handle) → u32` — number of keys/elements
- `am_obj_keys(obj_handle, out_ptr) → i32` / `am_obj_keys_len(obj_handle) → u32`
  - Null-separated key strings for maps
- `am_obj_items(obj_handle, out_ptr) → i32` / `am_obj_items_len(obj_handle) → u32`
  - Concatenated tagged values for all entries
- `am_obj_free(obj_handle) → i32` — release handle (not ROOT)

### Step 9: Change Introspection

- `am_change_by_hash(hash_ptr, hash_len, out_ptr) → i32`
  - Returns serialized change info
- `am_changes_since(heads_ptr, heads_count, out_ptr) → i32`
  - Returns concatenated raw change bytes
- `am_changes_since_len(heads_ptr, heads_count) → u32`
- `am_apply_changes(changes_ptr, changes_len) → i32`
  - Apply concatenated raw change bytes
- `am_change_hash(change_ptr, change_len, out_ptr) → i32`
  - Introspect a single change: hash
- `am_change_message(change_ptr, change_len, out_ptr) → i32`
  - Introspect: message
- `am_change_timestamp(change_ptr, change_len) → i64`
- `am_change_actor(change_ptr, change_len, out_ptr) → i32`

### Step 10: Sync Protocol

Keep existing sync exports and add:
- `am_sync_state_save(peer_id, out_ptr) → i32` / `am_sync_state_save_len(peer_id) → u32`
- `am_sync_state_load(data_ptr, data_len) → u32` — returns new peer_id

---

## Build

```bash
cd wasi/
cargo build --target wasm32-wasip1 --release
# Output: target/wasm32-wasip1/release/automerge_wasi.wasm
```

The `.wasm` file will be committed to the automerge-go repo (or built in CI)
and embedded via `//go:embed`.

---

## Testing Strategy

1. **Rust unit tests**: `cargo test` for each module
2. **Integration**: A small Go test program that loads the .wasm via wazero
   and exercises every export (this will become the future backend test suite)
3. **Parity**: Compare output of key operations (save/load/merge round-trip)
   between the C library and the WASI module

---

## Execution Order

| Step | Priority | Description |
|------|----------|-------------|
| 1 | Critical | Create crate, state.rs with ObjHandleTable |
| 2 | Critical | memory.rs, document.rs (create/save/load/merge/fork) |
| 3 | Critical | value.rs (typed encoding), map_ops.rs |
| 4 | Critical | list_ops.rs, text_ops.rs |
| 5 | High | commit.rs, actor.rs |
| 6 | High | obj.rs (introspection), counter_ops.rs |
| 7 | High | changes.rs, heads.rs |
| 8 | High | sync.rs |
| 9 | Medium | cargo test + build .wasm |
| 10 | Medium | Smoke test from Go |
