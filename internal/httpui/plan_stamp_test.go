package httpui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/overfin/overtide/internal/config"
)

// The plan page's stamp moves with what may change a plan, not with the
// disks' bytes ticking or tasks starting: the page builds the plan again
// only then.
func TestPlanStamp(t *testing.T) {
	server, _ := storageTestServer(t, `{"storage":{}}`)
	handler := server.routes()
	stamp := func() string {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/storage/plan/stamp?path=/data", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("stamp: %d", recorder.Code)
		}
		return decode[map[string]string](t, recorder)["stamp"]
	}
	first := stamp()
	server.publishUIChange("storage")
	server.publishUIChange("tasks")
	if got := stamp(); got != first {
		t.Fatalf("bytes ticking moved the stamp: %s, %s", first, got)
	}
	server.publishUIChange("inventory")
	if got := stamp(); got == first {
		t.Fatal("an inventory change didn't move the stamp")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/storage/plan?path=/data&live=1", nil))
	if plan := decode[cleanupPlanJSON](t, recorder); plan.Stamp != stamp() {
		t.Fatalf("the plan's stamp %q, now %q", plan.Stamp, stamp())
	}
}

// The Plan page's list of disks follows changes through a cheap stamp: it
// moves when automatic removal is turned on or off, not with the disks'
// bytes ticking, and /api/storage answers the stamp it was made at.
func TestDisksStamp(t *testing.T) {
	server, _ := storageTestServer(t, `{"storage":{}}`)
	handler := server.routes()
	stamp := func() string {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/storage/disks/stamp", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("stamp: %d", recorder.Code)
		}
		return decode[map[string]string](t, recorder)["stamp"]
	}
	first := stamp()
	server.publishUIChange("storage")
	if got := stamp(); got != first {
		t.Fatalf("bytes ticking moved the stamp: %s, %s", first, got)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/storage", nil))
	if got := decode[storageJSON](t, recorder).DisksStamp; got != first {
		t.Fatalf("/api/storage's stamp %q, the stamp %q", got, first)
	}
	removal := server.inv.Config().Removal
	removal.AutoMode = config.RemovalAutoConfirm
	if err := server.inv.SetRemovalSettings(removal, server.inv.Config().Protection.PrivateMinSeedingDays); err != nil {
		t.Fatal(err)
	}
	if got := stamp(); got == first {
		t.Fatal("turning automatic removal on didn't move the stamp")
	}
}
