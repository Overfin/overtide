package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The database keeps a session's hash, never the token itself: a copy of
// it doesn't sign anyone in. A row from before, with the token as it was,
// is dropped when the store opens.
func TestSessionsKeepOnlyAHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overtide.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const token = "a-random-session-token"
	if err := db.CreateSession(token, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.SessionValid(token); !ok || err != nil {
		t.Fatalf("valid %v, err %v", ok, err)
	}
	var kept string
	if err := db.q.QueryRow(`SELECT token FROM sessions`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(kept, token) || !strings.HasPrefix(kept, "sha256:") {
		t.Fatalf("the database keeps %q", kept)
	}
	if ok, _ := db.SessionValid(kept); ok {
		t.Fatal("the kept value signs in")
	}
	if err := db.exec(`INSERT INTO sessions(token,created_at,expires_at) VALUES(?,?,?)`, "old-plain-token", rfc3339(time.Now()), rfc3339(time.Now().Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows int
	if err := db.q.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("%d sessions after opening, want the hashed one", rows)
	}
	if ok, err := db.SessionValid(token); !ok || err != nil {
		t.Fatalf("after opening again: valid %v, err %v", ok, err)
	}
	if err := db.DeleteSession(token); err != nil {
		t.Fatal(err)
	}
	if ok, _ := db.SessionValid(token); ok {
		t.Fatal("still valid after DeleteSession")
	}
}
