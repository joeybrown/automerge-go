package automerge_test

import (
	"testing"

	"github.com/joeybrown/automerge-go"
	"github.com/stretchr/testify/require"
)

func TestText_Mark(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello, World!")
	require.NoError(t, doc.RootMap().Set("content", txt))

	// Mark "Hello" as bold
	require.NoError(t, txt.Mark("bold", "true", 0, 5, automerge.ExpandNone))

	marks, err := txt.Marks()
	require.NoError(t, err)
	require.Len(t, marks, 1)
	require.Equal(t, "bold", marks[0].Name)
	require.Equal(t, "true", marks[0].Value)
	require.Equal(t, uint(0), marks[0].Start)
	require.Equal(t, uint(5), marks[0].End)
}

func TestText_MarkMultiple(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello, World!")
	require.NoError(t, doc.RootMap().Set("content", txt))

	require.NoError(t, txt.Mark("bold", "true", 0, 5, automerge.ExpandNone))
	require.NoError(t, txt.Mark("italic", "true", 7, 12, automerge.ExpandNone))

	marks, err := txt.Marks()
	require.NoError(t, err)
	require.Len(t, marks, 2)

	names := map[string]bool{}
	for _, m := range marks {
		names[m.Name] = true
	}
	require.True(t, names["bold"])
	require.True(t, names["italic"])
}

func TestText_Unmark(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello, World!")
	require.NoError(t, doc.RootMap().Set("content", txt))

	require.NoError(t, txt.Mark("bold", "true", 0, 5, automerge.ExpandNone))

	marks, err := txt.Marks()
	require.NoError(t, err)
	require.Len(t, marks, 1)

	require.NoError(t, txt.Unmark("bold", 0, 5, automerge.ExpandNone))

	marks, err = txt.Marks()
	require.NoError(t, err)
	require.Len(t, marks, 0)
}

func TestText_MarkExpandAfter(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello")
	require.NoError(t, doc.RootMap().Set("content", txt))

	require.NoError(t, txt.Mark("bold", "true", 0, 5, automerge.ExpandAfter))

	// Append text — it should be within the bold mark
	require.NoError(t, txt.Append(" World"))

	marks, err := txt.Marks()
	require.NoError(t, err)
	require.Len(t, marks, 1)
	require.Equal(t, "bold", marks[0].Name)
	require.Equal(t, uint(0), marks[0].Start)
	require.Equal(t, uint(11), marks[0].End)
}

func TestText_MarkLinkValue(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("click here for info")
	require.NoError(t, doc.RootMap().Set("content", txt))

	require.NoError(t, txt.Mark("link", "https://example.com", 0, 10, automerge.ExpandNone))

	marks, err := txt.Marks()
	require.NoError(t, err)
	require.Len(t, marks, 1)
	require.Equal(t, "link", marks[0].Name)
	require.Equal(t, "https://example.com", marks[0].Value)
}

func TestText_MarksEmpty(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello")
	require.NoError(t, doc.RootMap().Set("content", txt))

	marks, err := txt.Marks()
	require.NoError(t, err)
	require.Len(t, marks, 0)
}

func TestText_MarksSurviveSaveLoad(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello, World!")
	require.NoError(t, doc.RootMap().Set("content", txt))
	require.NoError(t, txt.Mark("bold", "true", 0, 5, automerge.ExpandNone))
	_, err := doc.Commit("add formatting")
	require.NoError(t, err)

	saved := doc.Save()
	doc2, err := automerge.Load(saved)
	require.NoError(t, err)

	txt2, err := automerge.As[*automerge.Text](doc2.RootMap().Get("content"))
	require.NoError(t, err)

	marks, err := txt2.Marks()
	require.NoError(t, err)
	require.Len(t, marks, 1)
	require.Equal(t, "bold", marks[0].Name)
	require.Equal(t, "true", marks[0].Value)
	require.Equal(t, uint(0), marks[0].Start)
	require.Equal(t, uint(5), marks[0].End)
}

