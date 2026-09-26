package devicelab

import (
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// A whole-screen check sees what the user sees: hidden and zero-area nodes do
// not count, and hint text, quotes and wrapped text match as in Maestro.
func TestFindVisibleOnce(t *testing.T) {
	screen := `<hierarchy>
<node class="android.widget.TextView" text="Albums" displayed="false" bounds="[0,0][100,50]"/>
<node class="android.widget.TextView" text="Ghost" displayed="true" bounds="[0,0][0,0]"/>
<node class="android.widget.EditText" hint-text="Email" displayed="true" bounds="[0,100][500,150]"/>
<node class="android.widget.TextView" text="Open &quot;Moby Dick&quot;" displayed="true" bounds="[0,200][500,250]"/>
<node class="android.widget.TextView" text="Two&#10;lines" displayed="true" bounds="[0,300][500,350]"/>
<node class="android.widget.Button" resource-id="com.app:id/save" displayed="true" bounds="[0,400][500,450]"/>
</hierarchy>`
	for _, tc := range []struct {
		sel   flow.Selector
		found bool
	}{
		{flow.Selector{Text: "Albums"}, false},
		{flow.Selector{Text: "Ghost"}, false},
		{flow.Selector{Text: "Email"}, true},
		{flow.Selector{Text: `Open "Moby Dick"`}, true},
		{flow.Selector{Text: "Two lines"}, true},
		{flow.Selector{ID: "save"}, true},
		{flow.Selector{Text: "Nowhere"}, false},
	} {
		client := &mockDeviceLabClient{sourceFunc: func() (string, error) { return screen, nil }}
		d := New(client, &core.PlatformInfo{}, &mockShell{})
		info, err := d.findVisibleOnce(tc.sel)
		if found := err == nil && info != nil; found != tc.found {
			t.Errorf("%s: found = %v, want %v (%v)", tc.sel.Describe(), found, tc.found, err)
		}
	}
}

func TestChecksBySnapshot(t *testing.T) {
	if !checksBySnapshot(flow.Selector{Text: "a"}) || !checksBySnapshot(flow.Selector{ID: "a"}) {
		t.Error("text and id selectors should use the snapshot")
	}
	if checksBySnapshot(flow.Selector{Text: "a", Below: &flow.Selector{Text: "b"}}) {
		t.Error("relative selectors keep their own path")
	}
	if checksBySnapshot(flow.Selector{Text: "a", Index: "2"}) {
		t.Error("index selectors keep their own path")
	}
}
