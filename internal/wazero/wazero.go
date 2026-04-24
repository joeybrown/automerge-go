package wazero

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"

	_ "embed"

	"github.com/joeybrown/automerge-go/internal/backend"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed automerge.wasm
var wasmBytes []byte

var (
	wasmRuntime  wazero.Runtime
	wasmCompiled wazero.CompiledModule
	wasmOnce     sync.Once
	wasmInitErr  error
)

var instanceCounter atomic.Uint64

func ensureWasmCompiled() error {
	wasmOnce.Do(func() {
		ctx := context.Background()
		wasmRuntime = wazero.NewRuntime(ctx)
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, wasmRuntime); err != nil {
			wasmInitErr = fmt.Errorf("automerge: failed to instantiate WASI: %w", err)
			return
		}
		compiled, err := wasmRuntime.CompileModule(ctx, wasmBytes)
		if err != nil {
			wasmInitErr = fmt.Errorf("automerge: failed to compile WASM module: %w", err)
			return
		}
		wasmCompiled = compiled
	})
	return wasmInitErr
}

// Backend implements the backend.Backend interface using a wazero WASM module instance.
type Backend struct {
	mod api.Module
}

func newBackendInstance(ctx context.Context) (*Backend, error) {
	if err := ensureWasmCompiled(); err != nil {
		return nil, err
	}
	n := instanceCounter.Add(1)
	name := fmt.Sprintf("automerge-%d", n)
	config := wazero.NewModuleConfig().WithName(name).WithRandSource(rand.Reader)
	mod, err := wasmRuntime.InstantiateModule(ctx, wasmCompiled, config)
	if err != nil {
		return nil, fmt.Errorf("automerge: failed to instantiate module: %w", err)
	}
	return &Backend{mod: mod}, nil
}

// NewBackend creates a new wazero backend with an empty automerge document.
func NewBackend(ctx context.Context) (backend.Backend, error) {
	b, err := newBackendInstance(ctx)
	if err != nil {
		return nil, err
	}
	res, err := b.call(ctx, "am_create")
	if err != nil {
		b.Close(ctx)
		return nil, fmt.Errorf("automerge: am_create failed: %w", err)
	}
	if int32(res[0]) != 0 {
		b.Close(ctx)
		return nil, fmt.Errorf("automerge: am_create failed: %d", int32(res[0]))
	}
	return b, nil
}

// LoadBackend creates a new wazero backend by loading a serialized automerge document.
func LoadBackend(ctx context.Context, raw []byte) (backend.Backend, error) {
	b, err := newBackendInstance(ctx)
	if err != nil {
		return nil, err
	}
	ptr, size, err := b.writeBytes(ctx, raw)
	if err != nil {
		b.Close(ctx)
		return nil, err
	}
	defer b.free(ctx, ptr, size)

	res, err := b.call(ctx, "am_load", uint64(ptr), uint64(size))
	if err != nil {
		b.Close(ctx)
		return nil, err
	}
	if int32(res[0]) != 0 {
		b.Close(ctx)
		return nil, fmt.Errorf("automerge: failed to load document")
	}
	return b, nil
}