func TestText_MarksSurviveMerge(t *testing.T) {
	doc1 := automerge.New()
	txt := automerge.NewText("Hello, World!")
	require.NoError(t, doc1.RootMap().Set("content", txt))
	_, err := doc1.Commit("init")
	require.NoError(t, err)

	doc2, err := doc1.Fork()
	require.NoError(t, err)

	txt1, err := automerge.As[*automerge.Text](doc1.RootMap().Get("content"))
	require.NoError(t, err)
	require.NoError(t, txt1.Mark("bold", "true", 0, 5, automerge.ExpandNone))

	txt2, err := automerge.As[*automerge.Text](doc2.RootMap().Get("content"))
	require.NoError(t, err)
	require.NoError(t, txt2.Mark("italic", "true", 7, 12, automerge.ExpandNone))

	_, err = doc1.Merge(doc2)
	require.NoError(t, err)

	marks, err := txt1.Marks()
	require.NoError(t, err)
	require.Len(t, marks, 2)

	names := map[string]bool{}
	for _, m := range marks {
		names[m.Name] = true
	}
	require.True(t, names["bold"])
	require.True(t, names["italic"])
}

func TestText_Cursor(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello, World!")
	require.NoError(t, doc.RootMap().Set("content", txt))

	cursor, err := txt.GetCursor(7)
	require.NoError(t, err)
	require.NotNil(t, cursor)
	require.NotEmpty(t, cursor.String())

	idx, err := txt.LookupCursor(cursor)
	require.NoError(t, err)
	require.Equal(t, 7, idx)
}

func TestText_CursorSurvivesInsertBefore(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello, World!")
	require.NoError(t, doc.RootMap().Set("content", txt))

	cursor, err := txt.GetCursor(7)
	require.NoError(t, err)

	// Insert "Hey " at position 0
	require.NoError(t, txt.Insert(0, "Hey "))

	// Cursor should now resolve to 11 (7 + 4)
	idx, err := txt.LookupCursor(cursor)
	require.NoError(t, err)
	require.Equal(t, 11, idx)
}

func TestText_CursorSurvivesDeleteBefore(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello, World!")
	require.NoError(t, doc.RootMap().Set("content", txt))

	cursor, err := txt.GetCursor(7)
	require.NoError(t, err)

	// Delete "Hello, " (7 chars at pos 0)
	require.NoError(t, txt.Delete(0, 7))

	// Cursor should now resolve to 0
	idx, err := txt.LookupCursor(cursor)
	require.NoError(t, err)
	require.Equal(t, 0, idx)
}

func TestText_CursorNewCursorRoundtrip(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello, World!")
	require.NoError(t, doc.RootMap().Set("content", txt))

	cursor, err := txt.GetCursor(5)
	require.NoError(t, err)

	// Recreate from string representation
	cursor2 := automerge.NewCursor(cursor.String())

	idx, err := txt.LookupCursor(cursor2)
	require.NoError(t, err)
	require.Equal(t, 5, idx)
}

func TestText_CursorNil(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello")
	require.NoError(t, doc.RootMap().Set("content", txt))

	_, err := txt.LookupCursor(nil)
	require.Error(t, err)
}

func TestText_MarkDetachedError(t *testing.T) {
	txt := automerge.NewText("hello")
	err := txt.Mark("bold", "true", 0, 5, automerge.ExpandNone)
	require.Error(t, err)
}

func TestText_CursorDetachedError(t *testing.T) {
	txt := automerge.NewText("hello")
	_, err := txt.GetCursor(0)
	require.Error(t, err)
}

func TestText_ExpandMarkConstants(t *testing.T) {
	require.Equal(t, automerge.ExpandMark(0), automerge.ExpandNone)
	require.Equal(t, automerge.ExpandMark(1), automerge.ExpandBefore)
	require.Equal(t, automerge.ExpandMark(2), automerge.ExpandAfter)
	require.Equal(t, automerge.ExpandMark(3), automerge.ExpandBoth)
}

