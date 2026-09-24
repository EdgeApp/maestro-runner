package flow

import (
	"strings"
	"testing"
)

// `- action: <word>` is Maestro's old spelling of five commands. Suites still
// use it (duckduckgo/Android: 84 times across 30 flows), and it must parse to
// the same step as the modern spelling.
func TestLegacyActionSpelling(t *testing.T) {
	tests := map[string]StepType{
		"back":          StepBack,
		"hideKeyboard":  StepHideKeyboard,
		"scroll":        StepScroll,
		"clearKeychain": StepClearKeychain,
		"pasteText":     StepPasteText,
	}
	for word, want := range tests {
		t.Run(word, func(t *testing.T) {
			f, err := Parse([]byte("appId: com.example\n---\n- action: "+word+"\n"), "flow.yaml")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(f.Steps) != 1 || f.Steps[0].Type() != want {
				t.Fatalf("got %v, want one %s step", f.Steps, want)
			}
		})
	}
}

func TestLegacyActionNested(t *testing.T) {
	yamlText := "appId: com.example\n---\n- retry:\n    maxRetries: 1\n    commands:\n      - action: back\n"
	if _, err := Parse([]byte(yamlText), "flow.yaml"); err != nil {
		t.Fatalf("nested action: back did not parse: %v", err)
	}
}

func TestLegacyActionUnknownWord(t *testing.T) {
	_, err := Parse([]byte("appId: com.example\n---\n- action: fly\n"), "flow.yaml")
	if err == nil || !strings.Contains(err.Error(), "unknown action: fly") {
		t.Fatalf("err = %v, want an unknown action error naming the word", err)
	}
}
