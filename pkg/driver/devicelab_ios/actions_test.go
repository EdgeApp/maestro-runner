package devicelab_ios

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

func TestTapOnTapsElementCentre(t *testing.T) {
	d, fa, _ := newTestDriver(t, screenOf(node(1, "Button", "Sign In", 100, 200, 100, 40)))
	res := d.Execute(&flow.TapOnStep{Selector: flow.Selector{Text: "Sign In"}})
	if !res.Success {
		t.Fatalf("tapOn failed: %s", res.Message)
	}
	acts := fa.sent("act")
	if len(acts) != 1 || acts[0].Kind != "tap" || *acts[0].X != 150 || *acts[0].Y != 220 {
		t.Fatalf("act = %+v", acts)
	}
	finds := fa.sent("find")
	if len(finds) == 0 || strings.Join(finds[0].Needles, ",") != "sign,in" {
		t.Fatalf("find needles = %+v", finds)
	}
}

func TestTapOnClipsToScreen(t *testing.T) {
	// Half off the bottom: the tap goes to the centre of the visible part.
	d, fa, _ := newTestDriver(t, screenOf(node(1, "Button", "Go", 0, 700, 400, 200)))
	if res := d.Execute(&flow.TapOnStep{Selector: flow.Selector{Text: "Go"}}); !res.Success {
		t.Fatal(res.Message)
	}
	if a := fa.sent("act")[0]; *a.Y != 750 {
		t.Fatalf("y = %v, want 750", *a.Y)
	}
}

func TestTapOnOptionalMissingSucceeds(t *testing.T) {
	d, fa, _ := newTestDriver(t, screenOf())
	res := d.Execute(&flow.TapOnStep{BaseStep: flow.BaseStep{Optional: true, TimeoutMs: 50}, Selector: flow.Selector{Text: "Nope"}})
	if !res.Success {
		t.Fatalf("optional tap should succeed: %s", res.Message)
	}
	if len(fa.sent("act")) != 0 {
		t.Fatal("no tap expected")
	}
	// A later find includes alerts.
	finds := fa.sent("find")
	if len(finds) < 2 || !finds[1].Alerts || finds[0].Alerts {
		t.Fatalf("alerts after first miss: %+v", finds)
	}
}

func TestTapOnRequiredMissingFails(t *testing.T) {
	d, _, _ := newTestDriver(t, screenOf())
	res := d.Execute(&flow.TapOnStep{BaseStep: flow.BaseStep{TimeoutMs: 50}, Selector: flow.Selector{Text: "Nope"}})
	if res.Success || !strings.Contains(res.Message, "not found") {
		t.Fatalf("want not found, got %+v", res)
	}
}

func TestTapVariants(t *testing.T) {
	d, fa, _ := newTestDriver(t, screenOf(node(1, "Cell", "Row", 0, 100, 400, 50)))
	d.Execute(&flow.LongPressOnStep{Selector: flow.Selector{Text: "Row"}})
	d.Execute(&flow.DoubleTapOnStep{Selector: flow.Selector{Text: "Row"}})
	d.Execute(&flow.TapOnStep{Selector: flow.Selector{Text: "Row"}, Point: "10%, 50%"})
	d.Execute(&flow.TapOnStep{Selector: flow.Selector{Text: "Row"}, LongPress: true})
	acts := fa.sent("act")
	if len(acts) != 4 {
		t.Fatalf("acts = %d", len(acts))
	}
	if acts[0].Kind != "longPress" || *acts[0].HoldMs != 1000 {
		t.Errorf("longPress = %+v", acts[0])
	}
	if acts[1].Kind != "doubleTap" {
		t.Errorf("doubleTap = %+v", acts[1])
	}
	if acts[2].Kind != "tap" || *acts[2].X != 40 || *acts[2].Y != 125 {
		t.Errorf("relative point tap = %v,%v", *acts[2].X, *acts[2].Y)
	}
	if acts[3].Kind != "longPress" {
		t.Errorf("tapOn longPress = %+v", acts[3])
	}
}

