package wda

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// errNotInTree reports that WDA searched the whole accessibility tree and no
// element carries the selector's text or id anywhere, on screen or off. The
// page-source matcher reads the same attributes from the same tree, so it
// cannot match either, and the full /source fetch (seconds on a large
// screen) is skipped.
var errNotInTree = errors.New("no element in the accessibility tree has this text or id")

// absenceProbe returns a predicate matching every element the page-source
// matcher could accept for sel, and false when sel is outside what the
// predicate can mirror exactly.
//
// WDA evaluates label, value, placeholderValue and name through the same
// getters /source serialises, so the attributes agree. The comparison is
// CONTAINS[cd], looser than the matcher on both case and diacritics, so the
// probe finding nothing proves the matcher finds nothing. That argument only
// holds for a plain ASCII needle: regex selectors, non-ASCII text (NFC and
// case folding differ between Go and Foundation), and characters that are
// special inside a predicate literal all keep the page-source path.
//
// Width, height and state filters only narrow what the text or id already
// matched, so they do not disqualify a selector.
func absenceProbe(sel flow.Selector) (string, bool) {
	if sel.HasRelativeSelector() || sel.HasNonZeroIndex() || (sel.Text != "") == (sel.ID != "") {
		return "", false
	}
	if sel.Text != "" {
		if !predicateSafeASCII(sel.Text) || looksLikeRegex(sel.Text) {
			return "", false
		}
		t := sel.Text
		return fmt.Sprintf("label CONTAINS[cd] '%s' OR value CONTAINS[cd] '%s' OR placeholderValue CONTAINS[cd] '%s'", t, t, t), true
	}
	// matchesID compiles the id as a regex; with no metacharacters that is a
	// plain substring test.
	if !predicateSafeASCII(sel.ID) || regexp.QuoteMeta(sel.ID) != sel.ID {
		return "", false
	}
	return fmt.Sprintf("name CONTAINS[cd] '%s'", sel.ID), true
}

// predicateSafeASCII reports whether s is printable ASCII with none of the
// characters that end or escape a predicate literal or act as a format
// specifier.
func predicateSafeASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c < 0x20 || c > 0x7e:
			return false
		case c == '\'' || c == '"' || c == '\\' || c == '%' || c == '`':
			return false
		}
	}
	return true
}

// provenAbsent reports whether WDA shows that nothing in the tree could
// match sel. Any error or match leaves the answer to page source.
func (d *Driver) provenAbsent(sel flow.Selector) bool {
	pred, ok := absenceProbe(sel)
	if !ok {
		return false
	}
	n, err := d.client.CountElements("predicate string", pred)
	return err == nil && n == 0
}

// withPageSourceHint runs one page-source search after a find that failed
// on errNotInTree, so a failing required step still reports the closest
// on-screen texts (#89). Optional finds skip it; failing is their expected
// outcome and the fetch is what the shortcut saves.
func (d *Driver) withPageSourceHint(sel flow.Selector, err error) (*core.ElementInfo, error) {
	if !errors.Is(err, errNotInTree) {
		return nil, err
	}
	info, psErr := d.findElementByPageSourceOnce(sel)
	if psErr == nil {
		return info, nil
	}
	return nil, fmt.Errorf("%w; page source: %v", err, psErr)
}
