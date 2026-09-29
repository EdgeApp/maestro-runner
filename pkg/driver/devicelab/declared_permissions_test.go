package devicelab

import (
	"reflect"
	"sort"
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
)

const dumpsysSample = `Packages:
  Package [com.app] (abc):
    userId=10123
    requested permissions:
      android.permission.INTERNET
      android.permission.CAMERA
      android.permission.READ_EXTERNAL_STORAGE: restricted=true
    install permissions:
      android.permission.INTERNET: granted=true
    runtime permissions:
      android.permission.CAMERA: granted=false
`

func TestParseRequestedPermissions(t *testing.T) {
	got := parseRequestedPermissions(dumpsysSample)
	want := map[string]bool{"android.permission.INTERNET": true, "android.permission.CAMERA": true, "android.permission.READ_EXTERNAL_STORAGE": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if parseRequestedPermissions("nothing here") != nil {
		t.Error("no section should give nil")
	}
}

// "all" grants only what the app declares; an unreadable app keeps the full list.
func TestDeclaredOf(t *testing.T) {
	d := New(newTrackingClient(), &core.PlatformInfo{}, &mockShell{out: dumpsysSample})
	got := d.declaredOf("com.app", []string{"android.permission.CAMERA", "android.permission.ACCESS_FINE_LOCATION", "android.permission.READ_EXTERNAL_STORAGE"})
	sort.Strings(got)
	if want := []string{"android.permission.CAMERA", "android.permission.READ_EXTERNAL_STORAGE"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	d2 := New(newTrackingClient(), &core.PlatformInfo{}, &mockShell{out: ""})
	all := []string{"a", "b"}
	if got := d2.declaredOf("com.x", all); !reflect.DeepEqual(got, all) {
		t.Errorf("unreadable app: got %v, want %v", got, all)
	}
}
