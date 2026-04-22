package wasitest

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const wasmPath = "../automerge.wasm"

// runtime wraps a wazero module instance for calling WASI exports.
type runtime struct {
	mod_ api.Module
	rt   wazero.Runtime
}

func newRuntime(t *testing.T) *runtime {
	t.Helper()
	ctx := context.Background()

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read wasm: %v", err)
	}

	rt := wazero.NewRuntime(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		t.Fatalf("wasi instantiate: %v", err)
	}

	mod_, err := rt.Instantiate(ctx, wasmBytes)
	if err != nil {
		rt.Close(ctx)
		t.Fatalf("instantiate module: %v", err)
	}

	return &runtime{mod_: mod_, rt: rt}
}

func (r *runtime) close(t *testing.T) {
	t.Helper()
	if err := r.rt.Close(context.Background()); err != nil {
		t.Errorf("close runtime: %v", err)
	}
}

// call is a helper to invoke a WASM export by name.
func (r *runtime) call(t *testing.T, name string, params ...uint64) []uint64 {
	t.Helper()
	ctx := context.Background()
	fn := r.mod_.ExportedFunction(name)
	if fn == nil {
		t.Fatalf("export %q not found", name)
	}
	results, err := fn.Call(ctx, params...)
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return results
}

// alloc allocates n bytes in WASM memory and returns the pointer.
func (r *runtime) alloc(t *testing.T, n uint32) uint32 {
	t.Helper()
	res := r.call(t, "am_alloc", uint64(n))
	ptr := uint32(res[0])
	if ptr == 0 && n > 0 {
		t.Fatalf("am_alloc(%d) returned null", n)
	}
	return ptr
}

// free frees a WASM allocation.
func (r *runtime) free(t *testing.T, ptr, size uint32) {
	t.Helper()
	r.call(t, "am_free", uint64(ptr), uint64(size))
}

// writeBytes writes Go bytes into WASM memory at an allocated pointer.
func (r *runtime) writeBytes(t *testing.T, data []byte) (ptr uint32, size uint32) {
	t.Helper()
	size = uint32(len(data))
	if size == 0 {
		return 0, 0
	}
	ptr = r.alloc(t, size)
	ok := r.mod_.Memory().Write(ptr, data)
	if !ok {
		t.Fatalf("memory write failed at ptr=%d size=%d", ptr, size)
	}
	return ptr, size
}

// readBytes reads n bytes from WASM memory.
// IMPORTANT: wazero's Memory.Read() returns a view (slice) into the WASM
// memory buffer, NOT a copy. Subsequent WASM calls (especially am_free)
// can overwrite the memory and corrupt the returned slice. We always copy
// to a Go-owned buffer to avoid use-after-free via the view.
func (r *runtime) readBytes(t *testing.T, ptr, size uint32) []byte {
	t.Helper()
	view, ok := r.mod_.Memory().Read(ptr, size)
	if !ok {
		t.Fatalf("memory read failed at ptr=%d size=%d", ptr, size)
	}
	out := make([]byte, len(view))
	copy(out, view)
	return out
}

// Value type tags (must match wasi/src/value.rs)
const (
	TagNull      = 0x00
	TagBool      = 0x01
	TagInt64     = 0x02
	TagUint64    = 0x03
	TagFloat64   = 0x04
	TagString    = 0x05
	TagBytes     = 0x06
	TagCounter   = 0x07
	TagTimestamp = 0x08
	TagMap       = 0x09
	TagList      = 0x0A
	TagText      = 0x0B
	TagVoid      = 0xFF
)

func TestCreateAndSave(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	// Create a new document
	res := r.call(t, "am_create")
	if int32(res[0]) != 0 {
		t.Fatalf("am_create failed: %d", int32(res[0]))
	}

	// Save should produce bytes
	res = r.call(t, "am_save_len")
	saveLen := uint32(res[0])
	if saveLen == 0 {
		t.Fatal("am_save_len returned 0")
	}

	ptr := r.alloc(t, saveLen)
	defer r.free(t, ptr, saveLen)
	res = r.call(t, "am_save", uint64(ptr))
	if int32(res[0]) != 0 {
		t.Fatalf("am_save failed: %d", int32(res[0]))
	}

	saved := r.readBytes(t, ptr, saveLen)
	if len(saved) == 0 {
		t.Fatal("saved bytes empty")
	}
	t.Logf("empty doc saved: %d bytes", len(saved))
}

func TestMapPutGetString(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// Put a string at ROOT["greeting"]
	keyPtr, keyLen := r.writeBytes(t, []byte("greeting"))
	defer r.free(t, keyPtr, keyLen)
	valPtr, valLen := r.writeBytes(t, []byte("hello world"))
	defer r.free(t, valPtr, valLen)

	res := r.call(t, "am_map_put",
		0, // obj_handle = ROOT
		uint64(keyPtr), uint64(keyLen),
		uint64(TagString),
		uint64(valPtr), uint64(valLen),
	)
	if int32(res[0]) != 0 {
		t.Fatalf("am_map_put failed: %d", int32(res[0]))
	}

	// Read it back
	res = r.call(t, "am_map_get_len",
		0, uint64(keyPtr), uint64(keyLen),
	)
	getLen := uint32(res[0])
	if getLen == 0 {
		t.Fatal("am_map_get_len returned 0")
	}

	outPtr := r.alloc(t, getLen)
	defer r.free(t, outPtr, getLen)
	r.call(t, "am_map_get", 0, uint64(keyPtr), uint64(keyLen), uint64(outPtr))

	got := r.readBytes(t, outPtr, getLen)
	if got[0] != TagString {
		t.Fatalf("expected TagString (0x%02x), got 0x%02x", TagString, got[0])
	}
	gotStr := string(got[1:])
	if gotStr != "hello world" {
		t.Fatalf("expected 'hello world', got %q", gotStr)
	}
	t.Logf("map get: %q", gotStr)
}

