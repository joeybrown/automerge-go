package automerge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/joeybrown/automerge-go/internal/backend"
	wazerobackend "github.com/joeybrown/automerge-go/internal/wazero"
)

// Doc represents an automerge document. You can read and write the
// values of the document with [Doc.Root], [Doc.RootMap] or [Doc.Path],
// and other methods are provided to enable collaboration and accessing
// historical data.
// After writing to the document you should immediately call [Doc.Commit] to
// explicitly create a [Change], though if you forget to do this most methods
// on a document will create an anonymous change on your behalf.
type Doc struct {
	b backend.Backend
	m sync.Mutex
}

func (d *Doc) lock() (backend.Backend, func()) {
	d.m.Lock()
	locked := true
	return d.b, func() {
		if locked {
			locked = false
			d.m.Unlock()
		}
	}
}

// New creates a new empty document
func New() *Doc {
	ctx := context.Background()
	b, err := wazerobackend.NewBackend(ctx)
	if err != nil {
		panic(fmt.Errorf("automerge.New: %w", err))
	}
	return &Doc{b: b}
}

// Load loads a document from its serialized form
func Load(raw []byte) (*Doc, error) {
	ctx := context.Background()
	b, err := wazerobackend.LoadBackend(ctx, raw)
	if err != nil {
		return nil, err
	}
	return &Doc{b: b}, nil
}

// Save exports a document to its serialized form
func (d *Doc) Save() []byte {
	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	data, err := b.Save(ctx)
	if err != nil {
		panic(fmt.Errorf("automerge.Doc.Save: %w", err))
	}
	return data
}

// RootMap returns the root of the document as a Map
func (d *Doc) RootMap() *Map {
	return &Map{doc: d, handle: backend.RootObjHandle}
}

// Root returns the root of the document as a Value
// of [KindMap]
func (d *Doc) Root() *Value {
	return &Value{kind: KindMap, doc: d, val: d.RootMap()}
}

// Path returns a [*Path] that points to a position in the doc.
// Path will panic unless each path component is a string or an int.
// Calling Path with no arguments returns a path to the [Doc.Root].
func (d *Doc) Path(path ...any) *Path {
	return (&Path{d: d}).Path(path...)
}

// CommitOptions are (rarer) options passed to commit.
// If Time is not set then time.Now() is used. To omit a timestamp pass a pointer to the zero time: &time.Time{}
// If AllowEmpty is not set then commits with no operations will error.
type CommitOptions struct {
	Time       *time.Time
	AllowEmpty bool
}

// Commit adds a new version to the document with all operations so far.
// The returned ChangeHash is the new head of the document.
// Note: You should call commit immediately after modifying the document
// as most methods that inspect or modify the documents' history
// will automatically commit any outstanding changes.
func (d *Doc) Commit(msg string, opts ...CommitOptions) (ChangeHash, error) {
	b, unlock := d.lock()
	defer unlock()

	allowEmpty := false
	ts := time.Now()
	for _, o := range opts {
		if o.AllowEmpty {
			allowEmpty = true
		}
		if o.Time != nil {
			ts = *o.Time
		}
	}

	var millis int64
	if !ts.IsZero() {
		millis = ts.UnixMilli()
	}

	ctx := context.Background()
	ch, err := b.Commit(ctx, msg, millis)
	if err != nil {
		if !errors.Is(err, backend.ErrEmptyCommit) {
			return ChangeHash{}, err
		}
		if !allowEmpty {
			return ChangeHash{}, fmt.Errorf("Commit is empty")
		}
		ch, err = b.EmptyChange(ctx, msg, millis)
		if err != nil {
			return ChangeHash{}, err
		}
	}
	return ch, nil
}

// Heads returns the hashes of the current heads for the document.
func (d *Doc) Heads() []ChangeHash {
	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	hashes, err := b.Heads(ctx)
	if err != nil {
		panic(fmt.Errorf("automerge.Doc.Heads: %w", err))
	}
	return hashes
}

// Change gets a specific change by hash.
func (d *Doc) Change(ch ChangeHash) (*Change, error) {
	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	raw, err := b.GetChangeByHash(ctx, ch)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("hash %s does not correspond to a change in this document", ch)
	}

	info, err := b.GetChangeInfo(ctx, raw)
	if err != nil {
		return nil, err
	}
	return &Change{raw: raw, info: info}, nil
}

// Changes returns all changes made to the doc since the given heads.
// If since is empty, returns all changes to recreate the document.
func (d *Doc) Changes(since ...ChangeHash) ([]*Change, error) {
	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	rawChanges, err := b.GetChanges(ctx, since)
	if err != nil {
		return nil, err
	}

	changes := make([]*Change, len(rawChanges))
	for i, raw := range rawChanges {
		info, err := b.GetChangeInfo(ctx, raw)
		if err != nil {
			return nil, err
		}
		changes[i] = &Change{raw: raw, info: info}
	}
	return changes, nil
}

// Apply the given change(s) to the document
func (d *Doc) Apply(chs ...*Change) error {
	if len(chs) == 0 {
		return nil
	}

	rawChanges := make([][]byte, len(chs))
	for i, ch := range chs {
		rawChanges[i] = ch.raw
	}

	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	return b.ApplyChanges(ctx, rawChanges)
}

// SaveIncremental exports the changes since the last call to [Doc.Save] or
// [Doc.SaveIncremental] for passing to [Doc.LoadIncremental] on a different doc.
func (d *Doc) SaveIncremental() []byte {
	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	data, err := b.SaveIncremental(ctx)
	if err != nil {
		panic(fmt.Errorf("automerge.Doc.SaveIncremental: %w", err))
	}
	return data
}

// LoadIncremental applies the changes exported by [Doc.SaveIncremental].
func (d *Doc) LoadIncremental(raw []byte) error {
	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	return b.LoadIncremental(ctx, raw)
}

// Fork returns a new, independent, copy of the document
// if asOf is empty then it is forked in its current state.
// otherwise it returns a version as of the given heads.
func (d *Doc) Fork(asOf ...ChangeHash) (*Doc, error) {
	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	nb, err := b.Fork(ctx, asOf)
	if err != nil {
		return nil, err
	}
	return &Doc{b: nb}, nil
}

// Merge extracts all changes from d2 that are not in d
// and then applies them to d.
func (d *Doc) Merge(d2 *Doc) ([]ChangeHash, error) {
	// Save d2 first (need its bytes for merge)
	d2Bytes := d2.Save()

	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	if err := b.Merge(ctx, d2Bytes); err != nil {
		return nil, err
	}

	// Return the new heads after merge
	hashes, err := b.Heads(ctx)
	if err != nil {
		return nil, err
	}
	return hashes, nil
}

// ActorID returns the current actorId of the doc hex-encoded
func (d *Doc) ActorID() string {
	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	id, err := b.GetActorID(ctx)
	if err != nil {
		panic(fmt.Errorf("automerge.Doc.ActorID: %w", err))
	}
	return id
}

// SetActorID updates the current actorId of the doc.
// Valid actor IDs are a string with an even number of hex-digits.
func (d *Doc) SetActorID(id string) error {
	// Validate hex
	if _, err := hex.DecodeString(id); err != nil {
		return fmt.Errorf("automerge: invalid actor ID %q: %w", id, err)
	}

	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	return b.SetActorID(ctx, id)
}

// NewActorID generates a new unique actor id.
func NewActorID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("automerge.NewActorID: %w", err))
	}
	return hex.EncodeToString(b)
}
