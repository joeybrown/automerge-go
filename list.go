package automerge

import (
	"context"
	"fmt"
	"time"

	"github.com/joeybrown/automerge-go/internal/backend"
)

// List is an automerge type that stores a list of [Value]'s
type List struct {
	doc    *Doc
	handle backend.ObjHandle
	path   *Path
}

// NewList returns a detached list.
// Before you can read from or write to it you must write it to the document.
func NewList() *List {
	return &List{}
}

// Len returns the length of the list, or 0 on error
func (l *List) Len() int {
	if l.doc == nil {
		return 0
	}
	if l.path != nil {
		v, err := l.path.Get()
		if err != nil || v.Kind() != KindList {
			return 0
		}
		return v.List().Len()
	}

	b, unlock := l.doc.lock()
	defer unlock()

	ctx := context.Background()
	size, err := b.ListLen(ctx, l.handle)
	if err != nil {
		return 0
	}
	return int(size)
}

// Values returns a slice of the values in a list
func (l *List) Values() ([]*Value, error) {
	if l.doc == nil {
		return nil, fmt.Errorf("automerge.List: tried to read detached list")
	}
	if l.path != nil {
		v, err := l.path.Get()
		if err != nil {
			return nil, err
		}
		switch v.Kind() {
		case KindList:
			return v.List().Values()
		case KindVoid:
			return nil, nil
		default:
			return nil, fmt.Errorf("%#v: tried to read non-list %#v", l.path, v.val)
		}
	}

	b, unlock := l.doc.lock()
	defer unlock()

	ctx := context.Background()
	length, err := b.ListLen(ctx, l.handle)
	if err != nil {
		return nil, err
	}

	ret := make([]*Value, 0, length)
	for i := uint(0); i < length; i++ {
		bv, err := b.ListGet(ctx, l.handle, i)
		if err != nil {
			return nil, err
		}
		ret = append(ret, newValueInList(bv, l, int(i)))
	}
	return ret, nil
}

// Get returns the value at index i
func (l *List) Get(i int) (*Value, error) {
	if l.doc == nil {
		return nil, fmt.Errorf("automerge.List: tried to read detached list")
	}
	if l.path != nil {
		return l.path.Path(i).Get()
	}

	if i < 0 || i >= l.Len() {
		return &Value{kind: KindVoid}, nil
	}

	b, unlock := l.doc.lock()
	defer unlock()

	ctx := context.Background()
	bv, err := b.ListGet(ctx, l.handle, uint(i))
	if err != nil {
		return nil, err
	}
	return newValueInList(bv, l, i), nil
}

// Append adds the values at the end of the list.
func (l *List) Append(values ...any) error {
	for _, v := range values {
		length := l.Len()
		if err := l.put(uint(length), true, v); err != nil {
			return err
		}
	}
	return nil
}

// Set overwrites the value at l[idx] with value.
func (l *List) Set(idx int, value any) error {
	if idx < 0 || idx >= l.Len() {
		return fmt.Errorf("automerge.List: tried to write index %v beyond end of list length %v", idx, l.Len())
	}
	return l.put(uint(idx), false, value)
}

