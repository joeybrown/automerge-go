package automerge

import (
	"context"
	"fmt"
	"time"

	"github.com/joeybrown/automerge-go/internal/backend"
)

// Map is an automerge type that stores a map of strings to values
type Map struct {
	doc    *Doc
	handle backend.ObjHandle
	path   *Path
}

// NewMap returns a detached map.
// Before you can read from or write to it you must write it to the document.
func NewMap() *Map {
	return &Map{}
}

func (m *Map) createOnPath(key string) error {
	if m.path == nil {
		return nil
	}

	m2, err := m.path.ensureMap(key)
	if err != nil {
		return err
	}
	m.doc = m2.doc
	m.handle = m2.handle
	m.path = nil
	return nil
}

// Get retrieves the value from the map.
func (m *Map) Get(key string) (*Value, error) {
	if m.doc == nil {
		return nil, fmt.Errorf("automerge.Map: tried to read detached map")
	}
	if m.path != nil {
		return m.path.Path(key).Get()
	}

	b, unlock := m.doc.lock()
	defer unlock()

	ctx := context.Background()
	bv, err := b.MapGet(ctx, m.handle, key)
	if err != nil {
		return nil, err
	}
	return newValueInMap(bv, m, key), nil
}

// Len returns the number of keys set in the map, or 0 on error
func (m *Map) Len() int {
	if m.doc == nil {
		return 0
	}
	if m.path != nil {
		v, err := m.path.Get()
		if err != nil || v.Kind() != KindMap {
			return 0
		}
		return v.Map().Len()
	}

	b, unlock := m.doc.lock()
	defer unlock()

	ctx := context.Background()
	size, err := b.ObjSize(ctx, m.handle)
	if err != nil {
		return 0
	}
	return int(size)
}

// Delete deletes a key and its corresponding value from the map
func (m *Map) Delete(key string) error {
	if m.doc == nil {
		return fmt.Errorf("automerge.Map: tried to write to detached map")
	}
	if err := m.createOnPath(key); err != nil {
		return err
	}

	b, unlock := m.doc.lock()
	defer unlock()

	ctx := context.Background()
	return b.MapDelete(ctx, m.handle, key)
}

// Set sets a key in the map to a given value.
func (m *Map) Set(key string, value any) error {
	if m.doc == nil {
		return fmt.Errorf("automerge.Map: tried to write to detached map")
	}
	if err := m.createOnPath(key); err != nil {
		return err
	}

	value, err := normalize(value)
	if err != nil {
		return err
	}

	b, unlock := m.doc.lock()
	defer unlock()
	ctx := context.Background()

	switch v := value.(type) {
	case nil:
		tag, payload := backend.EncodeNull()
		_, err = b.MapPut(ctx, m.handle, key, tag, payload)

	case bool:
		tag, payload := backend.EncodeBool(v)
		_, err = b.MapPut(ctx, m.handle, key, tag, payload)

	case string:
		tag, payload := backend.EncodeStr(v)
		_, err = b.MapPut(ctx, m.handle, key, tag, payload)

	case []byte:
		tag, payload := backend.EncodeBytes(v)
		_, err = b.MapPut(ctx, m.handle, key, tag, payload)

	case int64:
		tag, payload := backend.EncodeInt64(v)
		_, err = b.MapPut(ctx, m.handle, key, tag, payload)

	case uint64:
		tag, payload := backend.EncodeUint64(v)
		_, err = b.MapPut(ctx, m.handle, key, tag, payload)

	case float64:
		tag, payload := backend.EncodeFloat64(v)
		_, err = b.MapPut(ctx, m.handle, key, tag, payload)

	case time.Time:
		tag, payload := backend.EncodeTimestamp(v)
		_, err = b.MapPut(ctx, m.handle, key, tag, payload)

	case []any:
		unlock()

		nl := NewList()
		if err := m.Set(key, nl); err != nil {
			return err
		}
		return nl.Append(v...)

	case map[string]any:
		unlock()

		n := NewMap()
		if err := m.Set(key, n); err != nil {
			return err
		}
		for key, val := range v {
			if err := n.Set(key, val); err != nil {
				return err
			}
		}

	case *Map:
		if m.doc != nil && v.doc != nil {
			return fmt.Errorf("automerge.Map: tried to move an existing *automerge.Map")
		}

		h, putErr := b.MapPut(ctx, m.handle, key, backend.TagMap, nil)
		if putErr != nil {
			err = putErr
			break
		}
		v.doc = m.doc
		v.handle = h

	case *List:
		if v.doc != nil {
			return fmt.Errorf("automerge.Map: tried to move an existing *automerge.List")
		}

		h, putErr := b.MapPut(ctx, m.handle, key, backend.TagList, nil)
		if putErr != nil {
			err = putErr
			break
		}
		v.doc = m.doc
		v.handle = h

	case *Counter:
		if v.m != nil || v.l != nil {
			return fmt.Errorf("automerge.Map: tried to move an existing *automerge.Counter")
		}

		tag, payload := backend.EncodeCounter(v.val)
		_, err = b.MapPut(ctx, m.handle, key, tag, payload)
		if err == nil {
			v.m = m
			v.key = key
		}

	case *Text:
		if v.doc != nil {
			return fmt.Errorf("automerge.Map: tried to move an existing *automerge.Text")
		}
		h, putErr := b.MapPut(ctx, m.handle, key, backend.TagText, nil)
		if putErr != nil {
			err = putErr
			break
		}
		v.doc = m.doc
		v.handle = h
		unlock()
		if err = v.Set(v.val); err != nil {
			return err
		}

	default:
		err = fmt.Errorf("automerge.Map: tried to write unsupported value %#v", value)
	}

	return err
}