// ParseRawChanges creates a temporary backend to parse concatenated raw change bytes,
// returning the individual raw changes and their metadata.
func ParseRawChanges(ctx context.Context, raw []byte) ([][]byte, []*backend.ChangeInfo, error) {
	nb, err := newBackendInstance(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer nb.Close(ctx)

	ptr, size, err := nb.writeBytes(ctx, raw)
	if err != nil {
		return nil, nil, err
	}
	defer nb.free(ctx, ptr, size)

	res, err := nb.call(ctx, "am_load", uint64(ptr), uint64(size))
	if err != nil {
		return nil, nil, err
	}
	if int32(res[0]) != 0 {
		return nil, nil, fmt.Errorf("unable to parse changes")
	}

	rawChanges, err := nb.GetChanges(ctx, nil)
	if err != nil {
		return nil, nil, err
	}

	infos := make([]*backend.ChangeInfo, len(rawChanges))
	for i, rc := range rawChanges {
		info, err := nb.GetChangeInfo(ctx, rc)
		if err != nil {
			return nil, nil, err
		}
		infos[i] = info
	}
	return rawChanges, infos, nil
}

// --- low-level WASM helpers ---

func (b *Backend) call(ctx context.Context, name string, params ...uint64) ([]uint64, error) {
	fn := b.mod.ExportedFunction(name)
	if fn == nil {
		return nil, fmt.Errorf("automerge: WASM export %q not found", name)
	}
	return fn.Call(ctx, params...)
}

func (b *Backend) lastError(ctx context.Context) string {
	res, err := b.call(ctx, "am_last_error_len")
	if err != nil {
		return ""
	}
	errLen := uint32(res[0])
	if errLen == 0 {
		return ""
	}
	ptr, err := b.alloc(ctx, errLen)
	if err != nil {
		return ""
	}
	defer b.free(ctx, ptr, errLen)
	_, err = b.call(ctx, "am_last_error", uint64(ptr))
	if err != nil {
		return ""
	}
	data, err := b.readBytes(ptr, errLen)
	if err != nil {
		return ""
	}
	return string(data)
}

func (b *Backend) alloc(ctx context.Context, n uint32) (uint32, error) {
	res, err := b.call(ctx, "am_alloc", uint64(n))
	if err != nil {
		return 0, err
	}
	ptr := uint32(res[0])
	if ptr == 0 && n > 0 {
		return 0, fmt.Errorf("automerge: am_alloc(%d) returned null", n)
	}
	return ptr, nil
}

func (b *Backend) free(ctx context.Context, ptr, size uint32) {
	if ptr != 0 && size != 0 {
		b.call(ctx, "am_free", uint64(ptr), uint64(size))
	}
}

func (b *Backend) writeBytes(ctx context.Context, data []byte) (ptr, size uint32, err error) {
	size = uint32(len(data))
	if size == 0 {
		return 0, 0, nil
	}
	ptr, err = b.alloc(ctx, size)
	if err != nil {
		return 0, 0, err
	}
	if !b.mod.Memory().Write(ptr, data) {
		b.free(ctx, ptr, size)
		return 0, 0, fmt.Errorf("automerge: memory write failed at ptr=%d size=%d", ptr, size)
	}
	return ptr, size, nil
}

func (b *Backend) readBytes(ptr, size uint32) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	view, ok := b.mod.Memory().Read(ptr, size)
	if !ok {
		return nil, fmt.Errorf("automerge: memory read failed at ptr=%d size=%d", ptr, size)
	}
	out := make([]byte, len(view))
	copy(out, view)
	return out, nil
}

// --- backend.Backend interface implementation ---

func (b *Backend) Close(ctx context.Context) error {
	return b.mod.Close(ctx)
}

func (b *Backend) Save(ctx context.Context) ([]byte, error) {
	res, err := b.call(ctx, "am_save_len")
	if err != nil {
		return nil, err
	}
	saveLen := uint32(res[0])
	if saveLen == 0 {
		return nil, fmt.Errorf("automerge: am_save_len returned 0")
	}
	ptr, err := b.alloc(ctx, saveLen)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, ptr, saveLen)
	res, err = b.call(ctx, "am_save", uint64(ptr))
	if err != nil {
		return nil, err
	}
	if int32(res[0]) != 0 {
		return nil, fmt.Errorf("automerge: am_save failed: %d", int32(res[0]))
	}
	return b.readBytes(ptr, saveLen)
}

func (b *Backend) GetActorID(ctx context.Context) (string, error) {
	res, err := b.call(ctx, "am_get_actor_len")
	if err != nil {
		return "", err
	}
	actLen := uint32(res[0])
	if actLen == 0 {
		return "", nil
	}
	ptr, err := b.alloc(ctx, actLen)
	if err != nil {
		return "", err
	}
	defer b.free(ctx, ptr, actLen)
	res, err = b.call(ctx, "am_get_actor", uint64(ptr))
	if err != nil {
		return "", err
	}
	if int32(res[0]) != 0 {
		return "", fmt.Errorf("automerge: am_get_actor failed: %d", int32(res[0]))
	}
	data, err := b.readBytes(ptr, actLen)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (b *Backend) SetActorID(ctx context.Context, id string) error {
	ptr, size, err := b.writeBytes(ctx, []byte(id))
	if err != nil {
		return err
	}
	defer b.free(ctx, ptr, size)
	res, err := b.call(ctx, "am_set_actor", uint64(ptr), uint64(size))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_set_actor failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) MapGet(ctx context.Context, obj backend.ObjHandle, key string) (*backend.BackendValue, error) {
	keyPtr, keySize, err := b.writeBytes(ctx, []byte(key))
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, keyPtr, keySize)

	res, err := b.call(ctx, "am_map_get_len", uint64(obj), uint64(keyPtr), uint64(keySize))
	if err != nil {
		return nil, err
	}
	getLen := uint32(res[0])
	if getLen == 0 {
		return &backend.BackendValue{Kind: backend.KindVoid}, nil
	}

	outPtr, err := b.alloc(ctx, getLen)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, outPtr, getLen)
	_, err = b.call(ctx, "am_map_get", uint64(obj), uint64(keyPtr), uint64(keySize), uint64(outPtr))
	if err != nil {
		return nil, err
	}
	data, err := b.readBytes(outPtr, getLen)
	if err != nil {
		return nil, err
	}
	return backend.DecodeValue(data)
}

