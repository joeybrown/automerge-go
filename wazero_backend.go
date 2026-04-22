package automerge

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	_ "embed"

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

// wazeroBackend implements the backend interface using a wazero WASM module instance.
type wazeroBackend struct {
	mod api.Module
}

func newWazeroBackend(ctx context.Context) (*wazeroBackend, error) {
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
	return &wazeroBackend{mod: mod}, nil
}

// call invokes a WASM exported function by name.
func (b *wazeroBackend) call(ctx context.Context, name string, params ...uint64) ([]uint64, error) {
	fn := b.mod.ExportedFunction(name)
	if fn == nil {
		return nil, fmt.Errorf("automerge: WASM export %q not found", name)
	}
	return fn.Call(ctx, params...)
}

// lastError reads the last error string from the WASM module.
// Returns empty string if no error is stored.
func (b *wazeroBackend) lastError(ctx context.Context) string {
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

// alloc allocates n bytes in WASM memory.
func (b *wazeroBackend) alloc(ctx context.Context, n uint32) (uint32, error) {
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

// free frees WASM memory.
func (b *wazeroBackend) free(ctx context.Context, ptr, size uint32) {
	if ptr != 0 && size != 0 {
		b.call(ctx, "am_free", uint64(ptr), uint64(size))
	}
}

// writeBytes writes Go bytes into WASM memory.
func (b *wazeroBackend) writeBytes(ctx context.Context, data []byte) (ptr, size uint32, err error) {
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

// readBytes reads n bytes from WASM memory, returning a Go-owned copy.
func (b *wazeroBackend) readBytes(ptr, size uint32) ([]byte, error) {
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

// --- Value encoding helpers ---

func encodeNull() (byte, []byte)  { return tagNull, nil }
func encodeBool(v bool) (byte, []byte) {
	if v {
		return tagBool, []byte{1}
	}
	return tagBool, []byte{0}
}
func encodeStr(v string) (byte, []byte)  { return tagString, []byte(v) }
func encodeBytes(v []byte) (byte, []byte) { return tagBytes, v }
func encodeInt64(v int64) (byte, []byte) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(v))
	return tagInt64, buf
}
func encodeUint64(v uint64) (byte, []byte) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, v)
	return tagUint64, buf
}
func encodeFloat64(v float64) (byte, []byte) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, math.Float64bits(v))
	return tagFloat64, buf
}
func encodeTimestamp(v time.Time) (byte, []byte) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(v.UnixMilli()))
	return tagTimestamp, buf
}
func encodeCounter(v int64) (byte, []byte) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(v))
	return tagCounter, buf
}
func encodeObjKind(k Kind) byte {
	switch k {
	case KindMap:
		return tagMap
	case KindList:
		return tagList
	case KindText:
		return tagText
	default:
		panic(fmt.Errorf("automerge: cannot encode Kind %v as object tag", k))
	}
}

// decodeValue decodes a tagged value buffer into a backendValue.
func decodeValue(data []byte) (*backendValue, error) {
	if len(data) == 0 {
		return &backendValue{kind: KindVoid}, nil
	}
	tag := data[0]
	payload := data[1:]
	switch tag {
	case tagVoid:
		return &backendValue{kind: KindVoid}, nil
	case tagNull:
		return &backendValue{kind: KindNull, val: nil}, nil
	case tagBool:
		if len(payload) < 1 {
			return nil, fmt.Errorf("automerge: bool payload too short")
		}
		return &backendValue{kind: KindBool, val: payload[0] != 0}, nil
	case tagInt64:
		if len(payload) < 8 {
			return nil, fmt.Errorf("automerge: int64 payload too short")
		}
		return &backendValue{kind: KindInt64, val: int64(binary.LittleEndian.Uint64(payload[:8]))}, nil
	case tagUint64:
		if len(payload) < 8 {
			return nil, fmt.Errorf("automerge: uint64 payload too short")
		}
		return &backendValue{kind: KindUint64, val: binary.LittleEndian.Uint64(payload[:8])}, nil
	case tagFloat64:
		if len(payload) < 8 {
			return nil, fmt.Errorf("automerge: float64 payload too short")
		}
		return &backendValue{kind: KindFloat64, val: math.Float64frombits(binary.LittleEndian.Uint64(payload[:8]))}, nil
	case tagString:
		return &backendValue{kind: KindStr, val: string(payload)}, nil
	case tagBytes:
		b := make([]byte, len(payload))
		copy(b, payload)
		return &backendValue{kind: KindBytes, val: b}, nil
	case tagCounter:
		if len(payload) < 8 {
			return nil, fmt.Errorf("automerge: counter payload too short")
		}
		return &backendValue{kind: KindCounter, val: int64(binary.LittleEndian.Uint64(payload[:8]))}, nil
	case tagTimestamp:
		if len(payload) < 8 {
			return nil, fmt.Errorf("automerge: timestamp payload too short")
		}
		ms := int64(binary.LittleEndian.Uint64(payload[:8]))
		return &backendValue{kind: KindTime, val: time.UnixMilli(ms)}, nil
	case tagMap:
		if len(payload) < 4 {
			return nil, fmt.Errorf("automerge: map handle payload too short")
		}
		h := objHandle(binary.LittleEndian.Uint32(payload[:4]))
		return &backendValue{kind: KindMap, obj: h}, nil
	case tagList:
		if len(payload) < 4 {
			return nil, fmt.Errorf("automerge: list handle payload too short")
		}
		h := objHandle(binary.LittleEndian.Uint32(payload[:4]))
		return &backendValue{kind: KindList, obj: h}, nil
	case tagText:
		if len(payload) < 4 {
			return nil, fmt.Errorf("automerge: text handle payload too short")
		}
		h := objHandle(binary.LittleEndian.Uint32(payload[:4]))
		return &backendValue{kind: KindText, obj: h}, nil
	default:
		return &backendValue{kind: KindUnknown}, nil
	}
}

