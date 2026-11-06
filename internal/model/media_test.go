package model

import "testing"

func TestNormalizeTorrentStatusMigratesLegacyStates(t *testing.T) {
	cases := map[string]string{
		"ASSOCIATED":   TorrentCurrent,
		"OPEN":         TorrentCurrent,
		"SUPERSEDED":   TorrentSuperseded,
		"ORPHANED":     TorrentOrphaned,
		"UNMATCHED":    TorrentUnassociated,
		"UNASSOCIATED": TorrentUnassociated,
		"":             TorrentUnassociated,
	}
	for input, want := range cases {
		if got := NormalizeTorrentStatus(input); got != want {
			t.Errorf("NormalizeTorrentStatus(%q)=%q, want %q", input, got, want)
		}
	}
}

// Until adding replaces them, Overswell's tag names read as Overtide's.
func TestFormerTagNamesStillRead(t *testing.T) {
	if !HasTag([]string{"Overswell-Added"}, TagAdded) || !IsManagedTag("overswell-found") || !IsManagedTag(TagNew) {
		t.Fatal("a former or current managed tag not recognized")
	}
	if !HasLabel([]int{7}, map[int]string{7: "overswell-ignore"}, TagIgnore) || HasTag([]string{"overswell-keep"}, TagKeep) {
		t.Fatal("ignore not recognized, or keep given a former name it never had")
	}
}
