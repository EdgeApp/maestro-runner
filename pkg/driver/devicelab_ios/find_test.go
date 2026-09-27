package devicelab_ios

import (
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

func sc(nodes ...Node) *screen { return &screen{nodes: nodes, width: 400, height: 800} }

func TestPickPrefersExactCaseThenDeepest(t *testing.T) {
	outer := node(1, "Other", "Login", 0, 0, 400, 100)
	inner := node(2, "Button", "Login", 10, 10, 100, 40)
	inner.P = 1
	lower := node(3, "Button", "login", 0, 200, 100, 40)
	got, err := pick(sc(outer, lower, inner), flow.Selector{Text: "Login"})
	if err != nil || got.I != 2 {
		t.Fatalf("got %+v %v, want the deepest exact-case match", got, err)
	}
	got, _ = pick(sc(outer, lower, inner), flow.Selector{Text: "login"})
	if got.I != 3 {
		t.Fatalf("got %d, want the exact-case lowercase match", got.I)
	}
}

func TestPickIndex(t *testing.T) {
	s := sc(node(1, "Cell", "Row", 0, 0, 400, 50), node(2, "Cell", "Row", 0, 60, 400, 50), node(3, "Cell", "Row", 0, 120, 400, 50))
	for idx, want := range map[string]int{"0": 1, "2": 3, "-1": 3} {
		got, err := pick(s, flow.Selector{Text: "Row", Index: idx})
		if err != nil || got.I != want {
			t.Errorf("index %s: got %+v %v, want %d", idx, got, err, want)
		}
	}
	for _, bad := range []string{"5", "x", "-9"} {
		if _, err := pick(s, flow.Selector{Text: "Row", Index: bad}); err == nil {
			t.Errorf("index %s should fail", bad)
		}
	}
}

func TestMatchesSelfFields(t *testing.T) {
	n := node(1, "Switch", "Wi-Fi", 0, 0, 100, 30)
	n.ID = "com.app:id/wifi_toggle"
	n.Value = "1"
	n.Focused = true
	cases := []struct {
		sel  flow.Selector
		want bool
	}{
		{flow.Selector{ID: "wifi_toggle"}, true},
		{flow.Selector{ID: "WIFI.*"}, true},
		{flow.Selector{ID: "bluetooth"}, false},
		{flow.Selector{Text: "wi-fi"}, true},
		{flow.Selector{Checked: boolp(true)}, true},
		{flow.Selector{Checked: boolp(false)}, false},
		{flow.Selector{Enabled: boolp(false)}, false},
		{flow.Selector{Selected: boolp(true)}, false},
		{flow.Selector{Focused: boolp(true)}, true},
		{flow.Selector{Width: 103, Height: 28}, true},
		{flow.Selector{Width: 120}, false},
		{flow.Selector{Height: 50, Tolerance: 30}, true},
	}
	for _, c := range cases {
		if got := matchesSelf(n, c.sel); got != c.want {
			t.Errorf("%+v: got %v, want %v", c.sel, got, c.want)
		}
	}
}

func TestInvisibleNodesDoNotMatch(t *testing.T) {
	n := node(1, "Button", "Hidden", 0, 0, 10, 10)
	n.Vis = 0.05
	if _, err := pick(sc(n), flow.Selector{Text: "Hidden"}); err == nil {
		t.Fatal("5% visible should not count")
	}
}

func TestRelativeSelectors(t *testing.T) {
	list := node(1, "Table", "", 0, 100, 400, 600)
	header := node(2, "StaticText", "Email", 0, 100, 400, 30)
	header.P = 1
	field := node(3, "TextField", "", 0, 140, 400, 40)
	field.P = 1
	field.Placeholder = "you@example.com"
	far := node(4, "TextField", "", 0, 600, 400, 40)
	far.P = 1
	left := node(5, "Button", "L", 0, 700, 50, 50)
	right := node(6, "Button", "R", 300, 700, 50, 50)
	s := sc(list, header, field, far, left, right)

	cases := []struct {
		name string
		sel  flow.Selector
		want int
	}{
		{"below nearest", flow.Selector{Below: &flow.Selector{Text: "Email"}, Text: ".*"}, 3},
		{"above", flow.Selector{Above: &flow.Selector{Text: "you@.*"}, Text: "Email"}, 2},
		{"rightOf", flow.Selector{RightOf: &flow.Selector{Text: "L"}, Text: "R"}, 6},
		{"leftOf", flow.Selector{LeftOf: &flow.Selector{Text: "R"}, Text: "L"}, 5},
		{"childOf", flow.Selector{ChildOf: &flow.Selector{ID: ""}, Text: "Email"}, -1},
		{"insideOf", flow.Selector{InsideOf: &flow.Selector{Text: "Email"}, Text: "Email"}, 2},
	}
	for _, c := range cases {
		got, err := pick(s, c.sel)
		if c.want < 0 {
			if err == nil {
				t.Errorf("%s: want error", c.name)
			}
			continue
		}
		if err != nil || got.I != c.want {
			t.Errorf("%s: got %+v %v, want %d", c.name, got, err, c.want)
		}
	}

	list.ID = "form"
	s = sc(list, header, field, far, left, right)
	got, err := pick(s, flow.Selector{ChildOf: &flow.Selector{ID: "form"}, Text: "Email"})
	if err != nil || got.I != 2 {
		t.Fatalf("childOf: %+v %v", got, err)
	}
	got, err = pick(s, flow.Selector{ID: "form", ContainsChild: &flow.Selector{Text: "Email"}})
	if err != nil || got.I != 1 {
		t.Fatalf("containsChild: %+v %v", got, err)
	}
	got, err = pick(s, flow.Selector{ID: "form", ContainsDescendants: []*flow.Selector{{Text: "you@.*"}, nil}})
	if err != nil || got.I != 1 {
		t.Fatalf("containsDescendants: %+v %v", got, err)
	}
	if _, err := pick(s, flow.Selector{ID: "form", ContainsChild: &flow.Selector{Text: "nope"}}); err == nil {
		t.Fatal("containsChild miss should fail")
	}
}

func TestLookupUsesSnapshotForRelative(t *testing.T) {
	d, fa, _ := newTestDriver(t, screenOf(node(1, "Button", "A", 0, 0, 10, 10)))
	_, _ = d.lookup(flow.Selector{Text: "A", Below: &flow.Selector{Text: "B"}}, false)
	_, _ = d.lookup(flow.Selector{Width: 10}, false)
	_, _ = d.lookup(flow.Selector{ID: "submit_btn"}, false)
	_, _ = d.lookup(flow.Selector{ID: "com.app.submit"}, false)
	if s := fa.sent("snapshot"); len(s) != 2 || !s[0].VisibleOnly {
		t.Fatalf("snapshots = %+v", s)
	}
	f := fa.sent("find")
	if len(f) != 2 || f[0].IDNeedle != "submit_btn" || f[1].IDNeedle != "" {
		t.Fatalf("finds = %+v", f)
	}
}

func TestToElementInfoAndDescribe(t *testing.T) {
	n := node(1, "Button", "Save", 1, 2, 3, 4)
	n.ID = "save"
	info := toElementInfo(&n)
	if info.Text != "Save" || info.Bounds.Width != 3 || !info.Visible || info.Class != "Button" {
		t.Fatalf("info = %+v", info)
	}
	if toElementInfo(nil) != nil {
		t.Fatal("nil")
	}
	if describe(flow.Selector{}) != "element" || describe(flow.Selector{Text: "a", ID: "b", Index: "1", Below: &flow.Selector{}}) == "" {
		t.Fatal("describe")
	}
	n.Title, n.Value = "", "v"
	if elementText(n) != "v" {
		t.Fatal("value before label")
	}
}