func (b *Backend) MapPut(ctx context.Context, obj backend.ObjHandle, key string, tag byte, payload []byte) (backend.ObjHandle, error) {
	keyPtr, keySize, err := b.writeBytes(ctx, []byte(key))
	if err != nil {
		return 0, err
	}
	defer b.free(ctx, keyPtr, keySize)

	valPtr, valSize, err := b.writeBytes(ctx, payload)
	if err != nil {
		return 0, err
	}
	defer b.free(ctx, valPtr, valSize)

	res, err := b.call(ctx, "am_map_put",
		uint64(obj),
		uint64(keyPtr), uint64(keySize),
		uint64(tag),
		uint64(valPtr), uint64(valSize),
	)
	if err != nil {
		return 0, err
	}
	rc := int32(res[0])
	if rc < 0 {
		return 0, fmt.Errorf("automerge: am_map_put failed: %d", rc)
	}
	if tag == backend.TagMap || tag == backend.TagList || tag == backend.TagText {
		return backend.ObjHandle(rc), nil
	}
	return 0, nil
}

func (b *Backend) MapDelete(ctx context.Context, obj backend.ObjHandle, key string) error {
	keyPtr, keySize, err := b.writeBytes(ctx, []byte(key))
	if err != nil {
		return err
	}
	defer b.free(ctx, keyPtr, keySize)

	res, err := b.call(ctx, "am_map_delete", uint64(obj), uint64(keyPtr), uint64(keySize))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_map_delete failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) MapIncrement(ctx context.Context, obj backend.ObjHandle, key string, delta int64) error {
	keyPtr, keySize, err := b.writeBytes(ctx, []byte(key))
	if err != nil {
		return err
	}
	defer b.free(ctx, keyPtr, keySize)

	res, err := b.call(ctx, "am_map_increment", uint64(obj), uint64(keyPtr), uint64(keySize), uint64(delta))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_map_increment failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) ListGet(ctx context.Context, obj backend.ObjHandle, index uint) (*backend.BackendValue, error) {
	res, err := b.call(ctx, "am_list_get_len", uint64(obj), uint64(index))
	if err != nil {
		return nil, err
	}
	getLen := uint32(res[0])
	if getLen == 0 {
		return &backend.BackendValue{Kind: backend.KindVoid}, nil
	}

	outPtr, err := b.alloc(ctx, getLen)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, outPtr, getLen)
	_, err = b.call(ctx, "am_list_get", uint64(obj), uint64(index), uint64(outPtr))
	if err != nil {
		return nil, err
	}
	data, err := b.readBytes(outPtr, getLen)
	if err != nil {
		return nil, err
	}
	return backend.DecodeValue(data)
}

func (b *Backend) ListPut(ctx context.Context, obj backend.ObjHandle, index uint, insert bool, tag byte, payload []byte) (backend.ObjHandle, error) {
	var insertFlag uint64
	if insert {
		insertFlag = 1
	}

	valPtr, valSize, err := b.writeBytes(ctx, payload)
	if err != nil {
		return 0, err
	}
	defer b.free(ctx, valPtr, valSize)

	res, err := b.call(ctx, "am_list_put",
		uint64(obj),
		uint64(index),
		insertFlag,
		uint64(tag),
		uint64(valPtr), uint64(valSize),
	)
	if err != nil {
		return 0, err
	}
	rc := int32(res[0])
	if rc < 0 {
		return 0, fmt.Errorf("automerge: am_list_put failed: %d", rc)
	}
	if tag == backend.TagMap || tag == backend.TagList || tag == backend.TagText {
		return backend.ObjHandle(rc), nil
	}
	return 0, nil
}

func (b *Backend) ListDelete(ctx context.Context, obj backend.ObjHandle, index uint) error {
	res, err := b.call(ctx, "am_list_delete", uint64(obj), uint64(index))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_list_delete failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) ListLen(ctx context.Context, obj backend.ObjHandle) (uint, error) {
	res, err := b.call(ctx, "am_list_len", uint64(obj))
	if err != nil {
		return 0, err
	}
	return uint(uint32(res[0])), nil
}

