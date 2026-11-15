package httpui

import (
	"testing"
	"time"
)

func TestPageOf(t *testing.T) {
	for _, c := range []struct{ total, page, size, wantPage, wantPages, from, to int }{
		{0, 1, 50, 1, 1, 0, 0},
		{120, 1, 50, 1, 3, 0, 50},
		{120, 3, 50, 3, 3, 100, 120},
		{120, 9, 50, 3, 3, 100, 120}, // past the end: the last page
		{120, -2, 50, 1, 3, 0, 50},
	} {
		page, pages, from, to := pageOf(c.total, c.page, c.size)
		if page != c.wantPage || pages != c.wantPages || from != c.from || to != c.to {
			t.Errorf("pageOf(%d, %d, %d) = %d %d %d %d", c.total, c.page, c.size, page, pages, from, to)
		}
	}
}

func TestSortUpgrades(t *testing.T) {
	now := time.Now()
	rows := func() []upgradeRow {
		return []upgradeRow{
			{ID: "a", Title: "Bravo", Value: 2, LastSearched: now},
			{ID: "b", Title: "alpha", Value: 9},
			{ID: "c", Title: "Charlie", Value: 2, LastSearched: now.Add(-time.Hour)},
		}
	}
	ids := func(r []upgradeRow) string {
		out := ""
		for _, x := range r {
			out += x.ID
		}
		return out
	}
	for _, c := range []struct{ key, order, want string }{
		{"order", "asc", "abc"},
		{"order", "desc", "cba"},
		{"title", "asc", "bac"},
		{"value", "desc", "bac"}, // ties keep the search order
		{"last", "asc", "bca"},
	} {
		r := rows()
		sortUpgrades(r, c.key, c.order)
		if got := ids(r); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.key, c.order, got, c.want)
		}
	}
}
