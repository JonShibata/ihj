package document

import (
	"reflect"
	"testing"
)

func TestLinks(t *testing.T) {
	tests := []struct {
		name string
		node *Node
		want []string
	}{
		{
			name: "nil node",
			node: nil,
			want: nil,
		},
		{
			name: "no links",
			node: NewDoc(NewParagraph(NewText("plain text"))),
			want: nil,
		},
		{
			name: "single link mark",
			node: NewDoc(NewParagraph(
				NewText("see "),
				NewStyledText("the docs", Link("https://example.com/docs")),
			)),
			want: []string{"https://example.com/docs"},
		},
		{
			name: "multiple links in document order",
			node: NewDoc(
				NewParagraph(NewStyledText("first", Link("https://a.test"))),
				NewParagraph(NewStyledText("second", Link("http://b.test/path"))),
			),
			want: []string{"https://a.test", "http://b.test/path"},
		},
		{
			name: "duplicates preserved",
			node: NewDoc(
				NewParagraph(NewStyledText("a", Link("https://dup.test"))),
				NewParagraph(NewStyledText("b", Link("https://dup.test"))),
			),
			want: []string{"https://dup.test", "https://dup.test"},
		},
		{
			name: "non-web schemes excluded",
			node: NewDoc(NewParagraph(
				NewStyledText("mail", Link("mailto:x@y.test")),
				NewStyledText("rel", Link("/relative/path")),
				NewStyledText("ftp", Link("ftp://host/file")),
				NewStyledText("web", Link("https://ok.test")),
			)),
			want: []string{"https://ok.test"},
		},
		{
			name: "media node urls are not web links",
			node: NewDoc(NewParagraph(NewMedia("image", "https://img.test/pic.png", "alt"))),
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Links(tt.node)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Links() = %v, want %v", got, tt.want)
			}
		})
	}
}
