# Overtide

Overtide is the storage side of Overfin: it reads each title's value from
Overfin (its apps API, `/apps/v1/titles`), weighs it against what only it
knows (files, hardlinks, torrents and their history, disk space), and
decides what the server keeps; requests and resource limits come next.
Built from Stewarr 0.5.0, which stays a project of its own. MIT, public,
part of the Overfin project: it works the way Overfin's server does.

## Non-negotiable
- Never copy or port code from other projects. Build from the Radarr,
  Sonarr, qBittorrent and Overfin APIs and their official docs.
- Standard library first. Any new dependency needs a stated reason.
- This repo is public. Never commit details of the maintainer's own setup:
  domains, IPs, hostnames, usernames, emails, network layout or secrets. Use
  `example.org` and documentation IPs (192.0.2.0/24, 198.51.100.0/24) in
  examples.
- AI assistance is always disclosed: keep the README's "AI-assisted
  development" section accurate, and end commits Claude contributed to with a
  `Co-Authored-By: Claude ...` trailer.
- Dry run stays the default: nothing is removed, or asked of Radarr and
  Sonarr, until the operator turns it off.

## Style
- Plain, idiomatic Go. Clear code over clever code.
- `gofmt`, `go vet ./...`, staticcheck and `go test -race ./...` must pass;
  CI (`.github/workflows/ci.yml`) runs them, govulncheck, and checks the
  committed web bundles match their TypeScript sources (rebuild with
  `go run ./tools/buildassets`).
- SQLite through `database/sql` and the pure-Go `modernc.org/sqlite`, as in
  Overfin: no cgo; the image is one static binary on distroless.
- Releases: a version tag (`v0.6.0`) publishes `ghcr.io/overfin/overtide`
  and a GitHub release (`.github/workflows/release.yml`); the version comes
  from the tag (`internal/product.Version`).

## Layout
- `cmd/overtide`: entry point
- `internal/httpui`: the web UI, server-rendered (templates, htmx, Stimulus)
- `internal/inventory`: what the services report, reconciled
- `internal/valuation`: Retention Value (Overfin's scores when it's
  configured) and Torrent Value
- `internal/services/*`: one client per service (Radarr, Sonarr,
  qBittorrent, Overfin…)
- `internal/store`: SQLite
- `internal/tasks`: the scheduler (Scheduler-Spec.md)
- `deploy/`: Compose file for operators
