package backend

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"
)

// ErrEmptyCommit is returned by commit when there are no pending operations.
var ErrEmptyCommit = errors.New("no pending operations")

// ObjHandle is an opaque reference to an automerge object in the backend.
// Handle 0 always refers to the document root map.
type ObjHandle uint32

const (
	RootObjHandle    ObjHandle = 0
	InvalidObjHandle ObjHandle = 0xFFFFFFFF
)

// Kind represents the underlying type of a Value
type Kind uint

var (
	KindVoid    Kind = 0
	KindBool    Kind = 1
	KindBytes   Kind = 2
	KindCounter Kind = 3
	KindFloat64 Kind = 4
	KindInt64   Kind = 5
	KindUint64  Kind = 6
	KindNull    Kind = 7
	KindStr     Kind = 8
	KindTime    Kind = 9
	KindUnknown Kind = 10
	KindMap     Kind = 11
	KindList    Kind = 12
	KindText    Kind = 13
)

var kindDescriptions = map[Kind]string{
	KindVoid:    "KindVoid",
	KindBool:    "KindBool",
	KindBytes:   "KindBytes",
	KindCounter: "KindCounter",
	KindFloat64: "KindFloat64",
	KindInt64:   "KindInt64",
	KindUint64:  "KindUint64",
	KindNull:    "KindNull",
	KindStr:     "KindStr",
	KindTime:    "KindTime",
	KindUnknown: "KindUnknown",
	KindMap:     "KindMap",
	KindList:    "KindList",
	KindText:    "KindText",
}

// String returns a human-readable representation of the Kind
func (k Kind) String() string {
	if s, ok := kindDescriptions[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%v)", uint(k))
}

// ChangeHash is a SHA-256 hash identifying an automerge change.
type ChangeHash [32]byte

// String returns the hex-encoded form of the change hash
func (ch ChangeHash) String() string {
	return hex.EncodeToString(ch[:])
}

// NewChangeHash creates a change hash from its hex representation.
func NewChangeHash(s string) (ChangeHash, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return ChangeHash{}, err
	}
	if len(b) != 32 {
		return ChangeHash{}, fmt.Errorf("automerge.NewChangeHash: expected 32 bytes, got %v", len(b))
	}
	return *(*ChangeHash)(b), nil
}

// Value type tags matching wasi/src/value.rs
const (
	TagNull      byte = 0x00
	TagBool      byte = 0x01
	TagInt64     byte = 0x02
	TagUint64    byte = 0x03
	TagFloat64   byte = 0x04
	TagString    byte = 0x05
	TagBytes     byte = 0x06
	TagCounter   byte = 0x07
	TagTimestamp byte = 0x08
	TagMap       byte = 0x09
	TagList      byte = 0x0A
	TagText      byte = 0x0B
	TagVoid      byte = 0xFF
)

// BackendValue represents a typed value returned from the backend.
type BackendValue struct {
	Kind Kind
	Val  any       // Go-native value for primitives; nil for objects
	Obj  ObjHandle // valid only for KindMap/KindList/KindText
}

// ChangeInfo holds all the metadata extracted from a raw change.
type ChangeInfo struct {
	Hash      ChangeHash
	Timestamp time.Time
	Seq       uint64
	ActorID   string
	Message   string
	Deps      []ChangeHash
}