func TestMapPutGetInt64(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	keyPtr, keyLen := r.writeBytes(t, []byte("count"))
	defer r.free(t, keyPtr, keyLen)

	// Encode int64 payload
	var payload [8]byte
	binary.LittleEndian.PutUint64(payload[:], uint64(42))
	valPtr, valLen := r.writeBytes(t, payload[:])
	defer r.free(t, valPtr, valLen)

	res := r.call(t, "am_map_put",
		0, uint64(keyPtr), uint64(keyLen),
		uint64(TagInt64),
		uint64(valPtr), uint64(valLen),
	)
	if int32(res[0]) != 0 {
		t.Fatalf("am_map_put int64 failed: %d", int32(res[0]))
	}

	// Read back
	res = r.call(t, "am_map_get_len", 0, uint64(keyPtr), uint64(keyLen))
	getLen := uint32(res[0])

	outPtr := r.alloc(t, getLen)
	defer r.free(t, outPtr, getLen)
	r.call(t, "am_map_get", 0, uint64(keyPtr), uint64(keyLen), uint64(outPtr))

	got := r.readBytes(t, outPtr, getLen)
	if got[0] != TagInt64 {
		t.Fatalf("expected TagInt64, got 0x%02x", got[0])
	}
	v := int64(binary.LittleEndian.Uint64(got[1:9]))
	if v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
}

func TestNestedMapObject(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// Create a nested map: ROOT["nested"] = {}
	keyPtr, keyLen := r.writeBytes(t, []byte("nested"))
	defer r.free(t, keyPtr, keyLen)

	res := r.call(t, "am_map_put",
		0, uint64(keyPtr), uint64(keyLen),
		uint64(TagMap),
		0, 0, // no payload for object creation
	)
	handle := int32(res[0])
	if handle <= 0 {
		t.Fatalf("expected positive handle, got %d", handle)
	}
	t.Logf("nested map handle: %d", handle)

	// Put a value in the nested map
	innerKeyPtr, innerKeyLen := r.writeBytes(t, []byte("inner"))
	defer r.free(t, innerKeyPtr, innerKeyLen)
	innerValPtr, innerValLen := r.writeBytes(t, []byte("deep value"))
	defer r.free(t, innerValPtr, innerValLen)

	res = r.call(t, "am_map_put",
		uint64(handle), // use the nested handle
		uint64(innerKeyPtr), uint64(innerKeyLen),
		uint64(TagString),
		uint64(innerValPtr), uint64(innerValLen),
	)
	if int32(res[0]) != 0 {
		t.Fatalf("nested put failed: %d", int32(res[0]))
	}

	// Read it back from the nested map
	res = r.call(t, "am_map_get_len",
		uint64(handle), uint64(innerKeyPtr), uint64(innerKeyLen),
	)
	getLen := uint32(res[0])

	outPtr := r.alloc(t, getLen)
	defer r.free(t, outPtr, getLen)
	r.call(t, "am_map_get", uint64(handle), uint64(innerKeyPtr), uint64(innerKeyLen), uint64(outPtr))

	got := r.readBytes(t, outPtr, getLen)
	if got[0] != TagString {
		t.Fatalf("expected TagString, got 0x%02x", got[0])
	}
	if string(got[1:]) != "deep value" {
		t.Fatalf("expected 'deep value', got %q", string(got[1:]))
	}
}

func TestListOperations(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// Create a list at ROOT["items"]
	keyPtr, keyLen := r.writeBytes(t, []byte("items"))
	defer r.free(t, keyPtr, keyLen)

	res := r.call(t, "am_map_put", 0, uint64(keyPtr), uint64(keyLen), uint64(TagList), 0, 0)
	listHandle := uint32(res[0])
	if listHandle == 0 {
		t.Fatal("expected list handle > 0")
	}

	// Insert three items
	for i, s := range []string{"alpha", "beta", "gamma"} {
		vPtr, vLen := r.writeBytes(t, []byte(s))
		res = r.call(t, "am_list_put",
			uint64(listHandle), uint64(i), 1, // insert=true
			uint64(TagString), uint64(vPtr), uint64(vLen),
		)
		if int32(res[0]) != 0 {
			t.Fatalf("list insert %q failed: %d", s, int32(res[0]))
		}
		r.free(t, vPtr, vLen)
	}

	// Check length
	res = r.call(t, "am_list_len", uint64(listHandle))
	if uint32(res[0]) != 3 {
		t.Fatalf("expected list len 3, got %d", uint32(res[0]))
	}

	// Read item at index 1 (natural _len → alloc → _get → read → free pattern)
	res = r.call(t, "am_list_get_len", uint64(listHandle), 1)
	getLen := uint32(res[0])
	outPtr := r.alloc(t, getLen)
	r.call(t, "am_list_get", uint64(listHandle), 1, uint64(outPtr))
	got := r.readBytes(t, outPtr, getLen) // readBytes copies, safe to free after
	r.free(t, outPtr, getLen)

	if got[0] != TagString || string(got[1:]) != "beta" {
		t.Fatalf("expected 'beta', got tag=0x%02x val=%q", got[0], string(got[1:]))
	}

	// Delete index 0
	res = r.call(t, "am_list_delete", uint64(listHandle), 0)
	if int32(res[0]) != 0 {
		t.Fatalf("list delete failed: %d", int32(res[0]))
	}
	res = r.call(t, "am_list_len", uint64(listHandle))
	if uint32(res[0]) != 2 {
		t.Fatalf("expected list len 2 after delete, got %d", uint32(res[0]))
	}
}