func (m *Map) inc(key string, delta int64) error {
	b, unlock := m.doc.lock()
	defer unlock()

	ctx := context.Background()
	return b.MapIncrement(ctx, m.handle, key, delta)
}

// Values returns the values of the map
func (m *Map) Values() (map[string]*Value, error) {
	if m.doc == nil {
		return nil, fmt.Errorf("automerge.Map: tried to read detached map")
	}
	if m.path != nil {
		v, err := m.path.Get()
		if err != nil {
			return nil, err
		}
		switch v.Kind() {
		case KindMap:
			return v.Map().Values()
		case KindVoid:
			return nil, nil
		default:
			return nil, fmt.Errorf("%#v: tried to read non-map %#v", m.path, v.val)
		}
	}

	b, unlock := m.doc.lock()
	defer unlock()

	ctx := context.Background()

	keys, err := b.ObjKeys(ctx, m.handle)
	if err != nil {
		return nil, err
	}

	ret := map[string]*Value{}
	for _, key := range keys {
		bv, err := b.MapGet(ctx, m.handle, key)
		if err != nil {
			return nil, err
		}
		ret[key] = newValueInMap(bv, m, key)
	}
	return ret, nil
}

// Keys returns the current list of keys for the map
func (m *Map) Keys() ([]string, error) {
	v, err := m.Values()
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for k := range v {
		keys = append(keys, k)
	}
	return keys, nil
}

// GoString returns a representation suitable for debugging.
func (m *Map) GoString() string {
	if m.doc == nil {
		return "&automerge.Map{}"
	}
	values, err := m.Values()
	if err != nil {
		return "&automerge.Map{<error>}"
	}

	sofar := "&automerge.Map{"
	i := 0
	for k, v := range values {
		if i > 0 {
			sofar += ", "
		}
		i++
		if v.Kind() == KindMap {
			sofar += fmt.Sprintf("%#v: &automerge.Map{...}", k)
		} else if v.Kind() == KindList {
			sofar += fmt.Sprintf("%#v: &automerge.List{...}", k)
		} else {
			sofar += fmt.Sprintf("%#v: %#v", k, v.val)
		}

		if i >= 5 {
			sofar += ", ..."
			break
		}
	}

	return sofar + "}"
}

// String returns a representation suitable for debugging.
func (m *Map) String() string {
	return m.GoString()
}
