package core

import "strings"

// AnimationController is implemented by drivers that can switch off the
// device's system animations for a run (disableAnimations).
type AnimationController interface {
	SetAnimationsDisabled(disabled bool) error
}

// androidAnimationScales are the three global settings Android test tools
// zero to disable animations (Espresso's guidance, Detox, agent-device).
// animator_duration_scale is the one app animators follow: at 0 an
// AnimatorSet or ObjectAnimator finishes at once.
var androidAnimationScales = []string{
	"window_animation_scale",
	"transition_animation_scale",
	"animator_duration_scale",
}

type shellRunner interface {
	Shell(cmd string) (string, error)
}

// AndroidAnimations switches a device's animation scales off and puts back
// exactly what was there before, including a setting that was unset.
type AndroidAnimations struct {
	saved map[string]string // nil until disabled; "" means the setting was unset
}

// Set disables the animations, or restores the saved scales. Restoring when
// nothing was disabled is a no-op.
func (a *AndroidAnimations) Set(sh shellRunner, disabled bool) error {
	if disabled {
		if a.saved == nil {
			saved := make(map[string]string, len(androidAnimationScales))
			for _, key := range androidAnimationScales {
				out, err := sh.Shell("settings get global " + key)
				if err != nil {
					return err
				}
				value := strings.TrimSpace(out)
				if value == "null" {
					value = ""
				}
				saved[key] = value
			}
			a.saved = saved
		}
		for _, key := range androidAnimationScales {
			if _, err := sh.Shell("settings put global " + key + " 0"); err != nil {
				return err
			}
		}
		return nil
	}
	if a.saved == nil {
		return nil
	}
	for _, key := range androidAnimationScales {
		cmd := "settings delete global " + key
		if value := a.saved[key]; value != "" {
			cmd = "settings put global " + key + " " + value
		}
		if _, err := sh.Shell(cmd); err != nil {
			return err
		}
	}
	a.saved = nil
	return nil
}