func (b *Backend) ListIncrement(ctx context.Context, obj backend.ObjHandle, index uint, delta int64) error {
	res, err := b.call(ctx, "am_list_increment", uint64(obj), uint64(index), uint64(delta))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_list_increment failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) TextGet(ctx context.Context, obj backend.ObjHandle) (string, error) {
	res, err := b.call(ctx, "am_text_get_len", uint64(obj))
	if err != nil {
		return "", err
	}
	getLen := uint32(res[0])
	if getLen == 0 {
		return "", nil
	}
	ptr, err := b.alloc(ctx, getLen)
	if err != nil {
		return "", err
	}
	defer b.free(ctx, ptr, getLen)
	_, err = b.call(ctx, "am_text_get", uint64(obj), uint64(ptr))
	if err != nil {
		return "", err
	}
	data, err := b.readBytes(ptr, getLen)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (b *Backend) TextSplice(ctx context.Context, obj backend.ObjHandle, pos uint, del int, text string) error {
	txtPtr, txtSize, err := b.writeBytes(ctx, []byte(text))
	if err != nil {
		return err
	}
	defer b.free(ctx, txtPtr, txtSize)

	res, err := b.call(ctx, "am_text_splice",
		uint64(obj),
		uint64(pos),
		uint64(del),
		uint64(txtPtr), uint64(txtSize),
	)
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		if errMsg := b.lastError(ctx); errMsg != "" {
			return fmt.Errorf("%s", errMsg)
		}
		return fmt.Errorf("automerge: am_text_splice failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) Mark(ctx context.Context, obj backend.ObjHandle, name string, value string, start, end uint, expand uint8) error {
	namePtr, nameSize, err := b.writeBytes(ctx, []byte(name))
	if err != nil {
		return err
	}
	defer b.free(ctx, namePtr, nameSize)

	valuePtr, valueSize, err := b.writeBytes(ctx, []byte(value))
	if err != nil {
		return err
	}
	defer b.free(ctx, valuePtr, valueSize)

	res, err := b.call(ctx, "am_mark",
		uint64(obj),
		uint64(namePtr), uint64(nameSize),
		uint64(valuePtr), uint64(valueSize),
		uint64(start), uint64(end),
		uint64(expand),
	)
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		if errMsg := b.lastError(ctx); errMsg != "" {
			return fmt.Errorf("%s", errMsg)
		}
		return fmt.Errorf("automerge: am_mark failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) Unmark(ctx context.Context, obj backend.ObjHandle, name string, start, end uint, expand uint8) error {
	namePtr, nameSize, err := b.writeBytes(ctx, []byte(name))
	if err != nil {
		return err
	}
	defer b.free(ctx, namePtr, nameSize)

	res, err := b.call(ctx, "am_unmark",
		uint64(obj),
		uint64(namePtr), uint64(nameSize),
		uint64(start), uint64(end),
		uint64(expand),
	)
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		if errMsg := b.lastError(ctx); errMsg != "" {
			return fmt.Errorf("%s", errMsg)
		}
		return fmt.Errorf("automerge: am_unmark failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) Marks(ctx context.Context, obj backend.ObjHandle) (string, error) {
	res, err := b.call(ctx, "am_marks_len", uint64(obj))
	if err != nil {
		return "", err
	}
	jsonLen := uint32(res[0])
	if jsonLen == 0 {
		return "[]", nil
	}
	ptr, err := b.alloc(ctx, jsonLen)
	if err != nil {
		return "", err
	}
	defer b.free(ctx, ptr, jsonLen)
	_, err = b.call(ctx, "am_marks", uint64(obj), uint64(ptr))
	if err != nil {
		return "", err
	}
	data, err := b.readBytes(ptr, jsonLen)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (b *Backend) GetCursor(ctx context.Context, obj backend.ObjHandle, index uint) (string, error) {
	res, err := b.call(ctx, "am_get_cursor", uint64(obj), uint64(index))
	if err != nil {
		return "", err
	}
	cursorLen := int32(res[0])
	if cursorLen < 0 {
		return "", fmt.Errorf("automerge: am_get_cursor failed: %d", cursorLen)
	}
	ptr, err := b.alloc(ctx, uint32(cursorLen))
	if err != nil {
		return "", err
	}
	defer b.free(ctx, ptr, uint32(cursorLen))
	_, err = b.call(ctx, "am_get_cursor_str", uint64(ptr))
	if err != nil {
		return "", err
	}
	data, err := b.readBytes(ptr, uint32(cursorLen))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (b *Backend) LookupCursor(ctx context.Context, obj backend.ObjHandle, cursor string) (uint, error) {
	cursorPtr, cursorSize, err := b.writeBytes(ctx, []byte(cursor))
	if err != nil {
		return 0, err
	}
	defer b.free(ctx, cursorPtr, cursorSize)

	res, err := b.call(ctx, "am_lookup_cursor",
		uint64(obj),
		uint64(cursorPtr), uint64(cursorSize),
	)
	if err != nil {
		return 0, err
	}
	index := int32(res[0])
	if index < 0 {
		return 0, fmt.Errorf("automerge: am_lookup_cursor failed: %d", index)
	}
	return uint(index), nil
}

func (b *Backend) ObjSize(ctx context.Context, obj backend.ObjHandle) (uint, error) {
	res, err := b.call(ctx, "am_obj_size", uint64(obj))
	if err != nil {
		return 0, err
	}
	return uint(uint32(res[0])), nil
}

func (b *Backend) ObjType(ctx context.Context, obj backend.ObjHandle) (backend.Kind, error) {
	res, err := b.call(ctx, "am_obj_type", uint64(obj))
	if err != nil {
		return backend.KindVoid, err
	}
	switch int32(res[0]) {
	case 1:
		return backend.KindMap, nil
	case 2:
		return backend.KindList, nil
	case 3:
		return backend.KindText, nil
	default:
		return backend.KindUnknown, nil
	}
}

func (b *Backend) ObjKeys(ctx context.Context, obj backend.ObjHandle) ([]string, error) {
	res, err := b.call(ctx, "am_obj_keys_len", uint64(obj))
	if err != nil {
		return nil, err
	}
	keysLen := uint32(res[0])
	if keysLen == 0 {
		return nil, nil
	}
	ptr, err := b.alloc(ctx, keysLen)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, ptr, keysLen)
	_, err = b.call(ctx, "am_obj_keys", uint64(obj), uint64(ptr))
	if err != nil {
		return nil, err
	}
	data, err := b.readBytes(ptr, keysLen)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	offset := 0
	for offset+4 <= len(data) {
		keyLen := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
		if offset+keyLen > len(data) {
			break
		}
		keys = append(keys, string(data[offset:offset+keyLen]))
		offset += keyLen
	}
	return keys, nil
}

func (b *Backend) ObjItems(ctx context.Context, obj backend.ObjHandle) ([]*backend.BackendValue, error) {
	res, err := b.call(ctx, "am_obj_items_len", uint64(obj))
	if err != nil {
		return nil, err
	}
	itemsLen := uint32(res[0])
	if itemsLen == 0 {
		return nil, nil
	}
	ptr, err := b.alloc(ctx, itemsLen)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, ptr, itemsLen)
	_, err = b.call(ctx, "am_obj_items", uint64(obj), uint64(ptr))
	if err != nil {
		return nil, err
	}
	data, err := b.readBytes(ptr, itemsLen)
	if err != nil {
		return nil, err
	}
	return backend.DecodeValueList(data)
}

func (b *Backend) ObjFree(ctx context.Context, obj backend.ObjHandle) error {
	if obj == backend.RootObjHandle {
		return nil
	}
	res, err := b.call(ctx, "am_obj_free", uint64(obj))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_obj_free failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) Commit(ctx context.Context, msg string, timestamp int64) (backend.ChangeHash, error) {
	msgPtr, msgSize, err := b.writeBytes(ctx, []byte(msg))
	if err != nil {
		return backend.ChangeHash{}, err
	}
	defer b.free(ctx, msgPtr, msgSize)

	res, err := b.call(ctx, "am_commit", uint64(msgPtr), uint64(msgSize), uint64(timestamp))
	if err != nil {
		return backend.ChangeHash{}, err
	}
	rc := int32(res[0])
	if rc == -3 {
		return backend.ChangeHash{}, backend.ErrEmptyCommit
	}
	if rc != 0 {
		if errMsg := b.lastError(ctx); errMsg != "" {
			return backend.ChangeHash{}, fmt.Errorf("%s", errMsg)
		}
		return backend.ChangeHash{}, fmt.Errorf("automerge: am_commit returned %d", rc)
	}

	res, err = b.call(ctx, "am_commit_hash_len")
	if err != nil {
		return backend.ChangeHash{}, err
	}
	hashLen := uint32(res[0])
	if hashLen != 32 {
		return backend.ChangeHash{}, fmt.Errorf("automerge: expected 32-byte commit hash, got %d", hashLen)
	}
	hashPtr, err := b.alloc(ctx, 32)
	if err != nil {
		return backend.ChangeHash{}, err
	}
	defer b.free(ctx, hashPtr, 32)
	_, err = b.call(ctx, "am_commit_hash", uint64(hashPtr))
	if err != nil {
		return backend.ChangeHash{}, err
	}
	hashBytes, err := b.readBytes(hashPtr, 32)
	if err != nil {
		return backend.ChangeHash{}, err
	}
	var ch backend.ChangeHash
	copy(ch[:], hashBytes)
	return ch, nil
}

func (b *Backend) EmptyChange(ctx context.Context, msg string, timestamp int64) (backend.ChangeHash, error) {
	msgPtr, msgSize, err := b.writeBytes(ctx, []byte(msg))
	if err != nil {
		return backend.ChangeHash{}, err
	}
	defer b.free(ctx, msgPtr, msgSize)

	res, err := b.call(ctx, "am_empty_change", uint64(msgPtr), uint64(msgSize), uint64(timestamp))
	if err != nil {
		return backend.ChangeHash{}, err
	}
	if int32(res[0]) != 0 {
		return backend.ChangeHash{}, fmt.Errorf("automerge: am_empty_change failed: %d", int32(res[0]))
	}

	res, err = b.call(ctx, "am_commit_hash_len")
	if err != nil {
		return backend.ChangeHash{}, err
	}
	hashLen := uint32(res[0])
	hashPtr, err := b.alloc(ctx, hashLen)
	if err != nil {
		return backend.ChangeHash{}, err
	}
	defer b.free(ctx, hashPtr, hashLen)
	_, err = b.call(ctx, "am_commit_hash", uint64(hashPtr))
	if err != nil {
		return backend.ChangeHash{}, err
	}
	hashBytes, err := b.readBytes(hashPtr, hashLen)
	if err != nil {
		return backend.ChangeHash{}, err
	}
	var ch backend.ChangeHash
	copy(ch[:], hashBytes)
	return ch, nil
}

func (b *Backend) Heads(ctx context.Context) ([]backend.ChangeHash, error) {
	res, err := b.call(ctx, "am_get_heads_len")
	if err != nil {
		return nil, err
	}
	totalLen := uint32(res[0])
	if totalLen == 0 {
		return nil, nil
	}
	ptr, err := b.alloc(ctx, totalLen)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, ptr, totalLen)
	_, err = b.call(ctx, "am_get_heads", uint64(ptr))
	if err != nil {
		return nil, err
	}
	data, err := b.readBytes(ptr, totalLen)
	if err != nil {
		return nil, err
	}
	return backend.ParseChangeHashes(data), nil
}

func (b *Backend) GetChanges(ctx context.Context, since []backend.ChangeHash) ([][]byte, error) {
	headsBytes := backend.EncodeChangeHashes(since)
	headsPtr, headsSize, err := b.writeBytes(ctx, headsBytes)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, headsPtr, headsSize)

	headsCount := uint32(len(since))
	res, err := b.call(ctx, "am_get_changes_len", uint64(headsPtr), uint64(headsCount))
	if err != nil {
		return nil, err
	}
	totalLen := uint32(res[0])
	if totalLen == 0 {
		return nil, nil
	}
	outPtr, err := b.alloc(ctx, totalLen)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, outPtr, totalLen)
	_, err = b.call(ctx, "am_get_changes", uint64(headsPtr), uint64(headsCount), uint64(outPtr))
	if err != nil {
		return nil, err
	}
	data, err := b.readBytes(outPtr, totalLen)
	if err != nil {
		return nil, err
	}
	return backend.ParseLengthPrefixedList(data), nil
}

func (b *Backend) GetChangeByHash(ctx context.Context, hash backend.ChangeHash) ([]byte, error) {
	hashPtr, hashSize, err := b.writeBytes(ctx, hash[:])
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, hashPtr, hashSize)

	res, err := b.call(ctx, "am_get_change_by_hash_len", uint64(hashPtr))
	if err != nil {
		return nil, err
	}
	changeLen := uint32(res[0])
	if changeLen == 0 {
		return nil, nil
	}
	outPtr, err := b.alloc(ctx, changeLen)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, outPtr, changeLen)
	_, err = b.call(ctx, "am_get_change_by_hash", uint64(hashPtr), uint64(outPtr))
	if err != nil {
		return nil, err
	}
	return b.readBytes(outPtr, changeLen)
}

