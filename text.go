package automerge

import (
	"context"
	"encoding/json"
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

// ExpandMark controls how marks behave when text is inserted at their boundaries.
type ExpandMark uint8

const (
	// ExpandNone means the mark does not expand when text is inserted at its boundaries.
	ExpandNone ExpandMark = 0
	// ExpandBefore means the mark expands when text is inserted at its start.
	ExpandBefore ExpandMark = 1
	// ExpandAfter means the mark expands when text is inserted at its end.
	ExpandAfter ExpandMark = 2
	// ExpandBoth means the mark expands when text is inserted at either boundary.
	ExpandBoth ExpandMark = 3
)

// Mark represents a rich text formatting annotation on a range of text.
type Mark struct {
	Name  string
	Value string
	Start uint
	End   uint
}

// Cursor tracks a stable position in a Text that survives concurrent edits.
type Cursor struct {
	value string // opaque cursor string from automerge
}

// NewCursor creates a Cursor from its opaque string representation.
// This is useful when deserializing a cursor that was previously obtained
// via Text.GetCursor.
func NewCursor(s string) *Cursor {
	return &Cursor{value: s}
}

// String returns the opaque cursor string.
func (c *Cursor) String() string {
	if c == nil {
		return ""
	}
	return c.value
}

// Mark adds a formatting mark to a range of text.
// The mark name identifies the format (e.g. "bold", "italic", "link").
// The value is the mark's value (e.g. "true", "https://...").
// Start is inclusive and end is exclusive.
func (t *Text) Mark(name string, value string, start, end int, expand ExpandMark) error {
	if t.doc == nil {
		return fmt.Errorf("automerge.Text: tried to mark detached text")
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
	return b.Mark(ctx, t.handle, name, value, uint(start), uint(end), uint8(expand))
}

// Unmark removes a formatting mark from a range of text.
func (t *Text) Unmark(name string, start, end int, expand ExpandMark) error {
	if t.doc == nil {
		return fmt.Errorf("automerge.Text: tried to unmark detached text")
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
	return b.Unmark(ctx, t.handle, name, uint(start), uint(end), uint8(expand))
}

// Marks returns all marks currently applied to the text.
func (t *Text) Marks() ([]Mark, error) {
	if t.doc == nil {
		return nil, fmt.Errorf("automerge.Text: tried to read marks on detached text")
	}
	if t.path != nil {
		v, err := t.path.Get()
		if err != nil {
			return nil, err
		}
		if v.Kind() != KindText {
			return nil, fmt.Errorf("automerge.Text: tried to read marks on non-text value")
		}
		return v.Text().Marks()
	}

	b, unlock := t.doc.lock()
	defer unlock()

	ctx := context.Background()
	jsonStr, err := b.Marks(ctx, t.handle)
	if err != nil {
		return nil, fmt.Errorf("automerge.Text: failed to get marks: %w", err)
	}

	if jsonStr == "[]" {
		return []Mark{}, nil
	}

	var rawMarks []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		Start uint   `json:"start"`
		End   uint   `json:"end"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &rawMarks); err != nil {
		return nil, fmt.Errorf("automerge.Text: failed to parse marks JSON: %w", err)
	}

	marks := make([]Mark, len(rawMarks))
	for i, raw := range rawMarks {
		marks[i] = Mark{
			Name:  raw.Name,
			Value: raw.Value,
			Start: raw.Start,
			End:   raw.End,
		}
	}
	return marks, nil
}

// GetCursor returns a cursor pointing to the given index in the text.
// The cursor tracks a stable position that adjusts automatically when
// text is inserted or deleted before it.
func (t *Text) GetCursor(index int) (*Cursor, error) {
	if t.doc == nil {
		return nil, fmt.Errorf("automerge.Text: tried to get cursor on detached text")
	}
	if t.path != nil {
		v, err := t.path.Get()
		if err != nil {
			return nil, err
		}
		if v.Kind() != KindText {
			return nil, fmt.Errorf("automerge.Text: tried to get cursor on non-text value")
		}
		return v.Text().GetCursor(index)
	}

	b, unlock := t.doc.lock()
	defer unlock()

	ctx := context.Background()
	cursorStr, err := b.GetCursor(ctx, t.handle, uint(index))
	if err != nil {
		return nil, fmt.Errorf("automerge.Text: failed to get cursor: %w", err)
	}
	return &Cursor{value: cursorStr}, nil
}

// LookupCursor returns the current index for a previously obtained cursor.
func (t *Text) LookupCursor(c *Cursor) (int, error) {
	if t.doc == nil {
		return 0, fmt.Errorf("automerge.Text: tried to lookup cursor on detached text")
	}
	if c == nil {
		return 0, fmt.Errorf("automerge.Text: cursor is nil")
	}
	if t.path != nil {
		v, err := t.path.Get()
		if err != nil {
			return 0, err
		}
		if v.Kind() != KindText {
			return 0, fmt.Errorf("automerge.Text: tried to lookup cursor on non-text value")
		}
		return v.Text().LookupCursor(c)
	}

	b, unlock := t.doc.lock()
	defer unlock()

	ctx := context.Background()
	idx, err := b.LookupCursor(ctx, t.handle, c.value)
	if err != nil {
		return 0, fmt.Errorf("automerge.Text: failed to lookup cursor: %w", err)
	}
	return int(idx), nil
}
