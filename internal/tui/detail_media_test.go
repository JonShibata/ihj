package tui

import (
	"strings"
	"testing"

	"github.com/mikecsmith/ihj/internal/core"
	"github.com/mikecsmith/ihj/internal/document"
)

func TestAnnotateMedia_InlinePlaceholderWithHint(t *testing.T) {
	m := &DetailModel{
		issue: &core.WorkItem{
			Attachments: []core.Attachment{
				{ID: "1", Filename: "a.png", MIMEType: "image/png"},
				{ID: "2", Filename: "b.png", MIMEType: "image/png"},
			},
		},
		hintLabels: []string{"x", "y"},
	}
	doc := document.NewDoc(
		document.NewParagraph(document.NewText("before")),
		&document.Node{Type: document.NodeMedia, Children: []*document.Node{
			document.NewMedia("file", "", "b.png"),
		}},
	)

	got := document.RenderMarkdown(m.annotateMedia(doc))

	if !strings.Contains(got, "[image: b.png] [y]") {
		t.Errorf("placeholder missing from body:\n%s", got)
	}
	if strings.Contains(document.RenderMarkdown(doc), "[image:") {
		t.Error("annotateMedia mutated the original AST")
	}
}