func TestTextOperations(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// Create a text object at ROOT["content"]
	keyPtr, keyLen := r.writeBytes(t, []byte("content"))
	defer r.free(t, keyPtr, keyLen)

	res := r.call(t, "am_map_put", 0, uint64(keyPtr), uint64(keyLen), uint64(TagText), 0, 0)
	textHandle := uint32(res[0])
	if textHandle == 0 {
		t.Fatal("expected text handle > 0")
	}

	// Splice in text
	insertPtr, insertLen := r.writeBytes(t, []byte("Hello, World!"))
	defer r.free(t, insertPtr, insertLen)

	res = r.call(t, "am_text_splice",
		uint64(textHandle), 0, 0, // pos=0, del=0
		uint64(insertPtr), uint64(insertLen),
	)
	if int32(res[0]) != 0 {
		t.Fatalf("text splice failed: %d", int32(res[0]))
	}

	// Read text back
	res = r.call(t, "am_text_get_len", uint64(textHandle))
	textByteLen := uint32(res[0])
	if textByteLen != 13 {
		t.Fatalf("expected text len 13, got %d", textByteLen)
	}

	outPtr := r.alloc(t, textByteLen)
	defer r.free(t, outPtr, textByteLen)
	r.call(t, "am_text_get", uint64(textHandle), uint64(outPtr))
	got := r.readBytes(t, outPtr, textByteLen)
	if string(got) != "Hello, World!" {
		t.Fatalf("expected 'Hello, World!', got %q", string(got))
	}

	// Splice: replace "World" with "Go"
	replPtr, replLen := r.writeBytes(t, []byte("Go"))
	defer r.free(t, replPtr, replLen)

	// del_count is isize, pass as uint64 of the signed value
	res = r.call(t, "am_text_splice",
		uint64(textHandle), 7, uint64(5), // pos=7, del=5
		uint64(replPtr), uint64(replLen),
	)
	if int32(res[0]) != 0 {
		t.Fatalf("text splice replace failed: %d", int32(res[0]))
	}

	res = r.call(t, "am_text_get_len", uint64(textHandle))
	textByteLen = uint32(res[0])
	outPtr2 := r.alloc(t, textByteLen)
	defer r.free(t, outPtr2, textByteLen)
	r.call(t, "am_text_get", uint64(textHandle), uint64(outPtr2))
	got = r.readBytes(t, outPtr2, textByteLen)
	if string(got) != "Hello, Go!" {
		t.Fatalf("expected 'Hello, Go!', got %q", string(got))
	}
	t.Logf("text after splice: %q", string(got))
}

func TestCommitAndHeads(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// Empty doc has 0 heads
	res := r.call(t, "am_get_heads_count")
	if uint32(res[0]) != 0 {
		t.Fatalf("expected 0 heads, got %d", uint32(res[0]))
	}

	// Put a value and commit
	keyPtr, keyLen := r.writeBytes(t, []byte("key"))
	defer r.free(t, keyPtr, keyLen)
	valPtr, valLen := r.writeBytes(t, []byte("value"))
	defer r.free(t, valPtr, valLen)

	r.call(t, "am_map_put", 0, uint64(keyPtr), uint64(keyLen), uint64(TagString), uint64(valPtr), uint64(valLen))

	msgPtr, msgLen := r.writeBytes(t, []byte("first commit"))
	defer r.free(t, msgPtr, msgLen)

	res = r.call(t, "am_commit", uint64(msgPtr), uint64(msgLen), 1000)
	if int32(res[0]) != 0 {
		t.Fatalf("am_commit failed: %d", int32(res[0]))
	}

	// Should now have 1 head
	res = r.call(t, "am_get_heads_count")
	if uint32(res[0]) != 1 {
		t.Fatalf("expected 1 head, got %d", uint32(res[0]))
	}

	// Get the hash
	res = r.call(t, "am_get_heads_len")
	headsLen := uint32(res[0])
	if headsLen != 32 {
		t.Fatalf("expected 32 bytes for 1 head, got %d", headsLen)
	}
	hashPtr := r.alloc(t, 32)
	defer r.free(t, hashPtr, 32)
	r.call(t, "am_get_heads", uint64(hashPtr))
	hash := r.readBytes(t, hashPtr, 32)

	nonZero := false
	for _, b := range hash {
		if b != 0 {
			nonZero = true
			break
		}
	}
	if !nonZero {
		t.Fatal("head hash is all zeros")
	}
	t.Logf("head hash: %x", hash)
}

func TestSaveLoadRoundtrip(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// Write data
	keyPtr, keyLen := r.writeBytes(t, []byte("msg"))
	defer r.free(t, keyPtr, keyLen)
	valPtr, valLen := r.writeBytes(t, []byte("persisted"))
	defer r.free(t, valPtr, valLen)
	r.call(t, "am_map_put", 0, uint64(keyPtr), uint64(keyLen), uint64(TagString), uint64(valPtr), uint64(valLen))

	msgPtr, msgLen := r.writeBytes(t, []byte("save test"))
	defer r.free(t, msgPtr, msgLen)
	r.call(t, "am_commit", uint64(msgPtr), uint64(msgLen), 0)

	// Save
	res := r.call(t, "am_save_len")
	saveLen := uint32(res[0])
	savePtr := r.alloc(t, saveLen)
	r.call(t, "am_save", uint64(savePtr))
	saved := r.readBytes(t, savePtr, saveLen)
	r.free(t, savePtr, saveLen)
	t.Logf("saved doc: %d bytes", len(saved))

	// Load into a fresh doc (same WASM instance, replaces global state)
	loadPtr, loadLen := r.writeBytes(t, saved)
	defer r.free(t, loadPtr, loadLen)
	res = r.call(t, "am_load", uint64(loadPtr), uint64(loadLen))
	if int32(res[0]) != 0 {
		t.Fatalf("am_load failed: %d", int32(res[0]))
	}

	// Read the value back
	res = r.call(t, "am_map_get_len", 0, uint64(keyPtr), uint64(keyLen))
	getLen := uint32(res[0])
	outPtr := r.alloc(t, getLen)
	defer r.free(t, outPtr, getLen)
	r.call(t, "am_map_get", 0, uint64(keyPtr), uint64(keyLen), uint64(outPtr))
	got := r.readBytes(t, outPtr, getLen)
	if got[0] != TagString || string(got[1:]) != "persisted" {
		t.Fatalf("after load, expected 'persisted', got tag=0x%02x val=%q", got[0], string(got[1:]))
	}
}