func TestTapOnPoint(t *testing.T) {
	d, fa, _ := newTestDriver(t, nil)
	if res := d.Execute(&flow.TapOnPointStep{Point: "50%, 25%"}); !res.Success {
		t.Fatal(res.Message)
	}
	if res := d.Execute(&flow.TapOnPointStep{X: 10, Y: 20, LongPress: true}); !res.Success {
		t.Fatal(res.Message)
	}
	if res := d.Execute(&flow.TapOnStep{Point: "10,10"}); !res.Success {
		t.Fatal(res.Message)
	}
	acts := fa.sent("act")
	if *acts[0].X != 200 || *acts[0].Y != 200 || acts[1].Kind != "longPress" || *acts[2].X != 10 {
		t.Fatalf("acts = %+v", acts)
	}
	if res := d.Execute(&flow.TapOnPointStep{Point: "nonsense"}); res.Success {
		t.Fatal("bad point should fail")
	}
}

func TestSwipeForms(t *testing.T) {
	d, fa, _ := newTestDriver(t, screenOf(node(1, "Cell", "Card", 0, 300, 400, 100)))
	steps := []*flow.SwipeStep{
		{Direction: "LEFT"},
		{Start: "90%, 50%", End: "10%, 50%", Duration: 800},
		{StartX: 1, StartY: 2, EndX: 3, EndY: 4, Speed: 50},
		{Direction: "UP", Selector: &flow.Selector{Text: "Card"}},
	}
	for _, s := range steps {
		if res := d.Execute(s); !res.Success {
			t.Fatalf("%+v: %s", s, res.Message)
		}
	}
	acts := fa.sent("act")
	if len(acts) != 4 {
		t.Fatalf("acts = %d", len(acts))
	}
	for _, a := range acts {
		if a.Kind != "swipe" || a.X2 == nil || a.MoveMs == nil || *a.MoveMs != 100 {
			t.Fatalf("swipe args = %+v", a)
		}
	}
	if *acts[0].X <= *acts[0].X2 {
		t.Errorf("left swipe should move left: %v → %v", *acts[0].X, *acts[0].X2)
	}
	if *acts[1].X != 360 || *acts[1].X2 != 40 || *acts[1].RestMs != 700 {
		t.Errorf("percent swipe = %+v", acts[1])
	}
	if *acts[2].X != 1 || *acts[2].Y2 != 4 {
		t.Errorf("absolute swipe = %+v", acts[2])
	}
	if *acts[3].Y < 300 || *acts[3].Y > 400 {
		t.Errorf("element swipe should start inside the card: %v", *acts[3].Y)
	}
	if res := d.Execute(&flow.SwipeStep{Direction: "sideways"}); res.Success {
		t.Error("bad direction should fail")
	}
}

func TestScroll(t *testing.T) {
	d, fa, _ := newTestDriver(t, nil)
	for _, dir := range []string{"", "up", "left", "right"} {
		if res := d.Execute(&flow.ScrollStep{Direction: dir}); !res.Success {
			t.Fatal(res.Message)
		}
	}
	acts := fa.sent("act")
	if *acts[0].Y != 400 || *acts[0].Y2 != 80 {
		t.Errorf("scroll down = %v→%v", *acts[0].Y, *acts[0].Y2)
	}
	if *acts[1].Y2 != 720 || *acts[2].X2 != 360 || *acts[3].X2 != 40 {
		t.Errorf("scrolls = %+v", acts)
	}
	if res := d.Execute(&flow.ScrollStep{Direction: "diagonal"}); res.Success {
		t.Error("bad direction should fail")
	}
}

func TestScrollUntilVisibleFindsAfterScrolling(t *testing.T) {
	var scrolls atomic.Int32
	d, _, _ := newTestDriver(t, func(cmd string, _ Args) (*Response, error) {
		switch cmd {
		case "act":
			scrolls.Add(1)
		case "find":
			if scrolls.Load() >= 2 {
				return tree(node(1, "Cell", "Target", 0, 300, 400, 50)), nil
			}
			return tree(), nil
		case "settle":
			return ok(&Payload{ScreenHash: "h" + string(rune('a'+scrolls.Load()))}), nil
		}
		return ok(nil), nil
	})
	res := d.Execute(&flow.ScrollUntilVisibleStep{Element: flow.Selector{Text: "Target"}})
	if !res.Success || scrolls.Load() != 2 {
		t.Fatalf("res=%+v scrolls=%d", res, scrolls.Load())
	}
}