func TestText_SpansPlainText(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello, World!")
	require.NoError(t, doc.RootMap().Set("content", txt))

	spans, err := txt.Spans()
	require.NoError(t, err)
	require.Len(t, spans, 1)
	require.Equal(t, automerge.SpanText, spans[0].Type)
	require.Equal(t, "Hello, World!", spans[0].Text)
	require.Empty(t, spans[0].Marks)
}

func TestText_SpansWithMarks(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("Hello, World!")
	require.NoError(t, doc.RootMap().Set("content", txt))

	require.NoError(t, txt.Mark("bold", "true", 0, 5, automerge.ExpandNone))

	spans, err := txt.Spans()
	require.NoError(t, err)
	// "Hello" (bold) + ", World!" (no marks) = 2 spans
	require.Len(t, spans, 2)

	require.Equal(t, automerge.SpanText, spans[0].Type)
	require.Equal(t, "Hello", spans[0].Text)
	require.Equal(t, "true", spans[0].Marks["bold"])

	require.Equal(t, automerge.SpanText, spans[1].Type)
	require.Equal(t, ", World!", spans[1].Text)
	require.Empty(t, spans[1].Marks)
}

func TestText_SpansEmpty(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("")
	require.NoError(t, doc.RootMap().Set("content", txt))

	spans, err := txt.Spans()
	require.NoError(t, err)
	require.Len(t, spans, 0)
}

func TestText_SplitBlockAndSpans(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("")
	require.NoError(t, doc.RootMap().Set("content", txt))

	// Insert a block marker at position 0
	block, err := txt.SplitBlock(0)
	require.NoError(t, err)
	require.NotNil(t, block)

	// Set block properties
	require.NoError(t, block.Set("type", "paragraph"))

	// Insert text after the block marker (position 1, since block occupies pos 0)
	require.NoError(t, txt.Splice(1, 0, "hello world"))

	spans, err := txt.Spans()
	require.NoError(t, err)
	require.Len(t, spans, 2)

	// First span should be the block marker
	require.Equal(t, automerge.SpanBlock, spans[0].Type)
	require.Equal(t, "paragraph", spans[0].Block["type"])

	// Second span should be the text
	require.Equal(t, automerge.SpanText, spans[1].Type)
	require.Equal(t, "hello world", spans[1].Text)
}

func TestText_JoinBlock(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("")
	require.NoError(t, doc.RootMap().Set("content", txt))

	// Insert a block, then remove it
	_, err := txt.SplitBlock(0)
	require.NoError(t, err)

	require.NoError(t, txt.Splice(1, 0, "hello"))

	// Remove the block marker
	require.NoError(t, txt.JoinBlock(0))

	spans, err := txt.Spans()
	require.NoError(t, err)
	// Should just be text, no block
	require.Len(t, spans, 1)
	require.Equal(t, automerge.SpanText, spans[0].Type)
	require.Equal(t, "hello", spans[0].Text)
}

func TestText_ReplaceBlock(t *testing.T) {
	doc := automerge.New()

	txt := automerge.NewText("")
	require.NoError(t, doc.RootMap().Set("content", txt))

	_, err := txt.SplitBlock(0)
	require.NoError(t, err)

	require.NoError(t, txt.Splice(1, 0, "heading text"))

	// Replace the block with a different type
	newBlock, err := txt.ReplaceBlock(0)
	require.NoError(t, err)
	require.NoError(t, newBlock.Set("type", "heading"))

	spans, err := txt.Spans()
	require.NoError(t, err)
	require.Len(t, spans, 2)
	require.Equal(t, automerge.SpanBlock, spans[0].Type)
	require.Equal(t, "heading", spans[0].Block["type"])
}

func TestText_SpansDetachedError(t *testing.T) {
	txt := automerge.NewText("hello")
	_, err := txt.Spans()
	require.Error(t, err)
}

func TestText_SplitBlockDetachedError(t *testing.T) {
	txt := automerge.NewText("hello")
	_, err := txt.SplitBlock(0)
	require.Error(t, err)
}