// Backend is the internal interface that abstracts the automerge engine.
type Backend interface {
	// Lifecycle
	Close(ctx context.Context) error

	// Document serialization
	Save(ctx context.Context) ([]byte, error)

	// Actor
	GetActorID(ctx context.Context) (string, error)
	SetActorID(ctx context.Context, id string) error

	// Map operations
	MapGet(ctx context.Context, obj ObjHandle, key string) (*BackendValue, error)
	MapPut(ctx context.Context, obj ObjHandle, key string, tag byte, payload []byte) (ObjHandle, error)
	MapDelete(ctx context.Context, obj ObjHandle, key string) error
	MapIncrement(ctx context.Context, obj ObjHandle, key string, delta int64) error

	// List operations
	ListGet(ctx context.Context, obj ObjHandle, index uint) (*BackendValue, error)
	ListPut(ctx context.Context, obj ObjHandle, index uint, insert bool, tag byte, payload []byte) (ObjHandle, error)
	ListDelete(ctx context.Context, obj ObjHandle, index uint) error
	ListLen(ctx context.Context, obj ObjHandle) (uint, error)
	ListIncrement(ctx context.Context, obj ObjHandle, index uint, delta int64) error

	// Text operations
	TextGet(ctx context.Context, obj ObjHandle) (string, error)
	TextSplice(ctx context.Context, obj ObjHandle, pos uint, del int, text string) error

	// Rich text marks
	Mark(ctx context.Context, obj ObjHandle, name string, value string, start, end uint, expand uint8) error
	Unmark(ctx context.Context, obj ObjHandle, name string, start, end uint, expand uint8) error
	Marks(ctx context.Context, obj ObjHandle) (string, error)

	// Spans and blocks
	Spans(ctx context.Context, obj ObjHandle) (string, error)
	SplitBlock(ctx context.Context, obj ObjHandle, index uint) (ObjHandle, error)
	JoinBlock(ctx context.Context, obj ObjHandle, index uint) error
	ReplaceBlock(ctx context.Context, obj ObjHandle, index uint) (ObjHandle, error)

	// Cursors
	GetCursor(ctx context.Context, obj ObjHandle, index uint) (string, error)
	LookupCursor(ctx context.Context, obj ObjHandle, cursor string) (uint, error)

	// Object introspection
	ObjSize(ctx context.Context, obj ObjHandle) (uint, error)
	ObjType(ctx context.Context, obj ObjHandle) (Kind, error)
	ObjKeys(ctx context.Context, obj ObjHandle) ([]string, error)
	ObjItems(ctx context.Context, obj ObjHandle) ([]*BackendValue, error)
	ObjFree(ctx context.Context, obj ObjHandle) error

	// Commit
	Commit(ctx context.Context, msg string, timestamp int64) (ChangeHash, error)
	EmptyChange(ctx context.Context, msg string, timestamp int64) (ChangeHash, error)

	// History
	Heads(ctx context.Context) ([]ChangeHash, error)
	GetChanges(ctx context.Context, since []ChangeHash) ([][]byte, error)
	GetChangeByHash(ctx context.Context, hash ChangeHash) ([]byte, error)
	ApplyChanges(ctx context.Context, changes [][]byte) error

	// Change introspection
	GetChangeInfo(ctx context.Context, raw []byte) (*ChangeInfo, error)

	// Incremental save/load
	SaveIncremental(ctx context.Context) ([]byte, error)
	LoadIncremental(ctx context.Context, data []byte) error

	// Fork & merge
	Fork(ctx context.Context, heads []ChangeHash) (Backend, error)
	Merge(ctx context.Context, otherSaved []byte) error

	// Sync protocol
	SyncInit(ctx context.Context) (uint32, error)
	SyncFree(ctx context.Context, peerID uint32) error
	SyncGenerateMessage(ctx context.Context, peerID uint32) ([]byte, bool, error)
	SyncReceiveMessage(ctx context.Context, peerID uint32, msg []byte) error
	SyncSave(ctx context.Context, peerID uint32) ([]byte, error)
	SyncLoad(ctx context.Context, data []byte) (uint32, error)
}

// --- Value encoding helpers ---

func EncodeNull() (byte, []byte) { return TagNull, nil }
func EncodeBool(v bool) (byte, []byte) {
	if v {
		return TagBool, []byte{1}
	}
	return TagBool, []byte{0}
}
func EncodeStr(v string) (byte, []byte)   { return TagString, []byte(v) }
func EncodeBytes(v []byte) (byte, []byte) { return TagBytes, v }
func EncodeInt64(v int64) (byte, []byte) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(v))
	return TagInt64, buf
}
func EncodeUint64(v uint64) (byte, []byte) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, v)
	return TagUint64, buf
}
func EncodeFloat64(v float64) (byte, []byte) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, math.Float64bits(v))
	return TagFloat64, buf
}
func EncodeTimestamp(v time.Time) (byte, []byte) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(v.UnixMilli()))
	return TagTimestamp, buf
}
func EncodeCounter(v int64) (byte, []byte) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(v))
	return TagCounter, buf
}

// DecodeValue decodes a tagged value buffer into a BackendValue.
func DecodeValue(data []byte) (*BackendValue, error) {
	if len(data) == 0 {
		return &BackendValue{Kind: KindVoid}, nil
	}
	tag := data[0]
	payload := data[1:]
	switch tag {
	case TagVoid:
		return &BackendValue{Kind: KindVoid}, nil
	case TagNull:
		return &BackendValue{Kind: KindNull, Val: nil}, nil
	case TagBool:
		if len(payload) < 1 {
			return nil, fmt.Errorf("automerge: bool payload too short")
		}
		return &BackendValue{Kind: KindBool, Val: payload[0] != 0}, nil
	case TagInt64:
		if len(payload) < 8 {
			return nil, fmt.Errorf("automerge: int64 payload too short")
		}
		return &BackendValue{Kind: KindInt64, Val: int64(binary.LittleEndian.Uint64(payload[:8]))}, nil
	case TagUint64:
		if len(payload) < 8 {
			return nil, fmt.Errorf("automerge: uint64 payload too short")
		}
		return &BackendValue{Kind: KindUint64, Val: binary.LittleEndian.Uint64(payload[:8])}, nil
	case TagFloat64:
		if len(payload) < 8 {
			return nil, fmt.Errorf("automerge: float64 payload too short")
		}
		return &BackendValue{Kind: KindFloat64, Val: math.Float64frombits(binary.LittleEndian.Uint64(payload[:8]))}, nil
	case TagString:
		return &BackendValue{Kind: KindStr, Val: string(payload)}, nil
	case TagBytes:
		b := make([]byte, len(payload))
		copy(b, payload)
		return &BackendValue{Kind: KindBytes, Val: b}, nil
	case TagCounter:
		if len(payload) < 8 {
			return nil, fmt.Errorf("automerge: counter payload too short")
		}
		return &BackendValue{Kind: KindCounter, Val: int64(binary.LittleEndian.Uint64(payload[:8]))}, nil
	case TagTimestamp:
		if len(payload) < 8 {
			return nil, fmt.Errorf("automerge: timestamp payload too short")
		}
		ms := int64(binary.LittleEndian.Uint64(payload[:8]))
		return &BackendValue{Kind: KindTime, Val: time.UnixMilli(ms)}, nil
	case TagMap:
		if len(payload) < 4 {
			return nil, fmt.Errorf("automerge: map handle payload too short")
		}
		h := ObjHandle(binary.LittleEndian.Uint32(payload[:4]))
		return &BackendValue{Kind: KindMap, Obj: h}, nil
	case TagList:
		if len(payload) < 4 {
			return nil, fmt.Errorf("automerge: list handle payload too short")
		}
		h := ObjHandle(binary.LittleEndian.Uint32(payload[:4]))
		return &BackendValue{Kind: KindList, Obj: h}, nil
	case TagText:
		if len(payload) < 4 {
			return nil, fmt.Errorf("automerge: text handle payload too short")
		}
		h := ObjHandle(binary.LittleEndian.Uint32(payload[:4]))
		return &BackendValue{Kind: KindText, Obj: h}, nil
	default:
		return &BackendValue{Kind: KindUnknown}, nil
	}
}

