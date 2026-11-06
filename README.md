# Overtide

**The library side of [Overfin](https://github.com/Overfin/server): it brings in what your people want, makes room on your disks by removing what they want least, and keeps the library rolling.**

> This page describes the next release, 0.7.0, not out yet. The latest release is [0.6.0](https://github.com/overfin/overtide/releases/tag/v0.6.0), which `deploy/compose.yaml` installs.

Overfin knows your titles and your people: what they watch, ask for and like, and what's worth having that you don't have yet. Overtide knows what Overfin can't see: your disks, the files on them, the hardlinks between your library and your torrents, and what each torrent is doing. Together they keep a library that evolves toward the people using it:

- **Removing.** When a disk goes over its target, Overtide removes the titles Overfin values least, with every torrent that shares their files.
- **Adding.** Overtide asks Overfin what people wished for and what's worth adding, and brings it in through Radarr and Sonarr, a few searches at a time, within your trackers' limits, and only while the disk has room.

Overfin decides; Overtide manages. Radarr, Sonarr and qBittorrent keep doing their jobs, and Overtide works through them, never behind their backs.

> **Status: early.** Overtide starts with adding off and removal in dry run: it plans and records what it would remove, and changes nothing. See [`ROADMAP.md`](ROADMAP.md) for what's next.

## Starting it

```sh
# config/ made by you, not by Docker (which would make it root's)
mkdir -p overtide/config && cd overtide
curl -fsSLO https://raw.githubusercontent.com/overfin/overtide/main/deploy/compose.yaml
curl -fsSL -o .env https://raw.githubusercontent.com/overfin/overtide/main/deploy/.env.example
# in .env, set MEDIA_DIR to your media and downloads, MEDIA_PATH to the path
# Radarr, Sonarr and your torrent client see them at, then:
docker compose up -d
```

Open the machine's address on port 8088 and create the admin account. Then, under **Services**, add Radarr, Sonarr, qBittorrent, and Overfin: its control panel address, and a key made there under **Apps** with **Values** ticked (and, for adding, **What came and went** and **Wishes to fetch**). Prowlarr is optional: with it, searches are paced by your trackers' limits. Adding is off until you switch it on in **Settings**: off, dry run (reads only, changes nothing), on. [`deploy/README.md`](deploy/README.md) has the details.

Images are published for amd64 and arm64 at `ghcr.io/overfin/overtide`, with a signed attestation of how they were built.

## How it works with Overfin

| | Overfin | Overtide |
|---|---|---|
| Knows | titles, people's wishes and viewing | disks, files, hardlinks, torrents |
| Decides | each title's value, what to add first | when to add and make room, and how |
| Gives the other | each title's value | (planned) what's leaving soon |

Overfin values a title the same way whether it's on the server or not, and a title's size never counts: files and space are entirely Overtide's concern. Overfin never tells Overtide who watched or wanted what.

## Adding

- **One source: Overfin.** What people wished for in Overfin comes first, in the turns Overfin gives (each account in turn, never who), read at every run; Overtide tells Overfin what became of each (downloading, not found until its next try, not out yet), and a wish no one wants any more leaves the list. Then, every hour, what Overfin suggests adding, in its order: the titles your libraries lack, the most valuable first, with trending and popular titles mixed in by the discovery share set in Overfin. Overfin's key needs Values, What came and went, and Wishes to fetch.
- **A waiting list.** Every 15 minutes, Overtide tries the titles whose turn it is, one search at a time, at most the searches set in Settings (3 by default; each season of a series counts). At most 200 of Overfin's suggestions wait.
- **An order.** Each run's searches go to, in turn: episodes of seasons airing, seasons just finished, wishes, older incomplete seasons, what "Everything" found missing, Overfin's suggestions, then upgrades.
- **Titles a day.** At most so many titles start downloading in 24 hours, whatever brought them (10 by default, in Settings; a series counts once per season, and one with more seasons than that still goes, alone in its day). Past it, they wait until the oldest download turns a day old, for months if need be: Overfin's suggestions only get what wishes leave. Episodes of a season airing don't wait and don't count; a season just finished (its last episode aired under 30 days ago) doesn't wait, but counts.
- **Airing seasons.** Overtide follows the airing seasons it has files of, in either mode: each episode is monitored from 2 days before it airs until its download starts, so Sonarr's RSS gets it, and searched for from 2 days after it aired, every day for 14 days, then less and less often. Later seasons come through wishes and suggestions, unless the series was followed when "Everything" took it.
- **Whole seasons.** People don't start a series with holes in it. 7 days after a season's last episode aired, a season with missing episodes is replaced by a whole-season pack. When the search finds no complete pack Sonarr would take (in the languages wanted, say, not every episode is out yet), the single episodes it found that Sonarr would take come at once, and the season's other missing episodes are searched one by one right after, within the run's searches (those left go first next time). None of these counts in the titles a day: the season's search did. A single episode's file list isn't read first: Sonarr matched it to its episode. Once such a season is complete, its pack is still looked for, up to 5 tries in all, as an upgrade: within the upgrades a day, so never while upgrades are off. A season with some of its files that lacks aired episodes gets the same in its turn, the most valued first, unless it lost a file in the last 6 months other than to an upgrade: that one was removed, not missed. A season with no file is one nobody wants, and is left alone. A season still short after a whole-season pack came, grabbed after its last episode aired, isn't searched again: its files may not match Sonarr's episodes, and the Adding page says it needs a look.
- **Finished downloads.** Sonarr keeps a finished download in its queue when it refused some of its files. A file matching none of Sonarr's episodes means the numbering may differ: the download stays, and the Adding page says its season needs a look. A pack's files Sonarr thought no upgrade replace the older ones anyway, even of better quality, so a season comes from one release. Once every episode a download is for has its file, Overtide takes it off the queue; the download stays in the client, seeding.
- **Upgrades.** Off by default. With **Upgrades a day** set in Settings, Overtide searches for a better copy of what's below its quality profile's cutoff, the most valued first, with its own limit, counting searches, and using the searches left after everything else. A season is one search; a season airing or lacking episodes waits; a title is searched again after 30 days at the earliest. A title is monitored for its search and no longer. Radarr and Sonarr's cutoff lists compare qualities only, not custom format scores. Overfin's key needs Values for the order.
- **Monitor, never delete.** Adding never deletes anything: removal does, by its own rules (below). A title goes into Radarr or Sonarr once, unmonitored and tagged `overtide-added`, at its destination (below). A try monitors it, searches for that one title, and checks a download started. If not, the title is unmonitored again and tried later: after 1 day, then 2, 4, 8. After 5 tries that found nothing, or 60 days in the list, it leaves the list, and isn't taken from Overfin's suggestions again for 6 months. Nor is a title that left the libraries in the last 6 months, whoever removed it: Overfin keeps that history (`/apps/v1/history`). Once its download is confirmed, the title is unmonitored, so Radarr and Sonarr don't upgrade it on their own: upgrades are Overtide's, below.
- **Releases.** A title not out yet waits, and two days before its release it's monitored without a search, so Radarr's or Sonarr's RSS catches it.
- **What Overtide manages.** "Only what it adds" (the default), or "Everything in Radarr and Sonarr": every title is tagged, and Radarr and Sonarr no longer fetch or upgrade anything on their own; Overtide fetches what they would have, at its own pace. The two modes only differ in which titles are Overtide's.
- **Tags.** Each title Overtide manages says where it came from: `overtide-added`, `overtide-requested` (a wish), `overtide-found` (there before Overtide took it), `overtide-new` (added from outside with "Everything"). Tag a title `overtide-ignore` in Radarr or Sonarr and Overtide never takes, tags, unmonitors or searches for it. The names Overswell used (`overswell-added` and so on) and Overtide's oldest tag, `overtide`, are replaced once with these, and read as these until then.

### Importing, with Sonarr's auto-import off

Sonarr gives every file of a release one of its own episode numbers, whatever
order the release is in, and reports the season complete. Turn off **Completed
Download Handling** in Sonarr (Settings, Download Clients, then **Show
Advanced** at the top: it's the **Enable** box in that section) and Overtide
does the importing instead, for everything Sonarr downloads, however it started.
Leave it on and nothing changes. Radarr isn't concerned.

Each finished download is imported file by file:

1. A title in the file's name that matches one episode wins over its number,
   even in another season. Titles are compared with Sonarr's, and with TMDB's
   in the server's language.
2. Otherwise the number is believed when nothing else can be meant by it: no
   other order on TMDB rearranges that season, Sonarr and TMDB agree on how
   many of its episodes aired, and the download doesn't hold more files for
   the season than it has episodes.
3. Otherwise the file waits. Nothing is imported on a guess.

An anime's files named by absolute number ("Show 783") go on that episode:
such a number means one episode, whatever the seasons' order.

Nothing is asked of you: Overtide keeps trying, other releases too, and a
later release completing the season ends the wait. The Adding page lists what
waits, where you can choose the episodes yourself, try another release, or
ignore it.

Before grabbing a whole season, Overtide also reads the file list in the
`.torrent` of up to five releases, one at a time, and prefers what would go in
by itself. It always grabs something. Each read counts as a grab in Prowlarr's
history; a `.torrent` Overtide can't reach (an address only Sonarr's network
knows) is logged and skipped.

Connect Overfin for TMDB's side of this: Overtide has no TMDB token of its
own. Without it, only titles Sonarr knows can settle a file.

## Where titles go

In Settings, **Where titles go** lists the destinations: a Radarr (films) or a Sonarr (series), a root folder and a quality profile, either for one of Overfin's categories or for every title of that kind. A title goes to the first destination of a category it fits (Overfin says which, by what TMDB knows of it: kind, genres, original language, age), else to its kind's "every title". Say, Anime to its own Sonarr and folder, everything else to the usual ones. With no destination for its kind, a title goes to the first Radarr or Sonarr, when that one has a single root folder and quality profile.

## Removing

- **Values come from Overfin, unchanged, before every plan.** Overtide reads every title's score from Overfin at startup and before each cleanup plan, opened by hand or made by automatic removal (it takes seconds). A film's value is Overfin's score for it, a season's is its own, a series' is its best season's. A title Overfin hasn't scored is never removed automatically, and without Overfin, nothing is.
- **Removed, not forgotten.** Removing a title deletes its files through Radarr or Sonarr; the title stays there, unmonitored (a series, its removed episodes), so it isn't downloaded again on its own.
- **It knows what a removal really frees.** Overtide reads your file system and proves which files are hardlinks of which: a title in your library, the torrent it came from, its cross-seeds. Removing a title alone, while a torrent still holds the same file, frees nothing, so Overtide never proposes it that way.
- **Bundles, removed together.** A title goes with every torrent that shares its files, cross-seeds included. Torrents that back nothing in the library (superseded, orphaned, unassociated) go first, the one inactive the longest first; cross-seeds of one another are removed together, since each alone frees nothing. A torrent added less than seven days ago is never removed on its own.
- **Copies instead of hardlinks.** If your library holds copies of your torrents' files, each torrent is a spare copy: removing it frees its whole size and the library loses nothing, so by default copies go before any title. In Settings, "Ordered with their title" places each copy by its title's value instead, so you keep seeding what people value.
- **You keep what you want.** **Keep** on a title's page or in the cleanup plan tags it `overtide-keep` in Radarr or Sonarr (you can set the tag there yourself too); a torrent goes on the Kept list. The Storage page lists both; nothing kept is ever planned for removal until you take it off.
- **Just added.** What adding brought in (tagged `overtide-added` or `overtide-requested`) isn't removed for 30 days after its files came: a film whole, a series season by season, so people have time to watch it.
- **Private trackers.** An optional setting keeps private torrents, and the titles they share files with, until they've seeded a number of days. Off by default.
- **Every removal is a plan first.** It shows exactly what would happen and how much it frees, and it's checked again just before running. Library files are removed through Radarr or Sonarr, torrents through qBittorrent. Overtide deletes a file itself only when no service claims it.
- **Three modes for automatic removal:** off, confirm (it picks, you approve) and automatic. And dry run, on by default, over every removal, by hand or automatic.

## Disks

Overtide finds your disks from the folders your services report, and gives each two thresholds:

- **Target** (90% by default): the usage Overtide steers toward. It isn't a wall: adding goes first, and removal brings the disk back under its target. So your space stays used, and titles keep rolling.
- **Critical** (95% by default): the one hard stop. Past it, nothing more is added there (no title, no followed series' new episodes, no whole-season pack, no upgrade) until removal catches up, so a run of downloads can't fill the disk. A disk Overtide can't read or doesn't know stops adding too, except in dry run.

Space used by things outside Overtide's view is set aside, not counted against it.

## Trackers

With Prowlarr connected, Overtide reads each tracker's query limit (set in Prowlarr, in the tracker's settings) and what Prowlarr counted against it: every search and RSS poll, whoever asked. A search goes only while every tracker with a limit has room for it, a fifth of each limit kept for Radarr's and Sonarr's RSS. A search that found nothing counts as a try only when every tracker answered. Prowlarr stays as it is: Overtide changes nothing there. The **Services** page shows each tracker's limit and use.

A tracker without a limit isn't paced: set one in Prowlarr.

## The pages

- **Adding**: what Overtide is doing now, what's downloading, what waits to be imported, and the waiting list.
- **Home**: each disk's state, the library and torrent totals, services' health, what cleanup has done so far.
- **Library**: your films and series, searchable, with Overfin's value.
- **Torrents**: every torrent, what it backs, what removing it would free, its cross-seeds; and those qBittorrent still lists after their files left the disk, which take no space and can be removed from qBittorrent in one go.
- **Unmanaged files**: files on disk no service claims, removed by hand only.
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

## Coming from Overswell

Adding was a separate app, Overswell, from 2026-10-30 until it came back into Overtide. To bring its settings and its state along, once: stop both apps, back up both config folders, then run Overtide's image with Overswell's config folder mounted (writable: SQLite needs it, though nothing is written there):

```sh
docker compose run --rm -v /path/to/overswell/config:/overswell overtide -config /config/config.json -import-overswell /overswell
```

Radarr and Sonarr must be in Overtide's Services already (the same IDs, or the same addresses). Overswell's Prowlarr is added, Overfin takes Overswell's key (it may also read wishes and history), and the adding settings, destinations, waiting list, followed series and counts come along. Then start Overtide, and leave Overswell stopped for good: two apps adding to the same Radarr and Sonarr would each search and monitor.

## Coming from Stewarr

Overtide reads the same config folder layout as [Stewarr](https://github.com/C0PT3R/stewarr), so a *copy* of Stewarr's `/config` brings its settings along. Never point both at the same folder. Titles and torrents carrying its keep tags (`stewarr_keep`…) are put on the Kept list after the first inventory, and the tags aren't read after that. To run both on one host, give Overtide another port (`OVERTIDE_PORT=8089` in its `.env`) and keep it in dry run: two tools removing from the same disks would remove more than either alone.

## Development

Plain Go, standard library first, like Overfin. SQLite through `database/sql` with a pure-Go driver, so the build needs no C compiler.

```bash
go test -race ./...
go run ./cmd/overtide -config ./config.json
```

The database and the daily logs always go under `/config` (`/config/state`, `/config/log`), so that folder must exist and be writable, even when the config file is elsewhere.

The panel is a [Svelte](https://svelte.dev) app in `frontend/`, on [Overfin UI](https://github.com/overfin/ui), the look and components every Overfin panel shares. It's built into `internal/httpui/dist`, which is committed, so building Overtide needs no Node. After changing `frontend/`:

```bash
cd frontend
npm ci
npm run check
npm run build   # into internal/httpui/dist; commit it
```

CI runs formatting, vet, staticcheck, govulncheck, the tests with the race detector, and checks the committed panel matches its sources. Pushing a version tag (`v0.7.0`) publishes the image and a GitHub release. [`config.example.json`](config.example.json) shows the settings file's shape, for debugging or scripted setups.

## Further reading

- [`ROADMAP.md`](ROADMAP.md): Overtide's design and what's next, then Stewarr's history.
- [`ARCHITECTURE.md`](ARCHITECTURE.md): the model and the technical architecture.
- [`Scheduler-Spec.md`](Scheduler-Spec.md): the background task scheduler.
- [`CHANGELOG.md`](CHANGELOG.md): what changed, release by release.

## Origins

Overtide is built from [Stewarr](https://github.com/C0PT3R/stewarr) 0.5.0, with its full history. Its adding was split out into Overswell on 2026-10-30 and merged back in 2026-11. Stewarr began as a storage-pressure cleanup experiment called **Spartarr** (first proposed as the regrettable **Shovitupyoarr**), then became **Togetharr**, **Connarr** and **Stewarr**. Stewarr stays a standalone tool for any *arr stack; Overtide is the one made to work with Overfin.

## How it's made

Overtide is designed by its maintainer, who makes the architecture, security and product decisions, and written by Claude (Anthropic's AI); Claude's commits carry a `Co-Authored-By: Claude` trailer. The code is checked by its tests, linters and the dry run, on by default; it hasn't had an independent security review yet.

## License

MIT, see [`LICENSE`](LICENSE).
