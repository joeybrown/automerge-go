package automerge

import (
	"context"
	"time"

	"github.com/joeybrown/automerge-go/internal/backend"
	wazerobackend "github.com/joeybrown/automerge-go/internal/wazero"
)

// ChangeHash is a SHA-256 hash identifying an automerge change.
// Like a git commit, the hash encompasses both the changes made,
// any metadata (like commit message, or timestamp)
// and any changes on which this change depends.
type ChangeHash = backend.ChangeHash

// NewChangeHash creates a change hash from its hex representation.
var NewChangeHash = backend.NewChangeHash

// Change is a set of mutations to the document. It is analagous
// to a commit in a version control system like Git.
type Change struct {
	raw  []byte
	info *backend.ChangeInfo
}

// ActorID identifies the actor that made the change hex-encoded
func (c *Change) ActorID() string {
	return c.info.ActorID
}

// ActorSeq is 1 for the first change by a given
// actor, 2 for the next, and so on.
func (c *Change) ActorSeq() uint64 {
	return c.info.Seq
}

// Hash identifies the change by the SHA-256 of its binary representation
func (c *Change) Hash() ChangeHash {
	return c.info.Hash
}

// Dependencies returns the hashes of all changes that this change
// directly depends on.
func (c *Change) Dependencies() []ChangeHash {
	return c.info.Deps
}

// Message returns the commit message (if any)
func (c *Change) Message() string {
	return c.info.Message
}

// Timestamp returns the commit time (or the zero time if one was not set)
func (c *Change) Timestamp() time.Time {
	return c.info.Timestamp
}

// Save exports the change for transferring between systems
func (c *Change) Save() []byte {
	out := make([]byte, len(c.raw))
	copy(out, c.raw)
	return out
}

// LoadChanges loads changes from bytes (see also [SaveChanges] and [Change.Save])
func LoadChanges(raw []byte) ([]*Change, error) {
	ctx := context.Background()
	rawChanges, infos, err := wazerobackend.ParseRawChanges(ctx, raw)
	if err != nil {
		return nil, err
	}
	changes := make([]*Change, len(rawChanges))
	for i := range rawChanges {
		changes[i] = &Change{raw: rawChanges[i], info: infos[i]}
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