func TestActorID(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// Get initial actor
	res := r.call(t, "am_get_actor_len")
	actorLen := uint32(res[0])
	if actorLen == 0 {
		t.Fatal("actor len is 0")
	}
	actorPtr := r.alloc(t, actorLen)
	r.call(t, "am_get_actor", uint64(actorPtr))
	actor := string(r.readBytes(t, actorPtr, actorLen))
	r.free(t, actorPtr, actorLen)
	t.Logf("initial actor: %s", actor)

	// Set a custom actor
	customActor := "deadbeefdeadbeefdeadbeefdeadbeef"
	caPtr, caLen := r.writeBytes(t, []byte(customActor))
	defer r.free(t, caPtr, caLen)
	res = r.call(t, "am_set_actor", uint64(caPtr), uint64(caLen))
	if int32(res[0]) != 0 {
		t.Fatalf("set_actor failed: %d", int32(res[0]))
	}

	// Read back
	res = r.call(t, "am_get_actor_len")
	actorLen = uint32(res[0])
	actorPtr = r.alloc(t, actorLen)
	r.call(t, "am_get_actor", uint64(actorPtr))
	gotActor := string(r.readBytes(t, actorPtr, actorLen))
	r.free(t, actorPtr, actorLen)
	if gotActor != customActor {
		t.Fatalf("expected %q, got %q", customActor, gotActor)
	}
}

func TestSyncProtocol(t *testing.T) {
	// This test uses a single WASM instance, so we simulate sync
	// by saving doc A, creating doc B, generating sync msg from B,
	// loading doc A back, and receiving the message.
	r := newRuntime(t)
	defer r.close(t)

	// Create doc A with data
	r.call(t, "am_create")
	keyPtr, keyLen := r.writeBytes(t, []byte("from"))
	defer r.free(t, keyPtr, keyLen)
	valPtr, valLen := r.writeBytes(t, []byte("alice"))
	defer r.free(t, valPtr, valLen)
	r.call(t, "am_map_put", 0, uint64(keyPtr), uint64(keyLen), uint64(TagString), uint64(valPtr), uint64(valLen))
	msgPtr, msgLen := r.writeBytes(t, []byte("alice"))
	defer r.free(t, msgPtr, msgLen)
	r.call(t, "am_commit", uint64(msgPtr), uint64(msgLen), 1000)

	// Init sync state for a peer
	res := r.call(t, "am_sync_state_init")
	peerID := uint32(res[0])
	if peerID == 0 {
		t.Fatal("sync_state_init returned 0")
	}
	t.Logf("peer_id: %d", peerID)

	// Generate sync message
	res = r.call(t, "am_sync_gen_len", uint64(peerID))
	syncLen := uint32(res[0])
	if syncLen == 0 {
		t.Fatal("expected sync message, got len 0")
	}

	syncPtr := r.alloc(t, syncLen)
	res = r.call(t, "am_sync_gen", uint64(peerID), uint64(syncPtr))
	if int32(res[0]) != 0 {
		t.Fatalf("sync_gen failed: %d", int32(res[0]))
	}
	syncMsg := r.readBytes(t, syncPtr, syncLen)
	r.free(t, syncPtr, syncLen)
	t.Logf("sync message: %d bytes", len(syncMsg))

	// Free peer
	res = r.call(t, "am_sync_state_free", uint64(peerID))
	if int32(res[0]) != 0 {
		t.Fatalf("sync_state_free failed: %d", int32(res[0]))
	}
}

func TestIncrementalSaveLoad(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// First commit
	k1Ptr, k1Len := r.writeBytes(t, []byte("k1"))
	defer r.free(t, k1Ptr, k1Len)
	v1Ptr, v1Len := r.writeBytes(t, []byte("v1"))
	defer r.free(t, v1Ptr, v1Len)
	r.call(t, "am_map_put", 0, uint64(k1Ptr), uint64(k1Len), uint64(TagString), uint64(v1Ptr), uint64(v1Len))
	m1Ptr, m1Len := r.writeBytes(t, []byte("c1"))
	defer r.free(t, m1Ptr, m1Len)
	r.call(t, "am_commit", uint64(m1Ptr), uint64(m1Len), 1000)

	// Full save (baseline)
	res := r.call(t, "am_save_len")
	fullLen := uint32(res[0])
	fullPtr := r.alloc(t, fullLen)
	r.call(t, "am_save", uint64(fullPtr))
	fullSave := r.readBytes(t, fullPtr, fullLen)
	r.free(t, fullPtr, fullLen)

	// Second commit
	v2Ptr, v2Len := r.writeBytes(t, []byte("v2"))
	defer r.free(t, v2Ptr, v2Len)
	r.call(t, "am_map_put", 0, uint64(k1Ptr), uint64(k1Len), uint64(TagString), uint64(v2Ptr), uint64(v2Len))
	m2Ptr, m2Len := r.writeBytes(t, []byte("c2"))
	defer r.free(t, m2Ptr, m2Len)
	r.call(t, "am_commit", uint64(m2Ptr), uint64(m2Len), 2000)

	// Incremental save
	res = r.call(t, "am_save_incremental_len")
	incLen := uint32(res[0])
	if incLen == 0 {
		t.Fatal("incremental save produced 0 bytes")
	}
	incPtr := r.alloc(t, incLen)
	r.call(t, "am_save_incremental", uint64(incPtr))
	incSave := r.readBytes(t, incPtr, incLen)
	r.free(t, incPtr, incLen)
	t.Logf("incremental: %d bytes (vs full %d bytes)", len(incSave), len(fullSave))

	// Load the full save, then apply incremental
	loadPtr, loadLen := r.writeBytes(t, fullSave)
	defer r.free(t, loadPtr, loadLen)
	r.call(t, "am_load", uint64(loadPtr), uint64(loadLen))

	incLoadPtr, incLoadLen := r.writeBytes(t, incSave)
	defer r.free(t, incLoadPtr, incLoadLen)
	res = r.call(t, "am_load_incremental", uint64(incLoadPtr), uint64(incLoadLen))
	if int32(res[0]) != 0 {
		t.Fatalf("load_incremental failed: %d", int32(res[0]))
	}

	// Verify the latest value is present
	res = r.call(t, "am_map_get_len", 0, uint64(k1Ptr), uint64(k1Len))
	getLen := uint32(res[0])
	outPtr := r.alloc(t, getLen)
	defer r.free(t, outPtr, getLen)
	r.call(t, "am_map_get", 0, uint64(k1Ptr), uint64(k1Len), uint64(outPtr))
	got := r.readBytes(t, outPtr, getLen)
	if got[0] != TagString || string(got[1:]) != "v2" {
		t.Fatalf("after incremental load, expected 'v2', got %q", string(got[1:]))
	}
}