func TestScrollUntilVisibleStopsWithoutProgress(t *testing.T) {
	d, fa, _ := newTestDriver(t, func(cmd string, _ Args) (*Response, error) {
		if cmd == "settle" {
			return ok(&Payload{ScreenHash: "same"}), nil
		}
		if cmd == "find" {
			return tree(), nil
		}
		return ok(nil), nil
	})
	res := d.Execute(&flow.ScrollUntilVisibleStep{Element: flow.Selector{Text: "Missing"}})
	if res.Success || !strings.Contains(res.Message, "no progress") {
		t.Fatalf("res = %+v", res)
	}
	if n := len(fa.sent("act")); n != 3 {
		t.Fatalf("scrolls = %d, want 3", n)
	}
}

func TestScrollUntilVisibleLimits(t *testing.T) {
	d, fa, _ := newTestDriver(t, screenOf())
	res := d.Execute(&flow.ScrollUntilVisibleStep{Element: flow.Selector{Text: "X"}, MaxScrolls: 2})
	if res.Success || len(fa.sent("act")) != 2 {
		t.Fatalf("res=%+v acts=%d", res, len(fa.sent("act")))
	}
	res = d.Execute(&flow.ScrollUntilVisibleStep{Element: flow.Selector{Text: "X"}, From: flow.Selector{ID: "list"}})
	if res.Success || !strings.Contains(res.Message, "from") {
		t.Fatalf("from: should be refused, got %+v", res)
	}
}

func TestScrollUntilVisibleNeedsVisibility(t *testing.T) {
	// 20% on screen is found by find but not enough for the default 100%.
	var scrolls atomic.Int32
	d, _, _ := newTestDriver(t, func(cmd string, _ Args) (*Response, error) {
		switch cmd {
		case "act":
			scrolls.Add(1)
		case "find":
			if scrolls.Load() == 0 {
				n := node(1, "Cell", "T", 0, 790, 400, 50)
				n.Vis = 0.2
				return tree(n), nil
			}
			return tree(node(1, "Cell", "T", 0, 500, 400, 50)), nil
		}
		return ok(nil), nil
	})
	if res := d.Execute(&flow.ScrollUntilVisibleStep{Element: flow.Selector{Text: "T"}}); !res.Success || scrolls.Load() != 1 {
		t.Fatalf("res=%+v scrolls=%d", res, scrolls.Load())
	}
}

func TestDragAndDrop(t *testing.T) {
	d, fa, _ := newTestDriver(t, func(cmd string, a Args) (*Response, error) {
		if cmd == "find" {
			return tree(node(1, "Cell", "A", 0, 0, 100, 100), node(2, "Cell", "B", 0, 400, 100, 100)), nil
		}
		return ok(nil), nil
	})
	res := d.Execute(&flow.DragAndDropStep{From: flow.Selector{Text: "A"}, To: flow.Selector{Point: "50%, 90%"}})
	if !res.Success {
		t.Fatal(res.Message)
	}
	a := fa.sent("act")[0]
	if a.Kind != "drag" || *a.X != 50 || *a.Y != 50 || *a.X2 != 200 || *a.Y2 != 720 || *a.HoldMs != 1000 {
		t.Fatalf("drag = %+v", a)
	}
	if res := d.Execute(&flow.DragAndDropStep{BaseStep: flow.BaseStep{TimeoutMs: 20}, From: flow.Selector{Text: "Z"}, To: flow.Selector{Text: "B"}}); res.Success {
		t.Fatal("missing from should fail")
	}
}

func TestAssertVisibleAndCount(t *testing.T) {
	nodes := []Node{node(1, "Cell", "Item", 0, 0, 400, 50), node(2, "Cell", "Item", 0, 60, 400, 50)}
	d, _, _ := newTestDriver(t, screenOf(nodes...))
	if res := d.Execute(&flow.AssertVisibleStep{Selector: flow.Selector{Text: "Item"}}); !res.Success {
		t.Fatal(res.Message)
	}
	for count, want := range map[string]bool{"2": true, ">=2": true, "<2": false, ">1": true, "<=1": false, "==2": true, "x": false} {
		res := d.Execute(&flow.AssertVisibleStep{BaseStep: flow.BaseStep{TimeoutMs: 50}, Selector: flow.Selector{Text: "Item"}, Count: count})
		if res.Success != want {
			t.Errorf("count %q: success=%v, want %v (%s)", count, res.Success, want, res.Message)
		}
	}
}

