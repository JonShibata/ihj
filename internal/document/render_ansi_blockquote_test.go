package document_test

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/mikecsmith/ihj/internal/document"
)

// TestRenderANSI_BlockquoteWrapsWithinWidth guards the callout/blockquote wrap
// budget. Glamour reserves the quote prefix width via BlockStack.Width; if the
// reservation is smaller than the "│ " token, quoted lines overflow the wrap
// width and an ancestor block re-wraps them so the spilled word lands on a
// fresh line with no prefix. Both symptoms are asserted below across single
// and nested quote levels.
func TestRenderANSI_BlockquoteWrapsWithinWidth(t *testing.T) {
	md := `> ## Summary
>
> FirewallSetState.stop is set when a mitigation withdraws and is never cleared. Once set, get_device_ids_to_announce() returns every device in the firewall set.
>
> > It was introduced by 5cb9f4a1a9fa (DEFBE-8208, "Pland support selective edge mitigations")
> >
> > purely as an override for the narrowing: a withdrawal has to reach every device, because the target set shrinks over time and a device that received rules earlier may no longer be in targeted_device_ids.`

	node, err := document.ParseMarkdownString(md)
	if err != nil {
		t.Fatal(err)
	}

	const width = 84
	out := document.RenderANSI(node, document.ANSIConfig{
		WrapWidth: width,
		Style:     document.ContentTheme("default"),
	})

	for i, line := range strings.Split(out, "\n") {
		plain := strings.TrimRight(xansi.Strip(line), " ")
		if w := xansi.StringWidth(plain); w > width {
			t.Errorf("line %d overflows width %d (got %d): %q", i, width, w, plain)
		}
		// Every non-blank quoted line begins with a prefix. A word that spilled
		// off a mis-wrapped quote line would appear as body text at column 0
		// (no "│"), so any non-blank line that isn't a heading/rule must carry
		// the quote bar.
		if plain == "" {
			continue
		}
		if !strings.HasPrefix(plain, "│") && !strings.HasPrefix(plain, "#") {
			t.Errorf("line %d lost its quote prefix (spilled continuation): %q", i, plain)
		}
	}
}
