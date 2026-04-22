package automerge_test

import (
	"fmt"
	"testing"

	"github.com/automerge/automerge-go"
)

// BenchmarkNewDoc measures the cost of creating a new document (includes WASM
// module instantiation).
func BenchmarkNewDoc(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = automerge.New()
	}
}

// BenchmarkMapSet measures the cost of setting a key in a map.
func BenchmarkMapSet(b *testing.B) {
	doc := automerge.New()
	m := doc.RootMap()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Set(fmt.Sprintf("key%d", i), i)
	}
}

// BenchmarkMapGet measures the cost of getting a key from a map.
func BenchmarkMapGet(b *testing.B) {
	doc := automerge.New()
	m := doc.RootMap()
	_ = m.Set("key", "value")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = m.Get("key")
	}
}

// BenchmarkListAppend measures the cost of appending to a list.
func BenchmarkListAppend(b *testing.B) {
	doc := automerge.New()
	l := automerge.NewList()
	_ = doc.RootMap().Set("list", l)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = l.Append(i)
	}
}

// BenchmarkTextSplice measures the cost of appending to text.
func BenchmarkTextSplice(b *testing.B) {
	doc := automerge.New()
	txt := automerge.NewText("")
	_ = doc.RootMap().Set("text", txt)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = txt.Append("x")
	}
}

// BenchmarkSaveSmall measures saving a small document.
func BenchmarkSaveSmall(b *testing.B) {
	doc := automerge.New()
	_ = doc.RootMap().Set("x", "hello")
	_, _ = doc.Commit("init")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = doc.Save()
	}
}

// BenchmarkSaveLarge measures saving a document with many keys.
func BenchmarkSaveLarge(b *testing.B) {
	doc := automerge.New()
	for i := 0; i < 1000; i++ {
		_ = doc.RootMap().Set(fmt.Sprintf("key%d", i), i)
	}
	_, _ = doc.Commit("init")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = doc.Save()
	}
}

// BenchmarkLoadSmall measures loading a small document.
func BenchmarkLoadSmall(b *testing.B) {
	doc := automerge.New()
	_ = doc.RootMap().Set("x", "hello")
	_, _ = doc.Commit("init")
	data := doc.Save()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = automerge.Load(data)
	}
}

// BenchmarkLoadLarge measures loading a document with many keys.
func BenchmarkLoadLarge(b *testing.B) {
	doc := automerge.New()
	for i := 0; i < 1000; i++ {
		_ = doc.RootMap().Set(fmt.Sprintf("key%d", i), i)
	}
	_, _ = doc.Commit("init")
	data := doc.Save()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = automerge.Load(data)
	}
}

// BenchmarkCommit measures the cost of committing changes.
func BenchmarkCommit(b *testing.B) {
	doc := automerge.New()
	for i := 0; i < b.N; i++ {
		_ = doc.RootMap().Set("x", i)
		_, _ = doc.Commit("change", automerge.CommitOptions{AllowEmpty: true})
	}
}

// BenchmarkSyncRoundTrip measures the cost of a full sync round-trip between
// two documents.
func BenchmarkSyncRoundTrip(b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc1 := automerge.New()
		doc2 := automerge.New()
		_ = doc1.RootMap().Set("a", "hello")
		_, _ = doc1.Commit("c1")

		s1 := automerge.NewSyncState(doc1)
		s2 := automerge.NewSyncState(doc2)

		for {
			m, valid := s1.GenerateMessage()
			if !valid {
				break
			}
			_, _ = s2.ReceiveMessage(m.Bytes())

			m, valid = s2.GenerateMessage()
			if !valid {
				break
			}
			_, _ = s1.ReceiveMessage(m.Bytes())
		}
	}
}

// BenchmarkMerge measures the cost of merging two documents.
func BenchmarkMerge(b *testing.B) {
	base := automerge.New()
	_ = base.RootMap().Set("shared", true)
	_, _ = base.Commit("base")
	baseBytes := base.Save()

	for i := 0; i < b.N; i++ {
		doc1, _ := automerge.Load(baseBytes)
		doc2, _ := automerge.Load(baseBytes)
		_ = doc1.RootMap().Set("a", i)
		_ = doc2.RootMap().Set("b", i)
		_, _ = doc1.Commit("d1")
		_, _ = doc2.Commit("d2")
		_, _ = doc1.Merge(doc2)
	}
}

// BenchmarkSaveIncremental measures incremental save cost.
func BenchmarkSaveIncremental(b *testing.B) {
	doc := automerge.New()
	_ = doc.RootMap().Set("x", 0)
	_, _ = doc.Commit("init")
	_ = doc.Save() // establish baseline

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = doc.RootMap().Set("x", i)
		_, _ = doc.Commit("update")
		_ = doc.SaveIncremental()
	}
}

// BenchmarkCounterInc measures the cost of incrementing a counter.
func BenchmarkCounterInc(b *testing.B) {
	doc := automerge.New()
	c := automerge.NewCounter(0)
	_ = doc.RootMap().Set("c", c)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.Inc(1)
	}
}
