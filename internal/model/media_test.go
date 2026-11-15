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

// Adding's tags read whatever their case; keep has no other name.
func TestTagNames(t *testing.T) {
	if !HasTag([]string{"Overtide-Added"}, TagAdded) || !IsManagedTag("overtide-found") || IsManagedTag("overswell-found") {
		t.Fatal("a managed tag not recognized, or a former name read")
	}
	if !HasLabel([]int{7}, map[int]string{7: "overtide-ignore"}, TagIgnore) || HasTag([]string{"overswell-keep"}, TagKeep) {
		t.Fatal("ignore not recognized, or keep given another name")
	}
}
