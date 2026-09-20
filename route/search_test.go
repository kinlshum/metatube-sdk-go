package route

import (
	"reflect"
	"testing"
)

func TestSplitProviderNames(t *testing.T) {
	got := splitProviderNames(" JavDB, javlibrary,JAVDB,, AVBASE ")
	want := []string{"JavDB", "javlibrary", "AVBASE"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitProviderNames() = %#v, want %#v", got, want)
	}
}

func TestSplitProviderNamesEmpty(t *testing.T) {
	if got := splitProviderNames(" , "); len(got) != 0 {
		t.Fatalf("splitProviderNames() = %#v, want empty", got)
	}
}