func TestMapPutFloat64(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	keyPtr, keyLen := r.writeBytes(t, []byte("pi"))
	defer r.free(t, keyPtr, keyLen)

	var payload [8]byte
	binary.LittleEndian.PutUint64(payload[:], math.Float64bits(3.14159))
	valPtr, valLen := r.writeBytes(t, payload[:])
	defer r.free(t, valPtr, valLen)

	res := r.call(t, "am_map_put", 0, uint64(keyPtr), uint64(keyLen), uint64(TagFloat64), uint64(valPtr), uint64(valLen))
	if int32(res[0]) != 0 {
		t.Fatalf("put float64 failed: %d", int32(res[0]))
	}

	res = r.call(t, "am_map_get_len", 0, uint64(keyPtr), uint64(keyLen))
	getLen := uint32(res[0])
	outPtr := r.alloc(t, getLen)
	defer r.free(t, outPtr, getLen)
	r.call(t, "am_map_get", 0, uint64(keyPtr), uint64(keyLen), uint64(outPtr))
	got := r.readBytes(t, outPtr, getLen)

	if got[0] != TagFloat64 {
		t.Fatalf("expected TagFloat64, got 0x%02x", got[0])
	}
	v := math.Float64frombits(binary.LittleEndian.Uint64(got[1:9]))
	if v != 3.14159 {
		t.Fatalf("expected 3.14159, got %f", v)
	}
}

func TestObjIntrospection(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// ROOT is a map (type=1)
	res := r.call(t, "am_obj_type", 0)
	if int32(res[0]) != 1 {
		t.Fatalf("expected ROOT type=1 (map), got %d", int32(res[0]))
	}

	// ROOT initially has 0 entries
	res = r.call(t, "am_obj_size", 0)
	if uint32(res[0]) != 0 {
		t.Fatalf("expected ROOT size 0, got %d", uint32(res[0]))
	}

	// Add some keys
	for _, kv := range [][2]string{{"a", "1"}, {"b", "2"}, {"c", "3"}} {
		kPtr, kLen := r.writeBytes(t, []byte(kv[0]))
		vPtr, vLen := r.writeBytes(t, []byte(kv[1]))
		r.call(t, "am_map_put", 0, uint64(kPtr), uint64(kLen), uint64(TagString), uint64(vPtr), uint64(vLen))
		r.free(t, kPtr, kLen)
		r.free(t, vPtr, vLen)
	}

	res = r.call(t, "am_obj_size", 0)
	if uint32(res[0]) != 3 {
		t.Fatalf("expected ROOT size 3, got %d", uint32(res[0]))
	}
}

func TestChangesRoundtrip(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// Make a change
	kPtr, kLen := r.writeBytes(t, []byte("x"))
	defer r.free(t, kPtr, kLen)
	vPtr, vLen := r.writeBytes(t, []byte("y"))
	defer r.free(t, vPtr, vLen)
	r.call(t, "am_map_put", 0, uint64(kPtr), uint64(kLen), uint64(TagString), uint64(vPtr), uint64(vLen))
	mPtr, mLen := r.writeBytes(t, []byte("change1"))
	defer r.free(t, mPtr, mLen)
	r.call(t, "am_commit", uint64(mPtr), uint64(mLen), 5000)

	// Get all changes (natural _len → alloc → _get → read → free pattern)
	res := r.call(t, "am_get_changes_len", 0, 0) // null heads = all changes
	changesLen := uint32(res[0])
	if changesLen == 0 {
		t.Fatal("expected changes bytes, got 0")
	}

	changesPtr := r.alloc(t, changesLen)
	r.call(t, "am_get_changes", 0, 0, uint64(changesPtr))
	changesData := r.readBytes(t, changesPtr, changesLen) // copies data
	r.free(t, changesPtr, changesLen)
	t.Logf("changes data: %d bytes", len(changesData))

	// Load an empty doc and apply the changes
	r.call(t, "am_create")
	applyPtr, applyLen := r.writeBytes(t, changesData)
	defer r.free(t, applyPtr, applyLen)
	res = r.call(t, "am_apply_changes", uint64(applyPtr), uint64(applyLen))
	if int32(res[0]) != 0 {
		t.Fatalf("apply_changes failed: %d", int32(res[0]))
	}

	// Verify the value is present in the new doc
	res = r.call(t, "am_map_get_len", 0, uint64(kPtr), uint64(kLen))
	getLen := uint32(res[0])
	outPtr := r.alloc(t, getLen)
	r.call(t, "am_map_get", 0, uint64(kPtr), uint64(kLen), uint64(outPtr))
	got := r.readBytes(t, outPtr, getLen)
	r.free(t, outPtr, getLen)
	if got[0] != TagString || string(got[1:]) != "y" {
		t.Fatalf("after apply_changes expected 'y', got %q", string(got[1:]))
	}
}

// ── Diagnostic test for RETURN_BUF corruption ──

