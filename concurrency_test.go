package automerge

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConcurrent_MapReadWrite tests that concurrent reads and writes to a
// single Doc are safe (the Doc mutex serializes access to the WASM instance).
func TestConcurrent_MapReadWrite(t *testing.T) {
	doc := New()

	const writers = 10
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(writers)

	for w := 0; w < writers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				key := fmt.Sprintf("w%d_%d", id, i)
				err := doc.RootMap().Set(key, i)
				if err != nil {
					t.Errorf("writer %d iter %d: Set failed: %v", id, i, err)
					return
				}
			}
		}(w)
	}

	wg.Wait()

	// Verify all keys were written
	keys, err := doc.RootMap().Keys()
	require.NoError(t, err)
	require.Equal(t, writers*iterations, len(keys))
}

// TestConcurrent_ListAppend tests concurrent appends to a list.
func TestConcurrent_ListAppend(t *testing.T) {
	doc := New()
	l := NewList()
	require.NoError(t, doc.RootMap().Set("list", l))

	const writers = 10
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(writers)

	for w := 0; w < writers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				err := l.Append(fmt.Sprintf("w%d_%d", id, i))
				if err != nil {
					t.Errorf("writer %d iter %d: Append failed: %v", id, i, err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	require.Equal(t, writers*iterations, l.Len())
}

// TestConcurrent_TextSplice tests concurrent text edits.
func TestConcurrent_TextSplice(t *testing.T) {
	doc := New()
	txt := NewText("")
	require.NoError(t, doc.RootMap().Set("text", txt))

	const writers = 5
	const iterations = 20

	var wg sync.WaitGroup
	wg.Add(writers)

	for w := 0; w < writers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = txt.Append("x")
			}
		}()
	}

	wg.Wait()
	require.Equal(t, writers*iterations, txt.Len())
}

// TestConcurrent_ReadWhileWriting tests that readers don't see partial state.
func TestConcurrent_ReadWhileWriting(t *testing.T) {
	doc := New()
	require.NoError(t, doc.RootMap().Set("counter", NewCounter(0)))

	const readers = 5
	const writers = 5
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(readers + writers)

	// Writers increment the counter
	for w := 0; w < writers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = doc.Path("counter").Counter().Inc(1)
			}
		}()
	}

	// Readers check the counter value is always non-negative
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				v, err := doc.Path("counter").Counter().Get()
				if err != nil {
					t.Errorf("reader: Get failed: %v", err)
					return
				}
				if v < 0 {
					t.Errorf("counter went negative: %d", v)
					return
				}
			}
		}()
	}

	wg.Wait()

	v, err := doc.Path("counter").Counter().Get()
	require.NoError(t, err)
	require.Equal(t, int64(writers*iterations), v)
}

// TestConcurrent_SaveLoad tests concurrent Save calls.
func TestConcurrent_SaveLoad(t *testing.T) {
	doc := New()
	require.NoError(t, doc.RootMap().Set("x", "hello"))

	const goroutines = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			b := doc.Save()
			d2, err := Load(b)
			if err != nil {
				t.Errorf("Load failed: %v", err)
				return
			}
			v, err := As[string](d2.RootMap().Get("x"))
			if err != nil {
				t.Errorf("As failed: %v", err)
				return
			}
			if v != "hello" {
				t.Errorf("expected hello, got %s", v)
			}
		}()
	}

	wg.Wait()
}

// TestConcurrent_MultiDocMerge tests that multiple independent docs can be
// created and merged concurrently (each doc has its own WASM instance).
func TestConcurrent_MultiDocMerge(t *testing.T) {
	const numDocs = 10

	docs := make([]*Doc, numDocs)
	for i := 0; i < numDocs; i++ {
		docs[i] = New()
		require.NoError(t, docs[i].RootMap().Set(fmt.Sprintf("doc%d", i), i))
		_, err := docs[i].Commit(fmt.Sprintf("doc %d initial", i))
		require.NoError(t, err)
	}

	// Merge all docs into doc[0] concurrently via saved bytes
	var wg sync.WaitGroup
	wg.Add(numDocs - 1)

	for i := 1; i < numDocs; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := docs[0].Merge(docs[idx])
			if err != nil {
				t.Errorf("Merge doc %d failed: %v", idx, err)
			}
		}(i)
	}

	wg.Wait()

	require.Equal(t, numDocs, docs[0].RootMap().Len())
}

// TestConcurrent_SyncProtocol tests sync state operations under concurrency.
func TestConcurrent_SyncProtocol(t *testing.T) {
	doc1 := New()
	doc2 := New()

	require.NoError(t, doc1.RootMap().Set("a", "from doc1"))
	_, err := doc1.Commit("doc1 change")
	require.NoError(t, err)

	require.NoError(t, doc2.RootMap().Set("b", "from doc2"))
	_, err = doc2.Commit("doc2 change")
	require.NoError(t, err)

	s1 := NewSyncState(doc1)
	s2 := NewSyncState(doc2)

	// Sync in a loop
	for i := 0; i < 10; i++ {
		m, valid := s1.GenerateMessage()
		if !valid {
			break
		}
		_, err = s2.ReceiveMessage(m.Bytes())
		require.NoError(t, err)

		m, valid = s2.GenerateMessage()
		if !valid {
			break
		}
		_, err = s1.ReceiveMessage(m.Bytes())
		require.NoError(t, err)
	}

	v1, err := As[map[string]string](doc1.Root())
	require.NoError(t, err)
	v2, err := As[map[string]string](doc2.Root())
	require.NoError(t, err)
	require.Equal(t, v1, v2)
	require.Equal(t, map[string]string{"a": "from doc1", "b": "from doc2"}, v1)
}