func (b *Backend) ApplyChanges(ctx context.Context, changes [][]byte) error {
	encoded := backend.EncodeLengthPrefixedList(changes)
	ptr, size, err := b.writeBytes(ctx, encoded)
	if err != nil {
		return err
	}
	defer b.free(ctx, ptr, size)

	res, err := b.call(ctx, "am_apply_changes", uint64(ptr), uint64(size))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_apply_changes failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) GetChangeInfo(ctx context.Context, raw []byte) (*backend.ChangeInfo, error) {
	rawPtr, rawSize, err := b.writeBytes(ctx, raw)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, rawPtr, rawSize)

	res, err := b.call(ctx, "am_change_info_len", uint64(rawPtr), uint64(rawSize))
	if err != nil {
		return nil, err
	}
	infoLen := uint32(res[0])
	if infoLen == 0 {
		return nil, fmt.Errorf("automerge: am_change_info_len returned 0 (invalid change?)")
	}
	outPtr, err := b.alloc(ctx, infoLen)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, outPtr, infoLen)
	_, err = b.call(ctx, "am_change_info", uint64(rawPtr), uint64(rawSize), uint64(outPtr))
	if err != nil {
		return nil, err
	}
	data, err := b.readBytes(outPtr, infoLen)
	if err != nil {
		return nil, err
	}
	return backend.ParseChangeInfo(data)
}