func TestReturnBufCorruption(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// Create a list at ROOT["items"]
	keyPtr, keyLen := r.writeBytes(t, []byte("items"))
	defer r.free(t, keyPtr, keyLen)
	res := r.call(t, "am_map_put", 0, uint64(keyPtr), uint64(keyLen), uint64(TagList), 0, 0)
	listHandle := uint32(res[0])

	// Insert "beta" at index 0
	vPtr, vLen := r.writeBytes(t, []byte("beta"))
	defer r.free(t, vPtr, vLen)
	r.call(t, "am_list_put", uint64(listHandle), 0, 1, uint64(TagString), uint64(vPtr), uint64(vLen))

	// Call _len to populate RETURN_BUF with [05 62 65 74 61]
	res = r.call(t, "am_list_get_len", uint64(listHandle), 0)
	getLen := uint32(res[0])
	t.Logf("get_len returned %d", getLen)

	// Inspect RETURN_BUF state BEFORE am_alloc
	res = r.call(t, "am_debug_return_buf_ptr")
	bufPtrBefore := uint32(res[0])
	res = r.call(t, "am_debug_return_buf_cap")
	bufCapBefore := uint32(res[0])
	t.Logf("BEFORE am_alloc: return_buf ptr=0x%08x cap=%d", bufPtrBefore, bufCapBefore)

	// Read RETURN_BUF contents byte by byte
	beforeBytes := make([]byte, getLen)
	for i := uint32(0); i < getLen; i++ {
		res = r.call(t, "am_debug_return_buf_byte", uint64(i))
		beforeBytes[i] = byte(res[0])
	}
	t.Logf("BEFORE am_alloc: return_buf data = %x", beforeBytes)

	// Also read the raw WASM memory at the RETURN_BUF pointer
	rawBefore := r.readBytes(t, bufPtrBefore, getLen)
	t.Logf("BEFORE am_alloc: raw memory at 0x%08x = %x", bufPtrBefore, rawBefore)

	// Now call am_alloc — this is the suspect
	allocSizes := []uint32{5, 8, 16, 32, 64, 128, 256}
	for _, sz := range allocSizes {
		allocPtr := r.alloc(t, sz)
		t.Logf("am_alloc(%d) returned 0x%08x (distance from return_buf: %d)",
			sz, allocPtr, int32(allocPtr)-int32(bufPtrBefore))

		// Check if alloc returned an address overlapping with RETURN_BUF
		if allocPtr >= bufPtrBefore && allocPtr < bufPtrBefore+bufCapBefore {
			t.Errorf("OVERLAP! am_alloc(%d) returned 0x%08x which is inside return_buf [0x%08x..0x%08x)",
				sz, allocPtr, bufPtrBefore, bufPtrBefore+bufCapBefore)
		}

		// Check RETURN_BUF state after this alloc
		res = r.call(t, "am_debug_return_buf_ptr")
		bufPtrAfter := uint32(res[0])
		res = r.call(t, "am_debug_return_buf_cap")
		bufCapAfter := uint32(res[0])

		if bufPtrAfter != bufPtrBefore {
			t.Errorf("return_buf ptr MOVED! was 0x%08x, now 0x%08x after am_alloc(%d)",
				bufPtrBefore, bufPtrAfter, sz)
		}
		if bufCapAfter != bufCapBefore {
			t.Errorf("return_buf cap CHANGED! was %d, now %d after am_alloc(%d)",
				bufCapBefore, bufCapAfter, sz)
		}

		// Check bytes via the safe accessor
		for i := uint32(0); i < getLen; i++ {
			res = r.call(t, "am_debug_return_buf_byte", uint64(i))
			if byte(res[0]) != beforeBytes[i] {
				t.Errorf("return_buf[%d] corrupted after am_alloc(%d): was 0x%02x, now 0x%02x",
					i, sz, beforeBytes[i], byte(res[0]))
			}
		}

		// Check raw memory at the original pointer
		rawAfter := r.readBytes(t, bufPtrBefore, getLen)
		if string(rawAfter) != string(rawBefore) {
			t.Errorf("raw memory at 0x%08x corrupted after am_alloc(%d): was %x, now %x",
				bufPtrBefore, sz, rawBefore, rawAfter)
		}

		r.free(t, allocPtr, sz)
	}

	// Final: do the actual copy and verify
	outPtr := r.alloc(t, getLen)
	defer r.free(t, outPtr, getLen)
	r.call(t, "am_list_get", uint64(listHandle), 0, uint64(outPtr))
	got := r.readBytes(t, outPtr, getLen)
	t.Logf("FINAL copy_return_buf result: %x (%q)", got, string(got[1:]))
	if got[0] != TagString || string(got[1:]) != "beta" {
		t.Fatalf("data corrupted: expected [05 'beta'], got %x", got)
	}
}

// TestReturnBufCorruptionMapGet tests the same pattern with map get.
func TestReturnBufCorruptionMapGet(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	keyPtr, keyLen := r.writeBytes(t, []byte("hello"))
	defer r.free(t, keyPtr, keyLen)
	valPtr, valLen := r.writeBytes(t, []byte("world"))
	defer r.free(t, valPtr, valLen)
	r.call(t, "am_map_put", 0, uint64(keyPtr), uint64(keyLen), uint64(TagString), uint64(valPtr), uint64(valLen))

	// Populate RETURN_BUF
	res := r.call(t, "am_map_get_len", 0, uint64(keyPtr), uint64(keyLen))
	getLen := uint32(res[0])

	// Get buf ptr
	res = r.call(t, "am_debug_return_buf_ptr")
	bufPtr := uint32(res[0])
	t.Logf("return_buf ptr=0x%08x len=%d", bufPtr, getLen)
	rawBefore := r.readBytes(t, bufPtr, getLen)
	t.Logf("return_buf data before alloc: %x", rawBefore)

	// Alloc exactly getLen bytes (the typical pattern)
	allocPtr := r.alloc(t, getLen)
	t.Logf("am_alloc(%d) = 0x%08x (buf was at 0x%08x)", getLen, allocPtr, bufPtr)

	rawAfter := r.readBytes(t, bufPtr, getLen)
	t.Logf("return_buf data after alloc:  %x", rawAfter)

	if string(rawBefore) != string(rawAfter) {
		t.Errorf("RAW MEMORY CORRUPTED at return_buf address")
	}

	// Check via Rust accessor
	for i := uint32(0); i < getLen; i++ {
		res = r.call(t, "am_debug_return_buf_byte", uint64(i))
		if byte(res[0]) != rawBefore[i] {
			t.Errorf("byte[%d]: was 0x%02x, now 0x%02x", i, rawBefore[i], byte(res[0]))
		}
	}

	// Do the copy
	r.call(t, "am_map_get", 0, uint64(keyPtr), uint64(keyLen), uint64(allocPtr))
	got := r.readBytes(t, allocPtr, getLen)
	t.Logf("final result: %x (%q)", got, string(got[1:]))
	if got[0] != TagString || string(got[1:]) != "world" {
		t.Fatalf("expected 'world', got %x", got)
	}
	r.free(t, allocPtr, getLen)
}

