# Overtide

**The storage side of [Overfin](https://github.com/Overfin/server): it keeps your disks under their limit by removing what your people value least.**

Overfin knows which titles people watch, ask for and like. Overtide knows what Overfin can't see: your disks, the files on them, the hardlinks between your library and your torrents, and what each torrent is doing. When a disk goes over its limit, Overtide removes the titles Overfin values least, with every torrent that shares their files, and it works through Radarr, Sonarr and qBittorrent, never behind their backs.

Overtide doesn't replace Radarr, Sonarr or your torrent client. They keep doing their jobs; Overtide decides what to ask of them.

> **Status: early.** Overtide starts in dry run, and should stay there for now: it plans and records what it would remove, and removes nothing. Adding titles and handling Overfin's requests come next; see [`ROADMAP.md`](ROADMAP.md).

## Starting it

```sh
mkdir overtide && cd overtide
curl -fsSLO https://raw.githubusercontent.com/overfin/overtide/main/deploy/compose.yaml
curl -fsSL -o .env https://raw.githubusercontent.com/overfin/overtide/main/deploy/.env.example
# in .env, set MEDIA_DIR to your media and downloads, MEDIA_PATH to the path
# Radarr, Sonarr and your torrent client see them at, then:
docker compose up -d
```

Open the machine's address on port 8088 and create the admin account. Then, under **Services**, add Radarr, Sonarr, qBittorrent, and Overfin: its control panel address, and a key made there under **Apps** with **Values** ticked. [`deploy/README.md`](deploy/README.md) has the details.

Images are published for amd64 and arm64 at `ghcr.io/overfin/overtide`, with a signed attestation of how they were built.

## What it does

- **Values come from Overfin, unchanged.** A film's value is Overfin's score for it, a season's is its own, a series' is its best season's. Overtide adds nothing and changes nothing. A title Overfin hasn't scored is never removed automatically, and without Overfin, nothing is.
- **It knows what a removal really frees.** It reads your file system and proves which files are hardlinks of which: a title in your library, the torrent it came from, its cross-seeds. Removing a title alone, while a torrent still holds the same file, frees nothing, so Overtide never proposes it that way.
- **Bundles, removed together.** A title goes with every torrent that shares its files, cross-seeds included. Torrents that back nothing in the library (superseded, orphaned, unassociated) go first, the one inactive the longest first; cross-seeds of one another are removed together, since each alone frees nothing. A torrent added less than seven days ago is never removed on its own.
- **Each disk has its own limit.** Overtide finds your disks from the folders your services report, and keeps each under its own target (90% by default). Space used by things outside its view is set aside, not counted against it.
- **You keep what you want.** **Keep** on a title's page or in the cleanup plan puts it on the Kept list, on the Storage page; nothing kept is ever planned for removal until you take it off the list.
- **Private trackers.** An optional setting keeps private torrents, and the titles they share files with, until they've seeded a number of days. Off by default.
- **Every removal is a plan first.** It shows exactly what would happen and how much it frees, and it's checked again just before running. Library files are removed through Radarr or Sonarr, torrents through qBittorrent. Overtide deletes a file itself only when no service claims it.
- **Three modes for automatic removal:** off, confirm (it picks, you approve) and automatic. And dry run, on by default, over everything.
- **Housekeeping:** files on disk no service claims (**Unmanaged files**), and torrents qBittorrent still lists after their files left the disk, which take no space and can be removed from qBittorrent in one go.

## The pages

- **Home**: each disk's state, the library and torrent totals, services' health, what cleanup has done so far.
- **Library**: your films and series, searchable, with Overfin's value.
- **Torrents**: every torrent, what it backs, what removing it would free, its cross-seeds.
- **Unmanaged files**: files no service claims. Removed by hand only.
- **Storage**: each disk with its limit and its cleanup plan, and the Kept list.
- **Services**, **Tasks** (the background work, with **Run now**), **History** (every removal, simulated or real), **Settings**.

## How it works with Overfin

| | Overfin | Overtide |
|---|---|---|
| Knows | titles, people's wishes and viewing | disks, files, hardlinks, torrents |
| Decides | each title's value; who may request what | what the disks keep |
| Gives the other | values per title, never who watched or wanted what | (planned) what's leaving soon, and requests' status |

Overfin values a title the same way whether it's on the server or not; files and space are entirely Overtide's concern.

## Concepts

### Torrent states

Every torrent Overtide knows is in one of four states:

- **Current**: it backs a title in the library right now, proven by Radarr's or Sonarr's import history or by the files themselves. A cross-seed of a current torrent is current too.
- **Superseded**: it backed a release since replaced by a newer import of the same title.
- **Orphaned**: it was imported for a title Radarr or Sonarr no longer has.
- **Unassociated**: nothing links it to the library. That isn't proof it's safe to remove; Overtide just has no basis to tie it to anything.

### Torrents and titles

A torrent has no value of its own: it never adds to a title's value or takes from it. A torrent goes with the title whose files it shares, and holds that title back only when something requires its files to stay: you kept it, its private tracker's seeding time isn't reached yet, or its ratio is under the minimum you set. Everything about a torrent comes from what qBittorrent reports: when it was added and last active, its seeding time, its ratio, whether its tracker is private.

## Paths

Overtide finds files where Radarr, Sonarr and qBittorrent say they are, so **every container that touches your media must mount it at the same path**. For example, all of them mount `/mnt/media` (on the host) at `/data` (inside). If your downloads live apart from your library, add the second folder in `compose.yaml` the same way. When a path a service reports can't be seen, the **Storage** page says so.

## Coming from Stewarr

Overtide reads the same config folder layout as [Stewarr](https://github.com/C0PT3R/stewarr), so a *copy* of Stewarr's `/config` brings its settings along. Never point both at the same folder. Titles and torrents carrying its keep tags (`stewarr_keep`…) are put on the Kept list after the first inventory, and the tags aren't read after that. To run both on one host, give Overtide another port: `OVERTIDE_PORT=8089` in its `.env`, and keep it in dry run: two tools removing from the same disks would remove more than either alone.

## Development

Plain Go, standard library first, like Overfin. SQLite through `database/sql` with a pure-Go driver, so the build needs no C compiler.

```bash
go test -race ./...
go run ./cmd/overtide -config ./config.json
```

The web assets are TypeScript, type-checked with `tsc` and bundled with esbuild's Go API; the bundles are committed, so building Overtide needs no Node. After changing `internal/httpui/static/src`:

```bash
npm --prefix internal/httpui/static/src ci
npm --prefix internal/httpui/static/src run typecheck
go run ./tools/buildassets
```

CI runs formatting, vet, staticcheck, govulncheck, the tests with the race detector, and checks the committed bundles match their sources. Pushing a version tag (`v0.7.0`) publishes the image and a GitHub release. [`config.example.json`](config.example.json) shows the settings file's shape, for debugging or scripted setups.

## Further reading

- [`ROADMAP.md`](ROADMAP.md): Overtide's design and what's next, then Stewarr's history.
- [`ARCHITECTURE.md`](ARCHITECTURE.md): the model and the technical architecture.
- [`Scheduler-Spec.md`](Scheduler-Spec.md): the background task scheduler.
- [`CHANGELOG.md`](CHANGELOG.md): what changed, release by release.

## Origins

Overtide is built from [Stewarr](https://github.com/C0PT3R/stewarr) 0.5.0, with its full history. Stewarr began as a storage-pressure cleanup experiment called **Spartarr** (first proposed as the regrettable **Shovitupyoarr**), then became **Togetharr**, **Connarr** and **Stewarr**. Stewarr stays a standalone tool for any *arr stack; Overtide is the one made to work with Overfin.

## AI-assisted development

Overtide's development is assisted by AI (Claude, by Anthropic), as Overfin's is. Commits it contributed to carry a `Co-Authored-By` line.

## License

MIT, see [`LICENSE`](LICENSE).