func (b *Backend) SaveIncremental(ctx context.Context) ([]byte, error) {
	res, err := b.call(ctx, "am_save_incremental_len")
	if err != nil {
		return nil, err
	}
	incLen := uint32(res[0])
	if incLen == 0 {
		return nil, nil
	}
	ptr, err := b.alloc(ctx, incLen)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, ptr, incLen)
	_, err = b.call(ctx, "am_save_incremental", uint64(ptr))
	if err != nil {
		return nil, err
	}
	return b.readBytes(ptr, incLen)
}

func (b *Backend) LoadIncremental(ctx context.Context, data []byte) error {
	ptr, size, err := b.writeBytes(ctx, data)
	if err != nil {
		return err
	}
	defer b.free(ctx, ptr, size)

	res, err := b.call(ctx, "am_load_incremental", uint64(ptr), uint64(size))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_load_incremental failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) Fork(ctx context.Context, atHeads []backend.ChangeHash) (backend.Backend, error) {
	saved, err := b.Save(ctx)
	if err != nil {
		return nil, err
	}

	nb, err := newBackendInstance(ctx)
	if err != nil {
		return nil, err
	}

	ptr, size, err := nb.writeBytes(ctx, saved)
	if err != nil {
		nb.Close(ctx)
		return nil, err
	}
	defer nb.free(ctx, ptr, size)

	res, err := nb.call(ctx, "am_load", uint64(ptr), uint64(size))
	if err != nil {
		nb.Close(ctx)
		return nil, err
	}
	if int32(res[0]) != 0 {
		nb.Close(ctx)
		return nil, fmt.Errorf("automerge: am_load failed during fork: %d", int32(res[0]))
	}

	headsBytes := backend.EncodeChangeHashes(atHeads)
	headsPtr, headsSize, err := nb.writeBytes(ctx, headsBytes)
	if err != nil {
		nb.Close(ctx)
		return nil, err
	}
	defer nb.free(ctx, headsPtr, headsSize)

	headsCount := uint32(len(atHeads))
	res, err = nb.call(ctx, "am_fork", uint64(headsPtr), uint64(headsCount))
	if err != nil {
		nb.Close(ctx)
		return nil, err
	}
	if int32(res[0]) != 0 {
		nb.Close(ctx)
		return nil, fmt.Errorf("automerge: am_fork failed: %d", int32(res[0]))
	}

	res, err = nb.call(ctx, "am_fork_len")
	if err != nil {
		nb.Close(ctx)
		return nil, err
	}
	forkLen := uint32(res[0])
	if forkLen == 0 {
		return nb, nil
	}

	forkPtr, err := nb.alloc(ctx, forkLen)
	if err != nil {
		nb.Close(ctx)
		return nil, err
	}
	defer nb.free(ctx, forkPtr, forkLen)
	_, err = nb.call(ctx, "am_fork_get", uint64(forkPtr))
	if err != nil {
		nb.Close(ctx)
		return nil, err
	}
	forkData, err := nb.readBytes(forkPtr, forkLen)
	if err != nil {
		nb.Close(ctx)
		return nil, err
	}

	forkLoadPtr, forkLoadSize, err := nb.writeBytes(ctx, forkData)
	if err != nil {
		nb.Close(ctx)
		return nil, err
	}
	defer nb.free(ctx, forkLoadPtr, forkLoadSize)
	res, err = nb.call(ctx, "am_load", uint64(forkLoadPtr), uint64(forkLoadSize))
	if err != nil {
		nb.Close(ctx)
		return nil, err
	}
	if int32(res[0]) != 0 {
		nb.Close(ctx)
		return nil, fmt.Errorf("automerge: am_load forked data failed: %d", int32(res[0]))
	}

	return nb, nil
}