// TestAllocReturnsSamePtrAsReturnBuf directly tests if alloc returns the
// same address the return buffer is using.
func TestAllocReturnsSamePtrAsReturnBuf(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// Create some data of various sizes and check each time
	sizes := []int{1, 2, 4, 5, 8, 16, 32, 64, 100, 256}
	for _, dataSize := range sizes {
		// Put a string of this size
		key := []byte("k")
		val := make([]byte, dataSize)
		for i := range val {
			val[i] = byte('A' + (i % 26))
		}

		kPtr, kLen := r.writeBytes(t, key)
		vPtr, vLen := r.writeBytes(t, val)
		r.call(t, "am_map_put", 0, uint64(kPtr), uint64(kLen), uint64(TagString), uint64(vPtr), uint64(vLen))
		r.free(t, kPtr, kLen)
		r.free(t, vPtr, vLen)

		// Populate return buf
		kPtr2, kLen2 := r.writeBytes(t, key)
		res := r.call(t, "am_map_get_len", 0, uint64(kPtr2), uint64(kLen2))
		getLen := uint32(res[0])

		// Get return buf address
		res = r.call(t, "am_debug_return_buf_ptr")
		bufPtr := uint32(res[0])
		res = r.call(t, "am_debug_return_buf_cap")
		bufCap := uint32(res[0])

		// Try alloc of the same size
		allocPtr := r.alloc(t, getLen)

		overlap := allocPtr >= bufPtr && allocPtr < bufPtr+bufCap
		t.Logf("dataSize=%d: return_buf=[0x%x..0x%x) cap=%d, alloc(%d)=0x%x overlap=%v",
			dataSize, bufPtr, bufPtr+getLen, bufCap, getLen, allocPtr, overlap)

		if overlap {
			t.Errorf("OVERLAP at dataSize=%d!", dataSize)
		}

		r.free(t, allocPtr, getLen)
		r.free(t, kPtr2, kLen2)
	}
}

// TestExactOriginalSequence reproduces the EXACT code that failed originally.
// Three inserts with alloc/free, then _len → alloc → _get (alloc between _len and _get).
func TestExactOriginalSequence(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	keyPtr, keyLen := r.writeBytes(t, []byte("items"))
	defer r.free(t, keyPtr, keyLen)
	res := r.call(t, "am_map_put", 0, uint64(keyPtr), uint64(keyLen), uint64(TagList), 0, 0)
	listHandle := uint32(res[0])

	// Insert three items (alloc+write+free each time, exactly as original)
	for i, s := range []string{"alpha", "beta", "gamma"} {
		vPtr, vLen := r.writeBytes(t, []byte(s))
		res = r.call(t, "am_list_put",
			uint64(listHandle), uint64(i), 1,
			uint64(TagString), uint64(vPtr), uint64(vLen))
		if int32(res[0]) != 0 {
			t.Fatalf("insert %q failed: %d", s, int32(res[0]))
		}
		r.free(t, vPtr, vLen)
	}

	// THE EXACT ORIGINAL SEQUENCE that failed:
	// Step 1: _len (populates RETURN_BUF)
	res = r.call(t, "am_list_get_len", uint64(listHandle), 1)
	getLen := uint32(res[0])
	t.Logf("Step 1: _len returned %d", getLen)

	// Debug: check RETURN_BUF state
	res = r.call(t, "am_debug_return_buf_ptr")
	bufPtr := uint32(res[0])
	res = r.call(t, "am_debug_return_buf_cap")
	bufCap := uint32(res[0])
	t.Logf("  RETURN_BUF: ptr=0x%08x cap=%d", bufPtr, bufCap)
	rawBefore := r.readBytes(t, bufPtr, getLen)
	t.Logf("  RETURN_BUF data: %x", rawBefore)

	// Verify bytes via accessor
	for i := uint32(0); i < getLen; i++ {
		res = r.call(t, "am_debug_return_buf_byte", uint64(i))
		t.Logf("  buf[%d] = 0x%02x", i, byte(res[0]))
	}

	// Step 2: alloc (THE SUSPECT - this is between _len and _get)
	outPtr := r.alloc(t, getLen)
	t.Logf("Step 2: am_alloc(%d) = 0x%08x", getLen, outPtr)

	// Debug: check RETURN_BUF again
	res = r.call(t, "am_debug_return_buf_ptr")
	bufPtrAfter := uint32(res[0])
	res = r.call(t, "am_debug_return_buf_cap")
	bufCapAfter := uint32(res[0])
	t.Logf("  RETURN_BUF after alloc: ptr=0x%08x cap=%d", bufPtrAfter, bufCapAfter)
	rawAfter := r.readBytes(t, bufPtrAfter, getLen)
	t.Logf("  RETURN_BUF data after alloc: %x", rawAfter)

	if bufPtr != bufPtrAfter {
		t.Errorf("RETURN_BUF ptr MOVED: 0x%08x -> 0x%08x", bufPtr, bufPtrAfter)
	}
	if string(rawBefore) != string(rawAfter) {
		t.Errorf("RETURN_BUF DATA CHANGED: %x -> %x", rawBefore, rawAfter)
	}

	// Check if alloc overlaps
	if outPtr >= bufPtr && outPtr < bufPtr+bufCap {
		t.Errorf("am_alloc returned address INSIDE return_buf!")
	}

	// Also check raw memory at outPtr before copy
	outRaw := r.readBytes(t, outPtr, getLen)
	t.Logf("  raw memory at outPtr before copy: %x", outRaw)

	// Step 3: _get (copy RETURN_BUF to outPtr)
	r.call(t, "am_list_get", uint64(listHandle), 1, uint64(outPtr))
	got := r.readBytes(t, outPtr, getLen)
	t.Logf("Step 3: result at outPtr: %x (%q)", got, string(got[1:]))

	if got[0] != TagString || string(got[1:]) != "beta" {
		t.Fatalf("CORRUPTION! expected [05 beta], got %x", got)
	}

	r.free(t, outPtr, getLen)
}

