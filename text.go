package automerge

import (
	"context"
	"fmt"

	"github.com/joeybrown/automerge-go/internal/backend"
)

// Text is a mutable unicode string that can be edited collaboratively.
//
// Note that automerge considers text to be a sequence of unicode codepoints
// while most go code treats strings as a sequence of bytes (that are hopefully valid utf8).
// When editing Text you must pass positions and counts in terms of codepoints not bytes.
type Text struct {
	doc    *Doc
	handle backend.ObjHandle
	path   *Path

	val string
}

// NewText returns a detached Text with the given starting value.
// Before you can read or write it you must write it to the document.
func NewText(s string) *Text {
	return &Text{val: s}
}

// Len returns number of unicode codepoints in the text, this
// may be less than the number of utf-8 bytes.
func (t *Text) Len() int {
	if t.doc == nil {
		return 0
	}
	if t.path != nil {
		v, err := t.path.Get()
		if err != nil || v.Kind() != KindText {
			return 0
		}
		return v.Text().Len()
	}

	b, unlock := t.doc.lock()
	defer unlock()

	ctx := context.Background()
	size, err := b.ObjSize(ctx, t.handle)
	if err != nil {
		return 0
	}
	return int(size)
}

// Get returns the current value as a string
func (t *Text) Get() (string, error) {
	if t.doc == nil {
		return "", fmt.Errorf("automerge.Text: tried to read detached text")
	}
	if t.path != nil {
		v, err := t.path.Get()
		if err != nil {
			return "", err
		}
		switch v.Kind() {
		case KindVoid:
			return "", nil
		case KindText:
			return v.Text().Get()
		default:
			return "", fmt.Errorf("automerge.Text: tried to read non-text value %#v", v.val)
		}
	}

	b, unlock := t.doc.lock()
	defer unlock()

	ctx := context.Background()
	return b.TextGet(ctx, t.handle)
}

// Set overwrites the entire string with a new value,
// prefer to use Insert/Delete/Append/Splice as appropriate
// to preserves collaborators changes.
func (t *Text) Set(s string) error {
	// Delete all existing content and insert new
	length := t.Len()
	return t.splice(0, length, s)
}

// Insert adds a substr at position pos in the Text
func (t *Text) Insert(pos int, s string) error {
	return t.splice(uint(pos), 0, s)
}

// Delete deletes del runes from position pos
func (t *Text) Delete(pos int, del int) error {
	return t.splice(uint(pos), del, "")
}

// Append adds substr s at the end of the string
func (t *Text) Append(s string) error {
	length := t.Len()
	return t.splice(uint(length), 0, s)
}

// Splice deletes del runes at position pos, and inserts
// substr s in their place.
func (t *Text) Splice(pos int, del int, s string) error {
	return t.splice(uint(pos), del, s)
}

func (t *Text) splice(pos uint, del int, s string) error {
	if t.doc == nil {
		return fmt.Errorf("automerge.Text: tried to write to detached text")
	}
	if t.path != nil {
		t2, err := t.path.ensureText()
		if err != nil {
			return err
		}
		t.doc = t2.doc
		t.handle = t2.handle
		t.path = nil
	}

	b, unlock := t.doc.lock()
	defer unlock()

	ctx := context.Background()
	err := b.TextSplice(ctx, t.handle, pos, del, s)
	if err != nil {
		return fmt.Errorf("automerge.Text: failed to write: %w", err)
	}
	return nil
}

// GoString returns a representation suitable for debugging.
func (t *Text) GoString() string {
	if t.doc == nil {
		return fmt.Sprintf("&automerge.Text{%#v}", t.val)
	}
	v, err := t.Get()
	if err != nil {
		return "&automerge.Text{<error>}"
	}
	return fmt.Sprintf("&automerge.Text{%#v}", v)
}

// String returns a representation suitable for debugging.
func (t *Text) String() string {
	return t.GoString()
}
