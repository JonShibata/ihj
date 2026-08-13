package jira

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikecsmith/ihj/internal/core"
	"github.com/mikecsmith/ihj/internal/document"
)

// sampleADF is a description exercising constructs Markdown can't represent
// losslessly: a table (with a header row) and an expand/collapse section.
const sampleADF = `{
	"version": 1,
	"type": "doc",
	"content": [
		{"type": "paragraph", "content": [{"type": "text", "text": "first paragraph"}]},
		{"type": "table", "content": [
			{"type": "tableRow", "content": [
				{"type": "tableHeader", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "H1"}]}]},
				{"type": "tableHeader", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "H2"}]}]}
			]},
			{"type": "tableRow", "content": [
				{"type": "tableCell", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "a"}]}]},
				{"type": "tableCell", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "b"}]}]}
			]}
		]},
		{"type": "expand", "attrs": {"title": "Details"}, "content": [
			{"type": "paragraph", "content": [{"type": "text", "text": "secret inside expand"}]}
		]}
	]
}`

func adfTypeCounts(v any) map[string]int {
	c := map[string]int{}
	var walk func(any)
	walk = func(n any) {
		switch t := n.(type) {
		case map[string]any:
			if ty, ok := t["type"].(string); ok {
				c[ty]++
			}
			for _, val := range t {
				walk(val)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return c
}

// adfHasText reports whether want appears in the concatenated text of the ADF
// tree. Text is concatenated because Markdown parsing can fragment a run into
// several adjacent text nodes ("just a" + " paragraph").
func adfHasText(v any, want string) bool {
	var b strings.Builder
	var walk func(any)
	walk = func(n any) {
		switch t := n.(type) {
		case map[string]any:
			if s, ok := t["text"].(string); ok {
				b.WriteString(s)
			}
			for _, val := range t {
				walk(val)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return strings.Contains(b.String(), want)
}

// editorBuffer mimics what ihj puts in the editor: the ADF rendered to Markdown.
func editorBuffer(t *testing.T, raw string) string {
	t.Helper()
	ast, err := parseADF([]byte(raw))
	if err != nil {
		t.Fatalf("parseADF: %v", err)
	}
	return document.RenderMarkdown(ast)
}

// TestReconcile_FullRenderIsLossy documents the baseline: regenerating the whole
// description from the edited Markdown loses the table header row and the expand.
func TestReconcile_FullRenderIsLossy(t *testing.T) {
	edited, _ := document.ParseMarkdownString(editorBuffer(t, sampleADF))
	full := renderADFValue(edited)
	c := adfTypeCounts(full)
	if c["tableHeader"] != 0 || c["expand"] != 0 {
		t.Fatalf("expected the lossy baseline to drop tableHeader/expand, got %v", c)
	}
}

// TestReconcile_NoOpPreservesAll: opening the editor and saving with no change
// must round-trip every construct verbatim.
func TestReconcile_NoOpPreservesAll(t *testing.T) {
	edited, _ := document.ParseMarkdownString(editorBuffer(t, sampleADF))
	merged, err := reconcileADF([]byte(sampleADF), edited)
	if err != nil {
		t.Fatal(err)
	}
	got := adfTypeCounts(merged)
	want := adfTypeCounts(mustJSON(t, sampleADF))
	for _, k := range []string{"expand", "tableHeader", "tableCell", "table"} {
		if got[k] != want[k] {
			t.Errorf("node %q: got %d, want %d (no-op edit must preserve)", k, got[k], want[k])
		}
	}
	if !adfHasText(merged, "secret inside expand") {
		t.Error("expand body lost")
	}
}

// TestReconcile_EditedBlockOnly: editing one paragraph re-renders only that
// block; the untouched table and expand are preserved verbatim.
func TestReconcile_EditedBlockOnly(t *testing.T) {
	buf := editorBuffer(t, sampleADF)
	buf = strings.Replace(buf, "first paragraph", "FIRST PARAGRAPH CHANGED", 1)
	edited, _ := document.ParseMarkdownString(buf)

	merged, err := reconcileADF([]byte(sampleADF), edited)
	if err != nil {
		t.Fatal(err)
	}
	if !adfHasText(merged, "FIRST PARAGRAPH CHANGED") {
		t.Error("edit not applied")
	}
	c := adfTypeCounts(merged)
	if c["tableHeader"] != 2 {
		t.Errorf("tableHeader = %d; want 2 (untouched table preserved)", c["tableHeader"])
	}
	if c["expand"] != 1 {
		t.Errorf("expand = %d; want 1 (untouched expand preserved)", c["expand"])
	}
}

// TestReconcile_AddedBlockSerialized: a brand-new block the user typed is
// serialized normally, and prior blocks stay verbatim.
func TestReconcile_AddedBlockSerialized(t *testing.T) {
	buf := editorBuffer(t, sampleADF) + "\nA brand new paragraph.\n"
	edited, _ := document.ParseMarkdownString(buf)
	merged, err := reconcileADF([]byte(sampleADF), edited)
	if err != nil {
		t.Fatal(err)
	}
	if !adfHasText(merged, "A brand new paragraph") {
		t.Error("new block not serialized")
	}
	if adfTypeCounts(merged)["expand"] != 1 {
		t.Error("expand lost when a block was appended")
	}
}

// TestDescriptionADF_FallbackWithoutOriginal: with no original payload the
// write falls back to a full render (Create path, or providers that don't
// retain source).
func TestDescriptionADF_FallbackWithoutOriginal(t *testing.T) {
	edited, _ := document.ParseMarkdownString("just a paragraph\n")
	ch := &core.Changes{Description: edited} // DescriptionOriginal nil
	out := descriptionADF(ch)
	if out["type"] != "doc" {
		t.Fatalf("expected a doc, got %v", out["type"])
	}
	if !adfHasText(out["content"], "just a paragraph") {
		t.Error("fallback render lost content")
	}
}

func mustJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