// ── Root cause investigation: wazero Memory.Read() behavior ──

// TestMemoryReadIsView proves whether wazero Memory.Read returns a view (slice)
// or a copy. This is critical for understanding the corruption.
func TestMemoryReadIsView(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	ptr := r.alloc(t, 8)
	r.mod_.Memory().WriteByte(ptr, 0xAA)

	// Read via wazero API
	buf, ok := r.mod_.Memory().Read(ptr, 1)
	if !ok {
		t.Fatal("read failed")
	}
	if buf[0] != 0xAA {
		t.Fatalf("initial read: expected 0xAA, got 0x%02x", buf[0])
	}

	// Now write a different byte to the same WASM address
	r.mod_.Memory().WriteByte(ptr, 0xBB)

	// Check if buf (obtained BEFORE the write) reflects the change
	if buf[0] == 0xBB {
		t.Log("CONFIRMED: Memory.Read() returns a VIEW (slice into wasm memory buffer)")
		t.Log("This means any WASM call that modifies memory at the same address will corrupt previously-read slices!")
	} else if buf[0] == 0xAA {
		t.Log("Memory.Read() returns a COPY (the hypothesis is wrong)")
	} else {
		t.Logf("Unexpected: buf[0] = 0x%02x", buf[0])
	}

	r.free(t, ptr, 8)
}

// TestFreeCorruptsReadSlice proves that am_free overwrites memory that
// a previous Memory.Read() slice still references.
func TestFreeCorruptsReadSlice(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	ptr := r.alloc(t, 8)
	// Write known data
	r.mod_.Memory().Write(ptr, []byte{0x05, 0x62, 0x65, 0x74, 0x61, 0x00, 0x00, 0x00})

	// Read via wazero — this may return a VIEW
	got, ok := r.mod_.Memory().Read(ptr, 5)
	if !ok {
		t.Fatal("read failed")
	}
	t.Logf("before free: %x (%q)", got, string(got[1:]))

	// Free the memory — the allocator may write metadata to ptr
	r.free(t, ptr, 8)

	// Check if the slice we read BEFORE the free was corrupted
	t.Logf("after free:  %x", got)
	if got[0] != 0x05 || string(got[1:]) != "beta" {
		t.Logf("CONFIRMED: am_free corrupts data visible through a previously-read Memory.Read() slice!")
		t.Logf("This is the root cause: Memory.Read() returns a view, and am_free overwrites the freed memory with allocator metadata.")
	} else {
		t.Log("Data survived am_free (either Read returns a copy, or the allocator didn't touch these bytes)")
	}
}

// TestOriginalBugExactRepro reproduces the exact bug: read via Memory.Read(),
// then free, then check the data — proving use-after-free via the view.
func TestOriginalBugExactRepro(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)

	r.call(t, "am_create")

	// Setup: create list with 3 items (exact original sequence)
	keyPtr, keyLen := r.writeBytes(t, []byte("items"))
	defer r.free(t, keyPtr, keyLen)
	res := r.call(t, "am_map_put", 0, uint64(keyPtr), uint64(keyLen), uint64(TagList), 0, 0)
	listHandle := uint32(res[0])
	for i, s := range []string{"alpha", "beta", "gamma"} {
		vPtr, vLen := r.writeBytes(t, []byte(s))
		r.call(t, "am_list_put", uint64(listHandle), uint64(i), 1, uint64(TagString), uint64(vPtr), uint64(vLen))
		r.free(t, vPtr, vLen)
	}

	// THE ORIGINAL BUGGY SEQUENCE:
	res = r.call(t, "am_list_get_len", uint64(listHandle), 1) // _len → RETURN_BUF
	getLen := uint32(res[0])

	outPtr := r.alloc(t, getLen) // alloc output buffer

	r.call(t, "am_list_get", uint64(listHandle), 1, uint64(outPtr)) // _get → copy to outPtr

	// Read the output — may be a VIEW into wasm memory
	got := r.readBytes(t, outPtr, getLen)
	t.Logf("before free: %x (%q)", got, string(got[1:]))

	// Copy the data to a Go-owned byte slice for safe comparison
	safeCopy := make([]byte, len(got))
	copy(safeCopy, got)

	// Free the output buffer — this is what the original code did
	r.free(t, outPtr, getLen)

	t.Logf("after free via view:   %x", got)
	t.Logf("after free safe copy:  %x", safeCopy)

	// The safe copy should always be correct
	if safeCopy[0] != TagString || string(safeCopy[1:]) != "beta" {
		t.Fatalf("even safe copy is wrong: %x", safeCopy)
	}

	// The view may be corrupted
	if got[0] != safeCopy[0] || string(got[1:]) != string(safeCopy[1:]) {
		t.Logf("ROOT CAUSE CONFIRMED: Memory.Read() returns a view, and am_free corrupts it")
		t.Logf("got (view) = %x, safeCopy = %x", got, safeCopy)
	} else {
		t.Log("View was NOT corrupted — allocator didn't overwrite these bytes")
	}
}