// Insert inserts the new values just before idx.
func (l *List) Insert(idx int, value ...any) error {
	if idx < 0 || idx > l.Len() {
		return fmt.Errorf("automerge.List: tried to write index %v beyond end of list length %v", idx, l.Len())
	}
	for i, v := range value {
		if err := l.put(uint(idx+i), true, v); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes the value at idx and shortens the list.
func (l *List) Delete(idx int) error {
	if idx < 0 || idx >= l.Len() {
		return fmt.Errorf("automerge.List: tried to write index %v beyond end of list length %v", idx, l.Len())
	}

	b, unlock := l.doc.lock()
	defer unlock()

	ctx := context.Background()
	return b.ListDelete(ctx, l.handle, uint(idx))
}

func (l *List) inc(i int, delta int64) error {
	b, unlock := l.doc.lock()
	defer unlock()

	ctx := context.Background()
	return b.ListIncrement(ctx, l.handle, uint(i), delta)
}

func (l *List) put(i uint, before bool, value any) error {
	if l.doc == nil {
		return fmt.Errorf("automerge.List: tried to write to detached list")
	}
	if l.path != nil {
		l2, err := l.path.ensureList(int(i))
		if err != nil {
			return err
		}
		l.doc = l2.doc
		l.handle = l2.handle
		l.path = nil
	}

	value, err := normalize(value)
	if err != nil {
		return err
	}

	b, unlock := l.doc.lock()
	defer unlock()
	ctx := context.Background()

	switch v := value.(type) {
	case nil:
		tag, payload := backend.EncodeNull()
		_, err = b.ListPut(ctx, l.handle, i, before, tag, payload)
	case bool:
		tag, payload := backend.EncodeBool(v)
		_, err = b.ListPut(ctx, l.handle, i, before, tag, payload)
	case string:
		tag, payload := backend.EncodeStr(v)
		_, err = b.ListPut(ctx, l.handle, i, before, tag, payload)
	case []byte:
		tag, payload := backend.EncodeBytes(v)
		_, err = b.ListPut(ctx, l.handle, i, before, tag, payload)
	case int64:
		tag, payload := backend.EncodeInt64(v)
		_, err = b.ListPut(ctx, l.handle, i, before, tag, payload)
	case uint64:
		tag, payload := backend.EncodeUint64(v)
		_, err = b.ListPut(ctx, l.handle, i, before, tag, payload)
	case float64:
		tag, payload := backend.EncodeFloat64(v)
		_, err = b.ListPut(ctx, l.handle, i, before, tag, payload)
	case time.Time:
		tag, payload := backend.EncodeTimestamp(v)
		_, err = b.ListPut(ctx, l.handle, i, before, tag, payload)

	case []any:
		unlock()
		nl := NewList()
		if err := l.put(i, before, nl); err != nil {
			return err
		}
		return nl.Append(v...)

	case map[string]any:
		unlock()
		nm := NewMap()
		if err := l.put(i, before, nm); err != nil {
			return err
		}
		for key, val := range v {
			if err := nm.Set(key, val); err != nil {
				return err
			}
		}

	case *Counter:
		if v.m != nil || v.l != nil {
			return fmt.Errorf("automerge.List: tried to move an attached *automerge.Counter")
		}
		tag, payload := backend.EncodeCounter(v.val)
		_, err = b.ListPut(ctx, l.handle, i, before, tag, payload)
		if err == nil {
			v.l = l
			v.idx = int(i)
		}

	case *Text:
		if v.doc != nil {
			return fmt.Errorf("automerge.List: tried to move an attached *automerge.Text")
		}
		h, putErr := b.ListPut(ctx, l.handle, i, before, backend.TagText, nil)
		if putErr != nil {
			err = putErr
			break
		}
		v.doc = l.doc
		v.handle = h
		unlock()
		if err := v.Set(v.val); err != nil {
			return err
		}

	case *Map:
		if v.doc != nil {
			return fmt.Errorf("automerge.List: tried to move an attached *automerge.Map")
		}
		h, putErr := b.ListPut(ctx, l.handle, i, before, backend.TagMap, nil)
		if putErr != nil {
			err = putErr
			break
		}
		v.doc = l.doc
		v.handle = h

	case *List:
		if v.doc != nil {
			return fmt.Errorf("automerge.List: tried to move an attached *automerge.List")
		}
		h, putErr := b.ListPut(ctx, l.handle, i, before, backend.TagList, nil)
		if putErr != nil {
			err = putErr
			break
		}
		v.doc = l.doc
		v.handle = h

	default:
		err = fmt.Errorf("automerge.List: tried to write unsupported value %#v", value)
	}

	return err
}

// GoString returns a representation suitable for debugging.
func (l *List) GoString() string {
	if l.doc == nil {
		return "&automerge.List{}"
	}
	values, err := l.Values()
	if err != nil {
		return "&automerge.List{<error>}"
	}
	sofar := "&automerge.List{"
	for i, v := range values {
		if i > 0 {
			sofar += ", "
		}
		i++
		if v.Kind() == KindMap {
			sofar += "&automerge.Map{...}"
		} else if v.Kind() == KindList {
			sofar += "&automerge.List{...}"
		} else {
			sofar += fmt.Sprintf("%#v", v.val)
		}

		if i >= 5 {
			sofar += ", ..."
			break
		}
	}

	return sofar + "}"
}

// String returns a representation suitable for debugging.
func (l *List) String() string {
	return l.GoString()
}
