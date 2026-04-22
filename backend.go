package automerge

import (
	"context"
	"errors"
	"time"
)

// errEmptyCommit is returned by commit when there are no pending operations.
var errEmptyCommit = errors.New("no pending operations")

// objHandle is an opaque reference to an automerge object in the backend.
// Handle 0 always refers to the document root map.
type objHandle uint32

const (
	rootObjHandle    objHandle = 0
	invalidObjHandle objHandle = 0xFFFFFFFF
)

// Value type tags matching wasi/src/value.rs
const (
	tagNull      byte = 0x00
	tagBool      byte = 0x01
	tagInt64     byte = 0x02
	tagUint64    byte = 0x03
	tagFloat64   byte = 0x04
	tagString    byte = 0x05
	tagBytes     byte = 0x06
	tagCounter   byte = 0x07
	tagTimestamp byte = 0x08
	tagMap       byte = 0x09
	tagList      byte = 0x0A
	tagText      byte = 0x0B
	tagVoid      byte = 0xFF
)

// backendValue represents a typed value returned from the backend.
type backendValue struct {
	kind Kind
	val  any       // Go-native value for primitives; nil for objects
	obj  objHandle // valid only for KindMap/KindList/KindText
}

// changeInfo holds all the metadata extracted from a raw change.
type changeInfo struct {
	hash      ChangeHash
	timestamp time.Time
	seq       uint64
	actorID   string
	message   string
	deps      []ChangeHash
}

// backend is the internal interface that abstracts the automerge engine.
// It is implemented by wazeroBackend. This interface is NOT part of the public API.
type backend interface {
	// Lifecycle
	close(ctx context.Context) error

	// Document serialization
	save(ctx context.Context) ([]byte, error)

	// Actor
	getActorID(ctx context.Context) (string, error)
	setActorID(ctx context.Context, id string) error

	// Map operations
	mapGet(ctx context.Context, obj objHandle, key string) (*backendValue, error)
	mapPut(ctx context.Context, obj objHandle, key string, tag byte, payload []byte) (objHandle, error)
	mapDelete(ctx context.Context, obj objHandle, key string) error
	mapIncrement(ctx context.Context, obj objHandle, key string, delta int64) error

	// List operations
	listGet(ctx context.Context, obj objHandle, index uint) (*backendValue, error)
	listPut(ctx context.Context, obj objHandle, index uint, insert bool, tag byte, payload []byte) (objHandle, error)
	listDelete(ctx context.Context, obj objHandle, index uint) error
	listLen(ctx context.Context, obj objHandle) (uint, error)
	listIncrement(ctx context.Context, obj objHandle, index uint, delta int64) error

	// Text operations
	textGet(ctx context.Context, obj objHandle) (string, error)
	textSplice(ctx context.Context, obj objHandle, pos uint, del int, text string) error

	// Object introspection
	objSize(ctx context.Context, obj objHandle) (uint, error)
	objType(ctx context.Context, obj objHandle) (Kind, error)
	objKeys(ctx context.Context, obj objHandle) ([]string, error)
	objItems(ctx context.Context, obj objHandle) ([]*backendValue, error)
	objFree(ctx context.Context, obj objHandle) error

	// Commit
	commit(ctx context.Context, msg string, timestamp int64) (ChangeHash, error)
	emptyChange(ctx context.Context, msg string, timestamp int64) (ChangeHash, error)

	// History
	heads(ctx context.Context) ([]ChangeHash, error)
	getChanges(ctx context.Context, since []ChangeHash) ([][]byte, error)
	getChangeByHash(ctx context.Context, hash ChangeHash) ([]byte, error)
	applyChanges(ctx context.Context, changes [][]byte) error

	// Change introspection
	changeInfo(ctx context.Context, raw []byte) (*changeInfo, error)

	// Incremental save/load
	saveIncremental(ctx context.Context) ([]byte, error)
	loadIncremental(ctx context.Context, data []byte) error

	// Fork & merge
	fork(ctx context.Context, heads []ChangeHash) (backend, error)
	merge(ctx context.Context, otherSaved []byte) error

	// Sync protocol
	syncInit(ctx context.Context) (uint32, error)
	syncFree(ctx context.Context, peerID uint32) error
	syncGenerateMessage(ctx context.Context, peerID uint32) ([]byte, bool, error)
	syncReceiveMessage(ctx context.Context, peerID uint32, msg []byte) error
	syncSave(ctx context.Context, peerID uint32) ([]byte, error)
	syncLoad(ctx context.Context, data []byte) (uint32, error)
}
