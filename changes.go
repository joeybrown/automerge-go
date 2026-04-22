package automerge

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"
)

// ChangeHash is a SHA-256 hash identifying an automerge change.
// Like a git commit, the hash encompasses both the changes made,
// any metadata (like commit message, or timestamp)
// and any changes on which this change depends.
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

// Change is a set of mutations to the document. It is analagous
// to a commit in a version control system like Git.
type Change struct {
	raw  []byte
	info *changeInfo
}

// ActorID identifies the actor that made the change hex-encoded
func (c *Change) ActorID() string {
	return c.info.actorID
}

// ActorSeq is 1 for the first change by a given
// actor, 2 for the next, and so on.
func (c *Change) ActorSeq() uint64 {
	return c.info.seq
}

// Hash identifies the change by the SHA-256 of its binary representation
func (c *Change) Hash() ChangeHash {
	return c.info.hash
}

// Dependencies returns the hashes of all changes that this change
// directly depends on.
func (c *Change) Dependencies() []ChangeHash {
	return c.info.deps
}

// Message returns the commit message (if any)
func (c *Change) Message() string {
	return c.info.message
}

// Timestamp returns the commit time (or the zero time if one was not set)
func (c *Change) Timestamp() time.Time {
	return c.info.timestamp
}

// Save exports the change for transferring between systems
func (c *Change) Save() []byte {
	out := make([]byte, len(c.raw))
	copy(out, c.raw)
	return out
}

// LoadChanges loads changes from bytes (see also [SaveChanges] and [Change.Save])
func LoadChanges(raw []byte) ([]*Change, error) {
	// The raw bytes may contain multiple concatenated changes.
	// We need a backend to parse change metadata.
	// Create a temporary doc for parsing.
	doc := New()
	b, unlock := doc.lock()
	defer unlock()

	// Try to load the changes into the temp doc to get them parsed
	// Format: concatenated raw change bytes
	// First, try to apply them and then get changes back
	ctx := context.Background()

	// LoadChanges in the C API uses AMchangeLoadDocument which parses
	// document-format bytes. We replicate by wrapping in length-prefix format.
	// Since we don't know boundaries of individual changes in the concatenated blob,
	// we try to apply the entire blob as a document and get changes back.
	changes, err := parseRawChanges(raw, b, ctx)
	if err != nil {
		return nil, err
	}
	return changes, nil
}

// parseRawChanges attempts to parse concatenated raw change bytes.
func parseRawChanges(raw []byte, b backend, ctx context.Context) ([]*Change, error) {
	// The raw bytes from SaveChanges are concatenated individual change bytes.
	// Each change is self-describing; we parse using the backend.
	// Use a temp doc: load the bytes as a document, then extract changes.

	// Create a new backend instance to load the bytes
	nb, err := newWazeroBackend(ctx)
	if err != nil {
		return nil, err
	}
	defer nb.close(ctx)

	// Try loading as a document
	ptr, size, err := nb.writeBytes(ctx, raw)
	if err != nil {
		return nil, err
	}
	defer nb.free(ctx, ptr, size)

	res, err := nb.call(ctx, "am_load", uint64(ptr), uint64(size))
	if err != nil {
		return nil, err
	}
	if int32(res[0]) != 0 {
		return nil, fmt.Errorf("unable to parse changes")
	}

	// Get all changes from the loaded document
	rawChanges, err := nb.getChanges(ctx, nil)
	if err != nil {
		return nil, err
	}

	changes := make([]*Change, len(rawChanges))
	for i, rc := range rawChanges {
		info, err := nb.changeInfo(ctx, rc)
		if err != nil {
			return nil, err
		}
		changes[i] = &Change{raw: rc, info: info}
	}
	return changes, nil
}

// SaveChanges saves multiple changes to bytes (see also [LoadChanges])
func SaveChanges(cs []*Change) []byte {
	out := []byte{}
	for _, c := range cs {
		out = append(out, c.Save()...)
	}
	return out
}