// --- backend interface implementation ---

func (b *wazeroBackend) close(ctx context.Context) error {
	return b.mod.Close(ctx)
}

func (b *wazeroBackend) save(ctx context.Context) ([]byte, error) {
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

func (b *wazeroBackend) getActorID(ctx context.Context) (string, error) {
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

func (b *wazeroBackend) setActorID(ctx context.Context, id string) error {
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

func (b *wazeroBackend) mapGet(ctx context.Context, obj objHandle, key string) (*backendValue, error) {
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
		return &backendValue{kind: KindVoid}, nil
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
	return decodeValue(data)
}

func (b *wazeroBackend) mapPut(ctx context.Context, obj objHandle, key string, tag byte, payload []byte) (objHandle, error) {
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
	// For object creation (map/list/text), rc is the new handle
	if tag == tagMap || tag == tagList || tag == tagText {
		return objHandle(rc), nil
	}
	return 0, nil
}

func (b *wazeroBackend) mapDelete(ctx context.Context, obj objHandle, key string) error {
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

func (b *wazeroBackend) mapIncrement(ctx context.Context, obj objHandle, key string, delta int64) error {
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

func (b *wazeroBackend) listGet(ctx context.Context, obj objHandle, index uint) (*backendValue, error) {
	res, err := b.call(ctx, "am_list_get_len", uint64(obj), uint64(index))
	if err != nil {
		return nil, err
	}
	getLen := uint32(res[0])
	if getLen == 0 {
		return &backendValue{kind: KindVoid}, nil
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
	return decodeValue(data)
}

func (b *wazeroBackend) listPut(ctx context.Context, obj objHandle, index uint, insert bool, tag byte, payload []byte) (objHandle, error) {
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
	if tag == tagMap || tag == tagList || tag == tagText {
		return objHandle(rc), nil
	}
	return 0, nil
}

func (b *wazeroBackend) listDelete(ctx context.Context, obj objHandle, index uint) error {
	res, err := b.call(ctx, "am_list_delete", uint64(obj), uint64(index))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_list_delete failed: %d", int32(res[0]))
	}
	return nil
}

func (b *wazeroBackend) listLen(ctx context.Context, obj objHandle) (uint, error) {
	res, err := b.call(ctx, "am_list_len", uint64(obj))
	if err != nil {
		return 0, err
	}
	return uint(uint32(res[0])), nil
}

func (b *wazeroBackend) listIncrement(ctx context.Context, obj objHandle, index uint, delta int64) error {
	res, err := b.call(ctx, "am_list_increment", uint64(obj), uint64(index), uint64(delta))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_list_increment failed: %d", int32(res[0]))
	}
	return nil
}

func (b *wazeroBackend) textGet(ctx context.Context, obj objHandle) (string, error) {
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

func (b *wazeroBackend) textSplice(ctx context.Context, obj objHandle, pos uint, del int, text string) error {
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

func (b *wazeroBackend) objSize(ctx context.Context, obj objHandle) (uint, error) {
	res, err := b.call(ctx, "am_obj_size", uint64(obj))
	if err != nil {
		return 0, err
	}
	return uint(uint32(res[0])), nil
}

func (b *wazeroBackend) objType(ctx context.Context, obj objHandle) (Kind, error) {
	res, err := b.call(ctx, "am_obj_type", uint64(obj))
	if err != nil {
		return KindVoid, err
	}
	switch int32(res[0]) {
	case 1:
		return KindMap, nil
	case 2:
		return KindList, nil
	case 3:
		return KindText, nil
	default:
		return KindUnknown, nil
	}
}

func (b *wazeroBackend) objKeys(ctx context.Context, obj objHandle) ([]string, error) {
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
	// Length-prefixed key strings: [4-byte LE key_len][key bytes] repeated
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

func (b *wazeroBackend) objItems(ctx context.Context, obj objHandle) ([]*backendValue, error) {
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
	return decodeValueList(data)
}

func (b *wazeroBackend) objFree(ctx context.Context, obj objHandle) error {
	if obj == rootObjHandle {
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

func (b *wazeroBackend) commit(ctx context.Context, msg string, timestamp int64) (ChangeHash, error) {
	msgPtr, msgSize, err := b.writeBytes(ctx, []byte(msg))
	if err != nil {
		return ChangeHash{}, err
	}
	defer b.free(ctx, msgPtr, msgSize)

	res, err := b.call(ctx, "am_commit", uint64(msgPtr), uint64(msgSize), uint64(timestamp))
	if err != nil {
		return ChangeHash{}, err
	}
	rc := int32(res[0])
	if rc == -3 {
		return ChangeHash{}, errEmptyCommit
	}
	if rc != 0 {
		if errMsg := b.lastError(ctx); errMsg != "" {
			return ChangeHash{}, fmt.Errorf("%s", errMsg)
		}
		return ChangeHash{}, fmt.Errorf("automerge: am_commit returned %d", rc)
	}

	// Get the hash from return buffer
	res, err = b.call(ctx, "am_commit_hash_len")
	if err != nil {
		return ChangeHash{}, err
	}
	hashLen := uint32(res[0])
	if hashLen != 32 {
		return ChangeHash{}, fmt.Errorf("automerge: expected 32-byte commit hash, got %d", hashLen)
	}
	hashPtr, err := b.alloc(ctx, 32)
	if err != nil {
		return ChangeHash{}, err
	}
	defer b.free(ctx, hashPtr, 32)
	_, err = b.call(ctx, "am_commit_hash", uint64(hashPtr))
	if err != nil {
		return ChangeHash{}, err
	}
	hashBytes, err := b.readBytes(hashPtr, 32)
	if err != nil {
		return ChangeHash{}, err
	}
	var ch ChangeHash
	copy(ch[:], hashBytes)
	return ch, nil
}

func (b *wazeroBackend) emptyChange(ctx context.Context, msg string, timestamp int64) (ChangeHash, error) {
	msgPtr, msgSize, err := b.writeBytes(ctx, []byte(msg))
	if err != nil {
		return ChangeHash{}, err
	}
	defer b.free(ctx, msgPtr, msgSize)

	res, err := b.call(ctx, "am_empty_change", uint64(msgPtr), uint64(msgSize), uint64(timestamp))
	if err != nil {
		return ChangeHash{}, err
	}
	if int32(res[0]) != 0 {
		return ChangeHash{}, fmt.Errorf("automerge: am_empty_change failed: %d", int32(res[0]))
	}

	res, err = b.call(ctx, "am_commit_hash_len")
	if err != nil {
		return ChangeHash{}, err
	}
	hashLen := uint32(res[0])
	hashPtr, err := b.alloc(ctx, hashLen)
	if err != nil {
		return ChangeHash{}, err
	}
	defer b.free(ctx, hashPtr, hashLen)
	_, err = b.call(ctx, "am_commit_hash", uint64(hashPtr))
	if err != nil {
		return ChangeHash{}, err
	}
	hashBytes, err := b.readBytes(hashPtr, hashLen)
	if err != nil {
		return ChangeHash{}, err
	}
	var ch ChangeHash
	copy(ch[:], hashBytes)
	return ch, nil
}

func (b *wazeroBackend) heads(ctx context.Context) ([]ChangeHash, error) {
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
	return parseChangeHashes(data), nil
}

func (b *wazeroBackend) getChanges(ctx context.Context, since []ChangeHash) ([][]byte, error) {
	headsBytes := encodeChangeHashes(since)
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
	// Parse: [4-byte LE len][raw bytes] repeated
	return parseLengthPrefixedList(data), nil
}

func (b *wazeroBackend) getChangeByHash(ctx context.Context, hash ChangeHash) ([]byte, error) {
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

func (b *wazeroBackend) applyChanges(ctx context.Context, changes [][]byte) error {
	// Encode: [4-byte LE len][raw bytes] repeated
	encoded := encodeLengthPrefixedList(changes)
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

func (b *wazeroBackend) changeInfo(ctx context.Context, raw []byte) (*changeInfo, error) {
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
	return parseChangeInfo(data)
}

func (b *wazeroBackend) saveIncremental(ctx context.Context) ([]byte, error) {
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

func (b *wazeroBackend) loadIncremental(ctx context.Context, data []byte) error {
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

func (b *wazeroBackend) fork(ctx context.Context, atHeads []ChangeHash) (backend, error) {
	// Save current doc state
	saved, err := b.save(ctx)
	if err != nil {
		return nil, err
	}

	// Create new instance
	nb, err := newWazeroBackend(ctx)
	if err != nil {
		return nil, err
	}

	// Load saved data
	ptr, size, err := nb.writeBytes(ctx, saved)
	if err != nil {
		nb.close(ctx)
		return nil, err
	}
	defer nb.free(ctx, ptr, size)

	res, err := nb.call(ctx, "am_load", uint64(ptr), uint64(size))
	if err != nil {
		nb.close(ctx)
		return nil, err
	}
	if int32(res[0]) != 0 {
		nb.close(ctx)
		return nil, fmt.Errorf("automerge: am_load failed during fork: %d", int32(res[0]))
	}

	// Fork at the given heads
	headsBytes := encodeChangeHashes(atHeads)
	headsPtr, headsSize, err := nb.writeBytes(ctx, headsBytes)
	if err != nil {
		nb.close(ctx)
		return nil, err
	}
	defer nb.free(ctx, headsPtr, headsSize)

	headsCount := uint32(len(atHeads))
	res, err = nb.call(ctx, "am_fork", uint64(headsPtr), uint64(headsCount))
	if err != nil {
		nb.close(ctx)
		return nil, err
	}
	if int32(res[0]) != 0 {
		nb.close(ctx)
		return nil, fmt.Errorf("automerge: am_fork failed: %d", int32(res[0]))
	}

	// Fork stores forked doc bytes in return buffer; we need to load them
	res, err = nb.call(ctx, "am_fork_len")
	if err != nil {
		nb.close(ctx)
		return nil, err
	}
	forkLen := uint32(res[0])
	if forkLen == 0 {
		// No heads means fork at current state, already loaded
		return nb, nil
	}

	forkPtr, err := nb.alloc(ctx, forkLen)
	if err != nil {
		nb.close(ctx)
		return nil, err
	}
	defer nb.free(ctx, forkPtr, forkLen)
	_, err = nb.call(ctx, "am_fork_get", uint64(forkPtr))
	if err != nil {
		nb.close(ctx)
		return nil, err
	}
	forkData, err := nb.readBytes(forkPtr, forkLen)
	if err != nil {
		nb.close(ctx)
		return nil, err
	}

	// Load the forked data
	forkLoadPtr, forkLoadSize, err := nb.writeBytes(ctx, forkData)
	if err != nil {
		nb.close(ctx)
		return nil, err
	}
	defer nb.free(ctx, forkLoadPtr, forkLoadSize)
	res, err = nb.call(ctx, "am_load", uint64(forkLoadPtr), uint64(forkLoadSize))
	if err != nil {
		nb.close(ctx)
		return nil, err
	}
	if int32(res[0]) != 0 {
		nb.close(ctx)
		return nil, fmt.Errorf("automerge: am_load forked data failed: %d", int32(res[0]))
	}

	return nb, nil
}

func (b *wazeroBackend) merge(ctx context.Context, otherSaved []byte) error {
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

func (b *wazeroBackend) syncInit(ctx context.Context) (uint32, error) {
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

func (b *wazeroBackend) syncFree(ctx context.Context, peerID uint32) error {
	res, err := b.call(ctx, "am_sync_state_free", uint64(peerID))
	if err != nil {
		return err
	}
	if int32(res[0]) != 0 {
		return fmt.Errorf("automerge: am_sync_state_free failed: %d", int32(res[0]))
	}
	return nil
}

func (b *wazeroBackend) syncGenerateMessage(ctx context.Context, peerID uint32) ([]byte, bool, error) {
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
		return nil, false, nil // no message to send
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

func (b *wazeroBackend) syncReceiveMessage(ctx context.Context, peerID uint32, msg []byte) error {
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

func (b *wazeroBackend) syncSave(ctx context.Context, peerID uint32) ([]byte, error) {
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

func (b *wazeroBackend) syncLoad(ctx context.Context, data []byte) (uint32, error) {
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

// --- Helpers ---

func parseChangeHashes(data []byte) []ChangeHash {
	if len(data) == 0 {
		return nil
	}
	hashes := make([]ChangeHash, 0, len(data)/32)
	for i := 0; i+32 <= len(data); i += 32 {
		var h ChangeHash
		copy(h[:], data[i:i+32])
		hashes = append(hashes, h)
	}
	return hashes
}

func encodeChangeHashes(hashes []ChangeHash) []byte {
	if len(hashes) == 0 {
		return nil
	}
	out := make([]byte, 0, len(hashes)*32)
	for _, h := range hashes {
		out = append(out, h[:]...)
	}
	return out
}

func parseLengthPrefixedList(data []byte) [][]byte {
	var items [][]byte
	offset := 0
	for offset+4 <= len(data) {
		itemLen := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
		if offset+itemLen > len(data) {
			break
		}
		item := make([]byte, itemLen)
		copy(item, data[offset:offset+itemLen])
		items = append(items, item)
		offset += itemLen
	}
	return items
}

func encodeLengthPrefixedList(items [][]byte) []byte {
	total := 0
	for _, item := range items {
		total += 4 + len(item)
	}
	out := make([]byte, 0, total)
	for _, item := range items {
		lenBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(lenBuf, uint32(len(item)))
		out = append(out, lenBuf...)
		out = append(out, item...)
	}
	return out
}

func parseChangeInfo(data []byte) (*changeInfo, error) {
	if len(data) < 32+8+8+4 {
		return nil, fmt.Errorf("automerge: change info too short")
	}
	ci := &changeInfo{}
	offset := 0

	// hash (32 bytes)
	copy(ci.hash[:], data[offset:offset+32])
	offset += 32

	// timestamp (8 bytes LE i64)
	ms := int64(binary.LittleEndian.Uint64(data[offset : offset+8]))
	ci.timestamp = time.UnixMilli(ms)
	offset += 8

	// seq (8 bytes LE u64)
	ci.seq = binary.LittleEndian.Uint64(data[offset : offset+8])
	offset += 8

	// actor hex string (4-byte len + data)
	if offset+4 > len(data) {
		return nil, fmt.Errorf("automerge: change info truncated at actor_len")
	}
	actorLen := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if offset+actorLen > len(data) {
		return nil, fmt.Errorf("automerge: change info truncated at actor")
	}
	ci.actorID = string(data[offset : offset+actorLen])
	offset += actorLen

	// message (4-byte len + data)
	if offset+4 > len(data) {
		return nil, fmt.Errorf("automerge: change info truncated at message_len")
	}
	msgLen := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if offset+msgLen > len(data) {
		return nil, fmt.Errorf("automerge: change info truncated at message")
	}
	ci.message = string(data[offset : offset+msgLen])
	offset += msgLen

	// deps (4-byte count + 32 bytes each)
	if offset+4 > len(data) {
		return nil, fmt.Errorf("automerge: change info truncated at deps_count")
	}
	depsCount := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
	offset += 4
	ci.deps = make([]ChangeHash, depsCount)
	for i := 0; i < depsCount; i++ {
		if offset+32 > len(data) {
			return nil, fmt.Errorf("automerge: change info truncated at dep %d", i)
		}
		copy(ci.deps[i][:], data[offset:offset+32])
		offset += 32
	}

	return ci, nil
}

// decodeValueList decodes concatenated tagged values.
func decodeValueList(data []byte) ([]*backendValue, error) {
	var items []*backendValue
	offset := 0
	for offset < len(data) {
		tag := data[offset]
		size := taggedValueSize(tag, data[offset:])
		if size <= 0 || offset+size > len(data) {
			break
		}
		bv, err := decodeValue(data[offset : offset+size])
		if err != nil {
			return nil, err
		}
		items = append(items, bv)
		offset += size
	}
	return items, nil
}

// taggedValueSize returns the total size of a tagged value (tag + payload).
func taggedValueSize(tag byte, data []byte) int {
	switch tag {
	case tagVoid, tagNull:
		return 1
	case tagBool:
		return 2 // tag + 1 byte
	case tagInt64, tagUint64, tagFloat64, tagCounter, tagTimestamp:
		return 9 // tag + 8 bytes
	case tagMap, tagList, tagText:
		return 5 // tag + 4-byte handle
	case tagString, tagBytes:
		// For concatenated items, strings/bytes need a length prefix.
		// The WASM obj_items format uses 4-byte LE length prefix after the tag.
		if len(data) < 5 {
			return -1
		}
		payloadLen := int(binary.LittleEndian.Uint32(data[1:5]))
		return 5 + payloadLen // tag + 4-byte len + payload
	default:
		return -1
	}
}