func TestAssertNotVisible(t *testing.T) {
	var gone atomic.Bool
	d, _, _ := newTestDriver(t, func(cmd string, _ Args) (*Response, error) {
		if cmd == "find" {
			if gone.Load() {
				return tree(), nil
			}
			gone.Store(true)
			return tree(node(1, "Other", "Spinner", 0, 0, 10, 10)), nil
		}
		return ok(nil), nil
	})
	if res := d.Execute(&flow.AssertNotVisibleStep{Selector: flow.Selector{Text: "Spinner"}}); !res.Success {
		t.Fatal(res.Message)
	}
	d2, _, _ := newTestDriver(t, screenOf(node(1, "Other", "Spinner", 0, 0, 10, 10)))
	if res := d2.Execute(&flow.AssertNotVisibleStep{BaseStep: flow.BaseStep{TimeoutMs: 50}, Selector: flow.Selector{Text: "Spinner"}}); res.Success {
		t.Fatal("still visible should fail")
	}
}

func TestWaitUntil(t *testing.T) {
	d, _, _ := newTestDriver(t, screenOf(node(1, "Other", "Ready", 0, 0, 10, 10)))
	if res := d.Execute(&flow.WaitUntilStep{Visible: &flow.Selector{Text: "Ready"}}); !res.Success {
		t.Fatal(res.Message)
	}
	if res := d.Execute(&flow.WaitUntilStep{BaseStep: flow.BaseStep{TimeoutMs: 50}, NotVisible: &flow.Selector{Text: "Ready"}}); res.Success {
		t.Fatal("notVisible should fail")
	}
	if res := d.Execute(&flow.WaitUntilStep{BaseStep: flow.BaseStep{TimeoutMs: 50}, Visible: &flow.Selector{Text: "Gone"}}); res.Success {
		t.Fatal("visible missing should fail")
	}
	if res := d.Execute(&flow.WaitUntilStep{}); res.Success {
		t.Fatal("empty waitUntil should fail")
	}
}

func TestWaitForAnimation(t *testing.T) {
	settled := false
	d, fa, _ := newTestDriver(t, func(cmd string, _ Args) (*Response, error) {
		return ok(&Payload{Settled: &settled}), nil
	})
	res := d.Execute(&flow.WaitForAnimationToEndStep{})
	if !res.Success || !strings.Contains(res.Message, "still changing") {
		t.Fatalf("res = %+v", res)
	}
	if a := fa.sent("settle")[0]; a.TimeoutMs != 5000 {
		t.Fatalf("timeout = %v", a.TimeoutMs)
	}
	settled = true
	if res := d.Execute(&flow.WaitForAnimationToEndStep{BaseStep: flow.BaseStep{TimeoutMs: 1000}}); !strings.Contains(res.Message, "settled") {
		t.Fatalf("res = %+v", res)
	}
}

func TestInputTextEraseAndKeys(t *testing.T) {
	notVerified := false
	d, fa, _ := newTestDriver(t, func(cmd string, _ Args) (*Response, error) {
		switch cmd {
		case "find":
			return tree(node(1, "TextField", "Email", 0, 0, 400, 40)), nil
		case "type":
			return ok(&Payload{Typed: true, Verified: &notVerified}), nil
		}
		return ok(nil), nil
	})
	_ = d.SetTypingFrequency(12)
	res := d.Execute(&flow.InputTextStep{Text: "a@b.c", Selector: flow.Selector{Text: "Email"}})
	if !res.Success || !strings.Contains(res.Message, "not confirmed") {
		t.Fatalf("res = %+v", res)
	}
	if len(fa.sent("act")) != 1 {
		t.Fatal("selector input should tap the field first")
	}
	d.Execute(&flow.InputTextStep{})
	d.Execute(&flow.InputRandomStep{DataType: "NUMBER", Length: 4})
	d.Execute(&flow.EraseTextStep{})
	d.Execute(&flow.PressKeyStep{Key: "Enter"})
	types := fa.sent("type")
	if len(types) != 4 {
		t.Fatalf("types = %+v", types)
	}
	if types[0].Text != "a@b.c" || types[0].Speed != 12 {
		t.Errorf("type = %+v", types[0])
	}
	if len(types[1].Text) != 4 || types[2].Erase != 50 || types[3].Key != "enter" {
		t.Errorf("types = %+v", types)
	}
	if res := d.Execute(&flow.PressKeyStep{Key: "volume up"}); res.Success {
		t.Error("volume keys should fail on the simulator")
	}
}

