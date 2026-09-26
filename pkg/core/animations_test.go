package core

import (
	"strings"
	"testing"
)

type fakeSettings struct {
	values   map[string]string
	commands []string
}

func (f *fakeSettings) Shell(cmd string) (string, error) {
	f.commands = append(f.commands, cmd)
	parts := strings.Fields(cmd)
	switch parts[1] {
	case "get":
		if v, ok := f.values[parts[3]]; ok {
			return v + "\n", nil
		}
		return "null\n", nil
	case "put":
		f.values[parts[3]] = parts[4]
	case "delete":
		delete(f.values, parts[3])
	}
	return "", nil
}

// Disabling zeroes the three scales; restoring puts back what was there,
// including a scale that was unset.
func TestAndroidAnimations(t *testing.T) {
	dev := &fakeSettings{values: map[string]string{
		"window_animation_scale":     "1.0",
		"transition_animation_scale": "0.5",
	}}
	var a AndroidAnimations
	if err := a.Set(dev, true); err != nil {
		t.Fatal(err)
	}
	for _, k := range androidAnimationScales {
		if dev.values[k] != "0" {
			t.Errorf("%s = %q after disable, want 0", k, dev.values[k])
		}
	}
	// A second disable keeps the first saved values.
	if err := a.Set(dev, true); err != nil {
		t.Fatal(err)
	}
	if err := a.Set(dev, false); err != nil {
		t.Fatal(err)
	}
	if dev.values["window_animation_scale"] != "1.0" || dev.values["transition_animation_scale"] != "0.5" {
		t.Errorf("restored %v, want the original scales", dev.values)
	}
	if _, ok := dev.values["animator_duration_scale"]; ok {
		t.Errorf("animator_duration_scale was unset before and should be deleted again")
	}
	// Restoring again does nothing.
	n := len(dev.commands)
	if err := a.Set(dev, false); err != nil || len(dev.commands) != n {
		t.Errorf("second restore ran commands or failed: %v", err)
	}
}
