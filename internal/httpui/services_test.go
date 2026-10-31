package httpui

import (
	"testing"
)

// The add-service window's up-front capability disclosure: Overfin never
// owns removable storage, so it's offered with the other types and the
// window explains it (frontend/src/parts/services/AddService.svelte).
func TestAddServiceTypesIncludeOverfin(t *testing.T) {
	found := false
	for _, option := range addServiceFormDataFor("").Types {
		found = found || option.Value == "overfin"
	}
	if !found {
		t.Fatal("Overfin isn't offered")
	}
}

// With nothing configured, the list is empty, not null: the page says
// "No services are configured yet."
func TestServicesListEmpty(t *testing.T) {
	handler, _ := newServiceTestServer(t, nil)
	if list := decode[servicesData](t, getPath(t, handler, "/api/services", nil)); list.Services == nil || len(list.Services) != 0 {
		t.Fatalf("%#v", list)
	}
}
