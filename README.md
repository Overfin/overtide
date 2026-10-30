# Overtide

**The storage side of [Overfin](https://github.com/Overfin/server): it fills your disks with what your people want, and keeps the library rolling.**

Overfin knows your titles and your people: what they watch, ask for and like, and what's worth having that you don't have yet. Overtide knows what Overfin can't see: your disks, the files on them, the hardlinks between your library and your torrents, and what each torrent is doing. Together they keep a library that evolves toward the people using it:

- **Adding.** Every hour, Overtide asks Overfin what to add, and adds it through Radarr and Sonarr.
- **Removing.** When a disk goes over its target, Overtide removes the titles Overfin values least, with every torrent that shares their files.

Overfin decides; Overtide manages. Radarr, Sonarr and qBittorrent keep doing their jobs, and Overtide works through them, never behind their backs.

> **Status: early.** Overtide starts with adding off and removal in dry run: it plans and records what it would remove, and changes nothing. Adding has its own dry run too. See [`ROADMAP.md`](ROADMAP.md) for what's next.

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

## How it works with Overfin

| | Overfin | Overtide |
|---|---|---|
| Knows | titles, people's wishes and viewing | disks, files, hardlinks, torrents |
| Decides | each title's value, and what's worth adding | when to add, when to make room, and how |
| Gives the other | what to add, in order; each title's value | (planned) what's leaving soon, and requests' status |

Overfin values a title the same way whether it's on the server or not, and a title's size never counts: files and space are entirely Overtide's concern. Overfin never tells Overtide who watched or wanted what.

## Adding

Adding is off until you switch it on, on the **Adding** page (off, dry run, on).

- **One source: Overfin.** Every hour, the **Overfin suggestions** task reads what Overfin says to add, in its order: the titles your libraries lack, the most valuable first, with trending and popular titles mixed in by the discovery share you set in Overfin. People's requests will always go first (coming).
- **A waiting list.** Every 15 minutes, Overtide tries the titles whose turn it is, one search at a time, at most 3 searches a run (each season of a series counts). At most 200 of Overfin's suggestions wait.
- **Monitor, never delete.** A title goes into Radarr or Sonarr once, unmonitored and tagged `overtide-added` (`overtide-requested` for a request), with their root folder and quality profile (Overtide adds only where there is one of each). A try monitors it (for a series, the seasons Overfin suggests), asks for a search of that one title, and checks a download started. If not, the title is unmonitored again and tried later: after 1 day, then 2, 4, 8. After 5 tries that found nothing, or 60 days in the list, it leaves the list, and isn't taken from Overfin's suggestions again for 6 months. Once its download is confirmed, the title is unmonitored, so Radarr and Sonarr don't upgrade it on their own: Overtide decides what comes in. It's imported all the same; a download that stops before its import puts the title back in line.
- **Releases.** A title not out yet waits, and two days before its release it's monitored without a search, so Radarr's or Sonarr's RSS catches it. Nothing within 14 days counts as a try that found nothing.
- **What Overtide manages.** "Only what it adds" (the default): its own additions, and the titles it removes, once it does; everything else is left to Radarr and Sonarr. "Everything in Radarr and Sonarr": every title is tagged, and Radarr and Sonarr no longer fetch or upgrade anything on their own. Overtide fetches what they would have, at its own pace: monitored films without their file go in line; monitored missing episodes are searched one at a time; the series whose new seasons were followed get each new episode monitored from two days before it airs until it's downloaded, so Sonarr's RSS grabs it. What was unmonitored stays unwanted. Switching back leaves titles unmonitored.
- **Whole seasons.** With "Everything", 7 days after a season's last episode aired, a season with missing episodes is replaced by a whole-season pack, so its episodes come from one release: the first complete pack Sonarr refused only for the files already there. Once it's downloaded, Sonarr's manual import brings in what Sonarr didn't take, in place of the older files (into its recycle bin, if it has one). Nothing is replaced before the pack is there.
- **Tags.** Each title Overtide manages carries where it came from: `overtide-added`, `overtide-requested`, `overtide-found` (there before Overtide took it), `overtide-new` (added from outside with "Everything"). Two tags are yours to set, in Radarr or Sonarr or from Overtide: `overtide-keep`, never planned for removal; `overtide-ignore` (**Leave alone** on a title's page), never taken, tagged, unmonitored or searched by Overtide, though it can still be removed. Radarr and Sonarr don't record how a title was added: to tell your lists or other request apps apart, give them tags of their own there.
- **Import lists are yours.** Overtide doesn't read them or work around them. Keep them in Radarr and Sonarr if you like; when Overtide removes a title, the removal can add it to their exclusions so a list doesn't bring it back.

## Removing

- **Values come from Overfin, unchanged, when space is needed.** Overtide asks Overfin at startup, and when a disk with automatic removal is over its target and Overfin's last answer is older than 15 minutes. A film's value is Overfin's score for it, a season's is its own, a series' is its best season's. A title Overfin hasn't scored is never removed automatically, and without Overfin, nothing is.
- **Removed, not forgotten.** Removing a title deletes its files through Radarr or Sonarr; the title stays there, unmonitored (a series, its removed episodes) and tagged `overtide-found` if it had none of Overtide's tags, so it isn't downloaded again on its own.
- **It knows what a removal really frees.** Overtide reads your file system and proves which files are hardlinks of which: a title in your library, the torrent it came from, its cross-seeds. Removing a title alone, while a torrent still holds the same file, frees nothing, so Overtide never proposes it that way.
- **Bundles, removed together.** A title goes with every torrent that shares its files, cross-seeds included. Torrents that back nothing in the library (superseded, orphaned, unassociated) go first, the one inactive the longest first; cross-seeds of one another are removed together, since each alone frees nothing. A torrent added less than seven days ago is never removed on its own.
- **Copies instead of hardlinks.** If your library holds copies of your torrents' files, each torrent is a spare copy: removing it frees its whole size and the library loses nothing, so by default copies go before any title. In Settings, "Ordered with their title" places each copy by its title's value instead, so you keep seeding what people value.
- **You keep what you want.** **Keep** on a title's page or in the cleanup plan tags it `overtide-keep` in Radarr or Sonarr (you can set the tag there yourself too); a torrent goes on the Kept list. The Storage page lists both; nothing kept is ever planned for removal until you take it off.
- **Private trackers.** An optional setting keeps private torrents, and the titles they share files with, until they've seeded a number of days. Off by default.
- **Every removal is a plan first.** It shows exactly what would happen and how much it frees, and it's checked again just before running. Library files are removed through Radarr or Sonarr, torrents through qBittorrent. Overtide deletes a file itself only when no service claims it.
- **Three modes for automatic removal:** off, confirm (it picks, you approve) and automatic. And dry run, on by default, over every removal, by hand or automatic; adding has its own.

## Disks

Overtide finds your disks from the folders your services report, and gives each two thresholds:

- **Target** (90% by default): the usage Overtide steers toward. It isn't a wall: Overtide adds first, and removal brings the disk back under its target. So your space stays used, and titles keep rolling.
- **Critical** (95% by default): the one hard stop. Past it, nothing more is added until removal catches up, so a run of downloads can't fill the disk.

Space used by things outside Overtide's view is set aside, not counted against it.

## The pages

- **Home**: each disk's state, the library and torrent totals, services' health, what cleanup has done so far.
- **Library**: your films and series, searchable, with Overfin's value.
- **Torrents**: every torrent, what it backs, what removing it would free, its cross-seeds.
- **Unmanaged files**: files on disk no service claims, removed by hand only; and torrents qBittorrent still lists after their files left the disk, which take no space and can be removed from qBittorrent in one go.
- **Adding**: the waiting list, what's downloading, what's waiting for its release, and what came in.
- **Storage**: each disk with its target and its cleanup plan, and the Kept list.
- **Services**, **Tasks** (the background work, with **Run now**), **History** (every removal, simulated or real), **Settings**.

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

Overtide reads the same config folder layout as [Stewarr](https://github.com/C0PT3R/stewarr), so a *copy* of Stewarr's `/config` brings its settings along. Never point both at the same folder. Titles and torrents carrying its keep tags (`stewarr_keep`…) are put on the Kept list after the first inventory, and the tags aren't read after that. To run both on one host, give Overtide another port (`OVERTIDE_PORT=8089` in its `.env`) and keep it in dry run: two tools removing from the same disks would remove more than either alone.

## Development

Plain Go, standard library first, like Overfin. SQLite through `database/sql` with a pure-Go driver, so the build needs no C compiler.

```bash
go test -race ./...
go run ./cmd/overtide -config ./config.json
```

The database and the daily logs always go under `/config` (`/config/state`, `/config/log`), so that folder must exist and be writable, even when the config file is elsewhere.

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

Said plainly, as for Overfin: Overtide is written by Claude (Anthropic's AI): its code, its tests and its documentation. The maintainer gives directions (what it should do, how, what to change) and runs it on a real server, but doesn't write the code or review it line by line. Commits Claude wrote carry a `Co-Authored-By: Claude` trailer. What checks the code is what runs before every push (the tests with the race detector, vet, staticcheck, govulncheck), and the dry run, on by default, before anything is removed or added for real.

## License

MIT, see [`LICENSE`](LICENSE).