func (b *Backend) Merge(ctx context.Context, otherSaved []byte) error {
	ptr, size, err := b.writeBytes(ctx, otherSaved)
	if err != nil {
		return err
	}
	defer b.free(ctx, ptr, size)

	res, err := b.call(ctx, "am_merge", uint64(ptr), uint64(size))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		if errMsg := b.lastError(ctx); errMsg != "" {
			return fmt.Errorf("%s", errMsg)
		}
		return fmt.Errorf("automerge: am_merge failed: %d", int32(res[0]))
	}
	return nil
}

// --- Sync operations ---

func (b *Backend) SyncInit(ctx context.Context) (uint32, error) {
	res, err := b.call(ctx, "am_sync_state_init")
	if err != nil {
		return 0, err
	}
	peerID := uint32(res[0])
	if peerID == 0 {
		return 0, fmt.Errorf("automerge: am_sync_state_init failed")
	}
	return peerID, nil
}

func (b *Backend) SyncFree(ctx context.Context, peerID uint32) error {
	res, err := b.call(ctx, "am_sync_state_free", uint64(peerID))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_sync_state_free failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) SyncGenerateMessage(ctx context.Context, peerID uint32) ([]byte, bool, error) {
	res, err := b.call(ctx, "am_sync_gen_len", uint64(peerID))
	if err != nil {
		return nil, false, err
	}
	msgLen := uint32(res[0])
	if msgLen == 0 {
		return nil, false, nil
	}
	ptr, err := b.alloc(ctx, msgLen)
	if err != nil {
		return nil, false, err
	}
	defer b.free(ctx, ptr, msgLen)
	res, err = b.call(ctx, "am_sync_gen", uint64(peerID), uint64(ptr))
	if err != nil {
		return nil, false, err
	}
	if int32(res[0]) == 1 {
		return nil, false, nil
	}
	if int32(res[0]) != 0 {
		return nil, false, fmt.Errorf("automerge: am_sync_gen failed: %d", int32(res[0]))
	}
	data, err := b.readBytes(ptr, msgLen)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (b *Backend) SyncReceiveMessage(ctx context.Context, peerID uint32, msg []byte) error {
	ptr, size, err := b.writeBytes(ctx, msg)
	if err != nil {
		return err
	}
	defer b.free(ctx, ptr, size)

	res, err := b.call(ctx, "am_sync_recv", uint64(peerID), uint64(ptr), uint64(size))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_sync_recv failed: %d", int32(res[0]))
	}
	return nil
}

func (b *Backend) SyncSave(ctx context.Context, peerID uint32) ([]byte, error) {
	res, err := b.call(ctx, "am_sync_state_save_len", uint64(peerID))
	if err != nil {
		return nil, err
	}
	saveLen := uint32(res[0])
	if saveLen == 0 {
		return nil, nil
	}
	ptr, err := b.alloc(ctx, saveLen)
	if err != nil {
		return nil, err
	}
	defer b.free(ctx, ptr, saveLen)
	_, err = b.call(ctx, "am_sync_state_save", uint64(peerID), uint64(ptr))
	if err != nil {
		return nil, err
	}
	return b.readBytes(ptr, saveLen)
}

func (b *Backend) SyncLoad(ctx context.Context, data []byte) (uint32, error) {
	ptr, size, err := b.writeBytes(ctx, data)
	if err != nil {
		return 0, err
	}
	defer b.free(ctx, ptr, size)

	res, err := b.call(ctx, "am_sync_state_load", uint64(ptr), uint64(size))
	if err != nil {
		return 0, err
	}
	peerID := uint32(res[0])
	if peerID == 0 {
		return 0, fmt.Errorf("automerge: am_sync_state_load failed")
	}
	return peerID, nil
}
