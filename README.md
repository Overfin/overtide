# Overtide

> **Overfin knows what people want and watch. Your disks, your torrent client and your *arr stack know what everything costs. Overtide brings the two together and decides what the server gets and what it keeps.**

Overtide is the storage side of [Overfin](https://github.com/Overfin/server). Overfin values every title from what people say they want, what they actually watch and how well the title is rated. Overtide weighs those values against what only it can see: file sizes on disk, hardlinks, each torrent's history, the space left on each device and the traffic used. It then sends requests to Radarr and Sonarr, and removes what's least worth keeping when space runs short.

Overtide doesn't replace Radarr, Sonarr or your torrent client. They keep doing their jobs; Overtide decides what to ask of them.

> **Status: early.** Overtide starts from [Stewarr](https://github.com/C0PT3R/stewarr) 0.5.0, a standalone storage tool for the *arr stack, which stays its own project. Today Overtide still does exactly what Stewarr does (below) and still reads Jellyfin, Seerr, TMDB and IMDb itself. The link to Overfin is being built; see [`ROADMAP.md`](ROADMAP.md). Run it in dry run (the default) until then.

## How it works with Overfin

Each side does what only it can:

| | Overfin | Overtide |
|---|---|---|
| Knows | titles, ratings, people's wishes and viewing | disks, files, hardlinks, torrents, traffic |
| Decides | who may request what (quotas, approval, kids' limits) | what fits: storage, traffic, downloads at once, seeding |
| Gives the other | each title's value, and approved requests | each request's status, and what's leaving soon |

- **Values, not people.** Overfin only ever gives Overtide values and counts per title, never who wanted or watched what. Requests come with an anonymous account ID, used for counting and nothing else.
- **Requests.** In Overfin, the operator chooses where approved requests go: straight to Radarr and Sonarr, or to Overtide. With Overtide, the most valued titles download first when resources are short, and a request that has to wait says why ("traffic limit reached, expected tomorrow").
- **Making space.** When a device goes over its target, Overtide asks Overfin for the least valuable titles on the server, weighs them with their real size on disk and the value of the torrents behind them, and removes the least worth keeping, or proposes it for the operator to approve.
- **Leaving soon.** Overtide tells Overfin what it plans to remove, so people get a last chance to watch it.

Overfin's values don't depend on files: a title is valued the same way whether it's on the server or not. Storage is entirely Overtide's concern.

## What it does today

Inherited from Stewarr, and kept:

- Correlates Radarr/Sonarr library media with qBittorrent torrents using authoritative import data, not fuzzy title matching.
- Proves whether a torrent's files are still physically hardlinked to library media, so you know what removing it would *actually* free, not just what it appears to occupy.
- Ranks library media by **Retention Value** to decide what's safest to remove first when storage gets tight. Today its inputs (ratings, watch activity, requests, favourites) come from Jellyfin, Seerr, TMDB and IMDb; they will come from Overfin's values instead.
- Scores torrents by their own **Torrent Value**, from the history Overtide records: ratio, contribution over time, consistency, seeding time, private trackers.
- Finds files on disk that no service claims ("Unmanaged").
- Plans every removal as a re-validated plan, dry run by default, carried out through the owning service's own API (Radarr, Sonarr, qBittorrent) rather than by deleting files directly.

## Quickstart

```yaml
services:
  overtide:
    build: https://github.com/Overfin/overtide.git
    container_name: overtide
    restart: unless-stopped
    user: "${PUID:-1000}:${PGID:-1000}"
    ports:
      - "8088:8088"
    volumes:
      - ./config:/config
      - /mnt/media:/data
```

1. `docker compose up -d`, then open `http://<host>:8088`. Overtide creates `/config/config.json`, its database and its log directory on first start.
2. Follow the first-run screen to create the admin account, then add your services from the Services page. [`config.example.json`](config.example.json) shows the file's shape, for debugging or scripted setups.

**Dry run is on by default and should stay on for now.** In dry run, Overtide plans and records what it would do, and does nothing. If you already run Stewarr or another cleanup tool on the same disks, keep Overtide in dry run: two tools removing from the same storage, each with its own logic, would remove more than either would alone.

## Navigation

- **Home**: storage state, library and torrent summaries, service health, storage capabilities and lifetime cleanup statistics.
- **Library**: Radarr/Sonarr media, searchable, filterable, ranked by Retention Value.
- **Torrents**: the torrent client's inventory, with provenance and reclaimable space.
- **Unmanaged files**: paths no service claims. Removable by hand, never picked automatically.
- **Tasks**: background tasks, their schedules and status, and **Run now**.
- **History**: every simulated and real removal, and what came of it.

## Core concepts

### Retention Value

How much a library item deserves to stay when storage gets scarce; higher survives longer. It's deliberately separate from size: how much space something *takes* and how much it's *worth keeping* are different questions, and Overtide weighs the two only when it picks what to remove.

Protection is separate from value: a keep tag, for instance, takes an item out of automatic removal whatever its value.

### Torrent Value

An independent score for each torrent, from its own history: ratio, realized contribution over time, consistency, seeding time and private-tracker status. Every signal comes from what any torrent client reports, not just qBittorrent. It's never compared directly with Retention Value; one setting, `torrent_care_percent`, scales one onto the other when both compete for the same device's space.

### Torrent provenance

Every torrent Overtide knows about is in one of four states:

- **Current**: it backs library media right now, proven by import data or by physical hardlink identity.
- **Superseded**: it backed an older release since replaced by a newer import for the same media.
- **Orphaned**: it was imported for media Radarr/Sonarr no longer has any record of.
- **Unassociated**: no current or past link to library media is established. That isn't proof it's safe to remove; Overtide just has no basis to link it to anything.

### Removal and dry run

Every removal (media, torrent or unmanaged file) first becomes a plan showing exactly what would happen, then is checked again just before anything runs. `removal.dry_run` defaults to **true**. Managed media is removed only through Radarr/Sonarr and torrent data only through the torrent client; unmanaged files, which nothing else claims, are the only ones Overtide deletes itself, through the same checks.

Automatic removal has three modes: off, confirm (candidates are picked, a person approves) and automatic.

### Storage and devices

There's no single storage path to configure. Overtide derives storage devices from the roots each service already reports (Radarr/Sonarr root folders, torrent save paths), grouping roots on the same physical device. Each device has its own target usage (90% by default) and can be left out of automatic removal entirely.

Usage is measured against what Overtide can actually work with: space held by something outside its view (another service on the same disk, the filesystem's reserved blocks) is shown on its own line and set aside.

## Docker

`PUID`/`PGID` should match the user that owns your media and config paths on the host.

**Every container that touches your media (Radarr, Sonarr, your torrent client and Overtide) must mount the same host directory at the same path inside.** They can live in different compose files, or on different hosts with shared storage; what matters is each container's own volume line. For example, all of them mount `/mnt/media` (on the host) at `/data` (inside).

Hardlink detection, file reconciliation and Unmanaged discovery depend on Overtide being able to `stat()` the exact paths the other services report. If it can't see a path, the Storage page says so, but it can't guess the right mount: that only exists in your compose files.

## Development

```bash
go test ./...
go build ./cmd/stewarr   # the code still carries Stewarr's name; renamed next
```

The real-browser test suite uses Playwright and an installed Chromium:

```bash
PLAYWRIGHT_NODE_MODULES=/path/to/node_modules make test-browser
```

`make deploy`/`make logs` read `REMOTE`/`REMOTE_DIR` from the environment or an optional, gitignored `Makefile.local`.

## Further reading

- [`ROADMAP.md`](ROADMAP.md): the way to Overfin, then Stewarr's history and plans.
- [`ARCHITECTURE.md`](ARCHITECTURE.md): the product model and technical architecture.
- [`Scheduler-Spec.md`](Scheduler-Spec.md): the background task scheduler.
- [`CHANGELOG.md`](CHANGELOG.md): release history.

## Origins

Overtide is built from [Stewarr](https://github.com/C0PT3R/stewarr), with its full history. Stewarr began as a storage-pressure cleanup experiment called **Spartarr** (first proposed as the regrettable **Shovitupyoarr**), then became **Togetharr**, **Connarr** and **Stewarr**. Stewarr stays a standalone tool for any *arr stack; Overtide is the one made to work with Overfin.

## AI-assisted development

Overtide's development is assisted by AI (Claude, by Anthropic), as Overfin's is. Commits it contributed to carry a `Co-Authored-By` line.

## License

MIT, see [`LICENSE`](LICENSE).