func TestPressKeyError(t *testing.T) {
	d, _, _ := newTestDriver(t, func(cmd string, _ Args) (*Response, error) {
		return nil, &AgentError{Code: ErrUnsupported, Message: "key f13"}
	})
	if res := d.Execute(&flow.PressKeyStep{Key: "f13"}); res.Success {
		t.Fatal("unsupported key should fail")
	}
	if res := d.Execute(&flow.EraseTextStep{Characters: 3}); res.Success {
		t.Fatal("type error should fail erase")
	}
	if res := d.Execute(&flow.InputTextStep{Text: "x"}); res.Success {
		t.Fatal("type error should fail input")
	}
}

func TestHideKeyboard(t *testing.T) {
	visible := true
	d, _, _ := newTestDriver(t, func(cmd string, _ Args) (*Response, error) {
		return ok(&Payload{Visible: visible}), nil
	})
	if res := d.Execute(&flow.HideKeyboardStep{}); res.Success {
		t.Fatal("keyboard still up should fail")
	}
	visible = false
	if res := d.Execute(&flow.HideKeyboardStep{}); !res.Success {
		t.Fatal(res.Message)
	}
}

func TestBack(t *testing.T) {
	back := node(1, "Button", "Back", 0, 50, 80, 44)
	d, fa, _ := newTestDriver(t, screenOf(back))
	if res := d.Execute(&flow.BackStep{}); !res.Success || res.Message != "tapped Back" {
		t.Fatalf("res = %+v", res)
	}
	if a := fa.sent("act")[0]; a.Kind != "tap" || *a.X != 40 {
		t.Fatalf("act = %+v", a)
	}
	titled := node(1, "Button", "Settings", 0, 50, 120, 44)
	titled.ID = "BackButton"
	d1, fa1, _ := newTestDriver(t, screenOf(titled, node(2, "Button", "Backup", 0, 60, 100, 44)))
	if res := d1.Execute(&flow.BackStep{}); !res.Success || *fa1.sent("act")[0].X != 60 {
		t.Fatalf("UIKit back button: %+v", res)
	}
	d2, fa2, _ := newTestDriver(t, screenOf(node(2, "Button", "Backup", 0, 60, 100, 44)))
	if res := d2.Execute(&flow.PressKeyStep{Key: "back"}); !res.Success {
		t.Fatal(res.Message)
	}
	if a := fa2.sent("act")[0]; a.Kind != "swipe" || *a.X != 2 {
		t.Fatalf("edge swipe = %+v", a)
	}
}

func TestClipboard(t *testing.T) {
	n := node(1, "StaticText", "", 0, 0, 100, 20)
	n.Value = "Order #42"
	d, fa, sl := newTestDriver(t, screenOf(n))
	res := d.Execute(&flow.CopyTextFromStep{Selector: flow.Selector{Text: "Order"}})
	if !res.Success || res.Data != "Order #42" {
		t.Fatalf("copy = %+v", res)
	}
	sl.answers["pbpaste"] = "pasted"
	if res := d.Execute(&flow.PasteTextStep{}); !res.Success {
		t.Fatal(res.Message)
	}
	if ty := fa.sent("type"); ty[0].Text != "pasted" {
		t.Fatalf("typed = %+v", ty)
	}
	sl.fail["pbpaste"] = errBoom
	if res := d.Execute(&flow.PasteTextStep{}); res.Success {
		t.Fatal("pbpaste error should fail")
	}

	orig := simctlStdin
	defer func() { simctlStdin = orig }()
	var got string
	simctlStdin = func(udid, text string) *exec.Cmd {
		got = udid + ":" + text
		return exec.Command("true")
	}
	if res := d.Execute(&flow.SetClipboardStep{Text: "hi"}); !res.Success || got != "SIM-1:hi" {
		t.Fatalf("setClipboard = %+v %q", res, got)
	}
	simctlStdin = func(string, string) *exec.Cmd { return exec.Command("false") }
	if res := d.Execute(&flow.SetClipboardStep{Text: "hi"}); res.Success {
		t.Fatal("pbcopy failure should fail")
	}
}

