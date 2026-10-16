package domain

import "testing"

func TestMarkerIsRecognisedThroughTagsAndEmphasis(t *testing.T) {
	cases := map[string]bool{
		"Done.\n\n<kbd>🤖 unattendish agent</kbd>\n": true,
		"Done.\n\n**🤖 unattendish agent**":          true,
		"Done.\r\n🤖 unattendish agent  \r\n\r\n":    true,
		"🤖 unattendish agent\nbut then a person":    false,
		"no marker here": false,
		"":               false,
		"Done.\n\n_<kbd>🤖 unattendish agent</kbd>_  ": true,
	}
	for body, want := range cases {
		if got := EndsWithMarker(body, DefaultMarker); got != want {
			t.Errorf("%q: got %v", body, got)
		}
	}
	if !EndsWithMarker("x\n<kbd>🤖 unattendish agent</kbd>", "<kbd>🤖 unattendish agent</kbd>") {
		t.Error("a marker written with its tags must match too")
	}
}
