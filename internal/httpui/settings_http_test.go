package httpui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestChangePasswordRevokesEveryOtherSession guards the actual point of a
// password rotation: it must sign out browsers other than the one making
// the change, not just accept a new password while leaving old sessions
// (e.g. from a device you no longer trust) valid.
func TestChangePasswordRevokesEveryOtherSession(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	firstSession := setUp(t, handler)
	secondSession := sessionCookieFrom(login(t, handler, "admin", "correct-horse-battery"))
	if secondSession == nil {
		t.Fatal("second login did not issue a session")
	}

	changeResponse := send(t, handler, http.MethodPut, "/api/settings/password", passwordForm{Current: "correct-horse-battery", New: "new-correct-horse", Confirm: "new-correct-horse"}, firstSession)
	if changeResponse.Code != http.StatusOK {
		t.Fatalf("change password status=%d body=%q", changeResponse.Code, changeResponse.Body.String())
	}
	newFirstSession := sessionCookieFrom(changeResponse)
	if newFirstSession == nil {
		t.Fatal("the browser making the change should get a fresh session, not be logged out too")
	}
	if stillWorks := getPath(t, handler, "/api/settings", newFirstSession); stillWorks.Code != http.StatusOK {
		t.Fatalf("the changing browser's new session should still work: status=%d", stillWorks.Code)
	}
	if revoked := getPath(t, handler, "/api/settings", secondSession); revoked.Code != http.StatusUnauthorized {
		t.Fatalf("the other browser's session should be revoked: status=%d", revoked.Code)
	}
	if old := login(t, handler, "admin", "correct-horse-battery"); !strings.Contains(old.Body.String(), "Incorrect username or password") {
		t.Fatalf("old password should no longer work: body=%q", old.Body.String())
	}
	if fresh := login(t, handler, "admin", "new-correct-horse"); fresh.Code != http.StatusOK {
		t.Fatalf("new password should work: status=%d body=%q", fresh.Code, fresh.Body.String())
	}
}

func TestChangePasswordRejectsWrongCurrentPassword(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	session := setUp(t, handler)
	response := send(t, handler, http.MethodPut, "/api/settings/password", passwordForm{Current: "totally-wrong", New: "new-correct-horse", Confirm: "new-correct-horse"}, session)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Current password is incorrect") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

// TestSetRemovalSettingsPersistsCheckboxState guards the Settings page's
// global removal switches: every one is sent each time, and false must
// turn a switch off, not leave whatever the previous save happened to be.
func TestSetRemovalSettingsPersistsCheckboxState(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	session := setUp(t, handler)

	allOn := send(t, handler, http.MethodPut, "/api/settings/removal", removalForm{AutoMode: "auto", AutoRemoveUnassociatedTorrents: true, DryRun: true}, session)
	if allOn.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", allOn.Code, allOn.Body.String())
	}
	saved := decode[settingsData](t, getPath(t, handler, "/api/settings", session))
	if saved.AutoMode != "auto" || !saved.AutoRemoveUnassociatedTorrents || !saved.DryRun {
		t.Fatalf("expected auto mode and both switches on: %#v", saved)
	}

	// Back to disabled with both switches off must turn every switch off.
	allOff := send(t, handler, http.MethodPut, "/api/settings/removal", removalForm{AutoMode: "disabled"}, session)
	if allOff.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", allOff.Code, allOff.Body.String())
	}
	saved = decode[settingsData](t, allOff)
	if saved.AutoMode != "disabled" || saved.AutoRemoveUnassociatedTorrents || saved.DryRun {
		t.Fatalf("expected every switch off after an all-off save: %#v", saved)
	}
}

// TestSetRemovalSettingsRejectsUnknownAutoModeOverHTTP guards the select
// against a malformed/hand-crafted request: an unrecognized auto mode must
// be rejected, not silently coerced into disabled or left unchanged.
func TestSetRemovalSettingsRejectsUnknownAutoModeOverHTTP(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	session := setUp(t, handler)
	response := send(t, handler, http.MethodPut, "/api/settings/removal", removalForm{AutoMode: "sometimes"}, session)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "auto_mode must be one of") {
		t.Fatalf("expected a validation error, got status=%d body=%q", response.Code, response.Body.String())
	}
}

// The private torrents' seeding days are saved, and 0 is off.
func TestSetRemovalSettingsPrivateSeedingDays(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	session := setUp(t, handler)
	set := send(t, handler, http.MethodPut, "/api/settings/removal", removalForm{AutoMode: "disabled", PrivateMinSeedingDays: 14}, session)
	if set.Code != http.StatusOK || decode[settingsData](t, set).PrivateMinSeedingDays != 14 {
		t.Fatalf("expected 14 days saved, got status=%d body=%q", set.Code, set.Body.String())
	}
	cleared := send(t, handler, http.MethodPut, "/api/settings/removal", removalForm{AutoMode: "disabled"}, session)
	if cleared.Code != http.StatusOK || decode[settingsData](t, cleared).PrivateMinSeedingDays != 0 {
		t.Fatalf("expected 0 to turn it off, got status=%d body=%q", cleared.Code, cleared.Body.String())
	}
}

// A form another site's page posts is refused: the settings only take JSON.
func TestSetRemovalSettingsRefusesAForm(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	session := setUp(t, handler)
	request := httptest.NewRequest(http.MethodPut, "/api/settings/removal", strings.NewReader(url.Values{"auto_mode": {"auto"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(session)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a form: status=%d", response.Code)
	}
	if saved := decode[settingsData](t, getPath(t, handler, "/api/settings", session)); saved.AutoMode != "disabled" {
		t.Fatalf("a form changed the settings: %#v", saved)
	}
}