func pngOf(w, h int) string {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, 0, color.White)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestScreenshotAndCrop(t *testing.T) {
	img := pngOf(400, 800)
	d, _, _ := newTestDriver(t, func(cmd string, _ Args) (*Response, error) {
		switch cmd {
		case "screenshot":
			return ok(&Payload{Image: img}), nil
		case "find":
			return tree(node(1, "Image", "Logo", 0, 0, 100, 50)), nil
		}
		return ok(nil), nil
	})
	res := d.Execute(&flow.TakeScreenshotStep{})
	if !res.Success || len(res.Data.([]byte)) == 0 {
		t.Fatalf("screenshot = %+v", res)
	}
	res = d.Execute(&flow.TakeScreenshotStep{CropOn: &flow.Selector{Text: "Logo"}})
	if !res.Success {
		t.Fatal(res.Message)
	}
	cropped, err := png.Decode(bytes.NewReader(res.Data.([]byte)))
	if err != nil || cropped.Bounds().Dx() != 100 {
		t.Fatalf("crop = %v %v", cropped.Bounds(), err)
	}
}

func TestAlert(t *testing.T) {
	d, fa, _ := newTestDriver(t, func(cmd string, _ Args) (*Response, error) {
		return ok(&Payload{Present: true, Message: "Allow?"}), nil
	})
	if res := d.Execute(&flow.AcceptAlertStep{}); !res.Success || !strings.Contains(res.Message, "Allow?") {
		t.Fatalf("res = %+v", res)
	}
	if a := fa.sent("alert")[0]; a.Action != "accept" {
		t.Fatalf("alert = %+v", a)
	}
	d2, _, _ := newTestDriver(t, screenOf())
	if res := d2.Execute(&flow.DismissAlertStep{BaseStep: flow.BaseStep{TimeoutMs: 50}}); !res.Success || !strings.Contains(res.Message, "no alert") {
		t.Fatalf("res = %+v", res)
	}
}

// A tap that did not take focus: the agent answers NO_FOCUS, and inputText
// taps the previous step's point again and types once more. Without a
// previous tap it fails clearly instead of passing with an empty field.
func TestInputTextRetapsWhenNothingHasFocus(t *testing.T) {
	field := node(1, "textField", "Username", 20, 100, 360, 44)
	typed := 0
	handler := func(cmd string, a Args) (*Response, error) {
		switch cmd {
		case "find", "snapshot":
			return tree(field), nil
		case "type":
			typed++
			if typed == 1 {
				return nil, &AgentError{Code: "NO_FOCUS", Message: "no field has keyboard focus"}
			}
		}
		return ok(&Payload{}), nil
	}
	d, f, _ := newTestDriver(t, handler)
	if r := d.Execute(&flow.TapOnStep{Selector: flow.Selector{Text: "Username"}}); !r.Success {
		t.Fatalf("tap failed: %s", r.Message)
	}
	// A wait between the tap and the typing keeps the tap point.
	d.Execute(&flow.WaitForAnimationToEndStep{})
	if r := d.Execute(&flow.InputTextStep{Text: "a"}); !r.Success {
		t.Fatalf("inputText failed: %s", r.Message)
	}
	if got := len(f.sent("act")); got != 2 {
		t.Errorf("taps = %d, want 2 (the step's tap and one re-tap)", got)
	}
	if typed != 2 {
		t.Errorf("type calls = %d, want 2", typed)
	}

	typed = 0
	d2, _, _ := newTestDriver(t, handler)
	if r := d2.Execute(&flow.InputTextStep{Text: "a"}); r.Success {
		t.Error("inputText with nothing focused and no previous tap should fail")
	}
}

// An optional assertVisible still fails when the element is absent: the
// executor checks `when: visible` with one, and a success made every such
// condition true.
func TestOptionalAssertVisibleFailsWhenAbsent(t *testing.T) {
	d, _, _ := newTestDriver(t, screenOf(node(1, "Button", "Other", 0, 0, 100, 40)))
	step := &flow.AssertVisibleStep{Selector: flow.Selector{Text: "Agree and Continue"}}
	step.Optional = true
	step.TimeoutMs = 50
	if r := d.Execute(step); r.Success {
		t.Fatal("optional assertVisible of an absent element must fail")
	}
	step.Selector.Text = "Other"
	if r := d.Execute(step); !r.Success {
		t.Fatalf("present element: %s", r.Message)
	}
}