// DecodeValueList decodes concatenated tagged values.
func DecodeValueList(data []byte) ([]*BackendValue, error) {
	var items []*BackendValue
	offset := 0
	for offset < len(data) {
		tag := data[offset]
		size := TaggedValueSize(tag, data[offset:])
		if size <= 0 || offset+size > len(data) {
			break
		}
		bv, err := DecodeValue(data[offset : offset+size])
		if err != nil {
			return nil, err
		}
		items = append(items, bv)
		offset += size
	}
	return items, nil
}

// TaggedValueSize returns the total size of a tagged value (tag + payload).
func TaggedValueSize(tag byte, data []byte) int {
	switch tag {
	case TagVoid, TagNull:
		return 1
	case TagBool:
		return 2
	case TagInt64, TagUint64, TagFloat64, TagCounter, TagTimestamp:
		return 9
	case TagMap, TagList, TagText:
		return 5
	case TagString, TagBytes:
		if len(data) < 5 {
			return -1
		}
		payloadLen := int(binary.LittleEndian.Uint32(data[1:5]))
		return 5 + payloadLen
	default:
		return -1
	}
}

// ParseChangeHashes parses concatenated 32-byte hashes.
func ParseChangeHashes(data []byte) []ChangeHash {
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

// EncodeChangeHashes encodes change hashes into concatenated bytes.
func EncodeChangeHashes(hashes []ChangeHash) []byte {
	if len(hashes) == 0 {
		return nil
	}
	out := make([]byte, 0, len(hashes)*32)
	for _, h := range hashes {
		out = append(out, h[:]...)
	}
	return out
}

// ParseLengthPrefixedList parses a length-prefixed list of byte slices.
func ParseLengthPrefixedList(data []byte) [][]byte {
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

// EncodeLengthPrefixedList encodes a list of byte slices with length prefixes.
func EncodeLengthPrefixedList(items [][]byte) []byte {
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

// ParseChangeInfo parses binary change info into a ChangeInfo struct.
func ParseChangeInfo(data []byte) (*ChangeInfo, error) {
	if len(data) < 32+8+8+4 {
		return nil, fmt.Errorf("automerge: change info too short")
	}
	ci := &ChangeInfo{}
	offset := 0

	copy(ci.Hash[:], data[offset:offset+32])
	offset += 32

	ms := int64(binary.LittleEndian.Uint64(data[offset : offset+8]))
	ci.Timestamp = time.UnixMilli(ms)
	offset += 8

	ci.Seq = binary.LittleEndian.Uint64(data[offset : offset+8])
	offset += 8

	if offset+4 > len(data) {
		return nil, fmt.Errorf("automerge: change info truncated at actor_len")
	}
	actorLen := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if offset+actorLen > len(data) {
		return nil, fmt.Errorf("automerge: change info truncated at actor")
	}
	ci.ActorID = string(data[offset : offset+actorLen])
	offset += actorLen

	if offset+4 > len(data) {
		return nil, fmt.Errorf("automerge: change info truncated at message_len")
	}
	msgLen := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if offset+msgLen > len(data) {
		return nil, fmt.Errorf("automerge: change info truncated at message")
	}
	ci.Message = string(data[offset : offset+msgLen])
	offset += msgLen

	if offset+4 > len(data) {
		return nil, fmt.Errorf("automerge: change info truncated at deps_count")
	}
	depsCount := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
	offset += 4
	ci.Deps = make([]ChangeHash, depsCount)
	for i := 0; i < depsCount; i++ {
		if offset+32 > len(data) {
			return nil, fmt.Errorf("automerge: change info truncated at dep %d", i)
		}
		copy(ci.Deps[i][:], data[offset:offset+32])
		offset += 32
	}

	return ci, nil
}
