package uiautomator2

import "testing"

// An id that matches some elements whole keeps only those; the substring
// match stays as the fallback when nothing matches whole.
func TestPreferExactID(t *testing.T) {
	input := &ParsedElement{ResourceID: "com.duckduckgo.mobile.android:id/omnibarTextInput", Text: "why use duckduckgo"}
	catcher := &ParsedElement{ResourceID: "com.duckduckgo.mobile.android:id/omnibarTextInputClickCatcher"}
	both := []*ParsedElement{catcher, input}

	if got := preferExactID(both, "omnibarTextInput|inputField"); len(got) != 1 || got[0] != input {
		t.Errorf("alternation: got %d elements, want only omnibarTextInput", len(got))
	}
	if got := preferExactID(both, "omnibarTextInput"); len(got) != 1 || got[0] != input {
		t.Errorf("plain id: got %d elements, want only omnibarTextInput", len(got))
	}
	if got := preferExactID([]*ParsedElement{catcher}, "omnibarTextInput"); len(got) != 1 {
		t.Errorf("no whole-id match should keep the substring match")
	}
	if got := preferExactID(both, "omnibar"); len(got) != 2 {
		t.Errorf("substring-only selector should keep both, got %d", len(got))
	}
}
