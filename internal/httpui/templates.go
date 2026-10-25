package httpui

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/overfin/overtide/internal/product"
)

// UI source and its pinned browser dependencies ship inside the Overtide
// binary. Running Overtide never depends on a CDN or a JavaScript build service.
//
//go:embed templates/*.html static/*
var uiFiles embed.FS

func parseUITemplate(name string, functions template.FuncMap) (*template.Template, error) {
	content, err := uiFiles.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("read UI template %s: %w", name, err)
	}
	parsed, err := template.New(name).Funcs(functions).Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("parse UI template %s: %w", name, err)
	}
	return parsed, nil
}

func uiStaticHandler() (http.Handler, error) {
	staticFiles, err := fs.Sub(uiFiles, "static")
	if err != nil {
		return nil, fmt.Errorf("open embedded UI assets: %w", err)
	}
	return http.StripPrefix("/assets/", http.FileServer(http.FS(staticFiles))), nil
}

func appHead() template.HTML {
	// htmx and Stimulus are bundled into app.js by tools/buildassets rather
	// than served as separate <script> tags — every page load already
	// competes for the browser's ~6-connections-per-origin limit (no TLS,
	// no HTTP/2 multiplexing here), so one fewer script request matters.
	return template.HTML(`<link rel="icon" href="/assets/logo.svg" type="image/svg+xml">
<link rel="stylesheet" href="/assets/app.css">
<script defer src="/assets/app.js"></script>`)
}

// navIcons are the menu's line icons, drawn like Overfin's control panel's
// (24-unit grid, stroked in the text colour).
var navIcons = map[string][]string{
	"home":      {"M3 11l9-7 9 7", "M5 10v10h14V10", "M10 20v-6h4v6"},
	"library":   {"M4 5h16v14H4z", "M4 9h16", "M9 5v14"},
	"torrents":  {"M12 4v11", "M7 10l5 5 5-5", "M5 20h14"},
	"unmanaged": {"M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z", "M12 11v3", "M12 16.5h.01"},
	"storage":   {"M4 5h16v6H4z", "M4 13h16v6H4z", "M8 8h.01", "M8 16h.01"},
	"services":  {"M9 7V3", "M15 7V3", "M7 7h10v4a5 5 0 0 1-10 0z", "M12 16v5"},
	"tasks":     {"M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z", "M12 7v5l3 2"},
	"history":   {"M4 6h16", "M4 12h16", "M4 18h10"},
	"settings":  {"M4 6h10", "M18 6h2", "M4 12h4", "M12 12h8", "M4 18h12", "M16 4v4", "M10 10v4", "M18 16v4"},
}

func navIcon(key string) string {
	var icon strings.Builder
	icon.WriteString(`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">`)
	for _, path := range navIcons[key] {
		icon.WriteString(`<path d="` + path + `"/>`)
	}
	icon.WriteString(`</svg>`)
	return icon.String()
}

func appChrome(active string) string {
	links := []struct{ Group, Key, Label, URL string }{
		{"", "home", "Home", "/"},
		{"Media", "library", "Library", "/library"},
		{"Media", "torrents", "Torrents", "/torrents"},
		{"Media", "unmanaged", "Unmanaged files", "/unmanaged"},
		{"Server", "storage", "Storage", "/storage"},
		{"Server", "services", "Services", "/services"},
		{"Server", "tasks", "Tasks", "/tasks"},
		{"Server", "history", "History", "/history"},
		{"Server", "settings", "Settings", "/settings"},
	}
	var chrome strings.Builder
	chrome.WriteString(`<header class="appbar"><a class="appbrand" href="/"><img src="/assets/logo.svg" alt=""><div><strong>` + product.Name + `</strong><small>Storage</small></div></a><nav class="appnav" aria-label="Primary">`)
	group := ""
	for _, link := range links {
		if link.Group != group {
			group = link.Group
			chrome.WriteString(`<div class="group">` + group + `</div>`)
		}
		classAttribute := ""
		ariaCurrent := ""
		if link.Key == active {
			classAttribute = ` class="active"`
			ariaCurrent = ` aria-current="page"`
		}
		chrome.WriteString(`<a` + classAttribute + ariaCurrent + ` href="` + link.URL + `">` + navIcon(link.Key) + `<span>` + link.Label + `</span></a>`)
	}
	chrome.WriteString(`</nav><div class="appfoot"><a id="operation-indicator" class="operation-indicator" href="/tasks" hidden></a><form class="signout-form" method="post" action="/logout"><button type="submit" class="ghost signout-button">Sign out</button></form></div></header>
<aside id="updates-available" class="updates-available" hidden data-controller="updates"><button type="button" data-action="updates#apply">Updates available</button></aside>
<div id="persistent-notices" class="persistent-notices" aria-live="polite"></div>
<div id="modal-root" class="modal-root"></div>
<div id="ui-announcer" class="visually-hidden" aria-live="polite" aria-atomic="true"></div>`)
	return chrome.String()
}
