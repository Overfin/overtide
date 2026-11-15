# Overtide Roadmap

This file separates implemented behavior from intended direction. It is not a promise of release dates. `CHANGELOG.md` has the release-by-release history; Stewarr's roadmap, which this file held until 2026-11, is in git history.

## The design

**The vision.** A library that keeps renewing itself, toward what the people of the server like. Overtide brings new titles in and takes weaker ones out, so the library drifts over time toward the operator's and people's preferences, and it keeps every disk under its threshold: a full disk would stop everything, so staying under it is Overtide's responsibility, and renewal happens within it.

**Foundations, kept from Stewarr.**
1. **Hardlinked bundles.** Overtide proves through the filesystem (device and inode) which files share the same data, so it knows exactly what a removal frees before it acts.
2. **What the torrent client knows**: when a torrent was added and last active, its seeding time, its ratio, whether its tracker is private. (Stewarr's Torrent Value, scored from a sampled history, is gone: once torrents stopped weighing on titles, it only ordered torrents that all go anyway.)
3. **Every action through the file's owner**: Radarr, Sonarr, the torrent client. Overtide deletes directly only what no service owns, checked again right before.

**Roles.** Overfin values every title, in the libraries or not (tastes, viewing, requests, favourites, watchlist, ratings, new release, recently added, popularity, the operator's weights), and keeps requests and the limits on people. Overtide knows the storage, decides what to remove, and adds. **Overtide has no valuation of its own**: it keeps a copy of Overfin's scores, for its pages and its plans, read again when it plans.

**Removing.** The unit is a bundle: a title's files and every torrent sharing them, cross-seeds included. Torrents don't weigh on a title: a bundle is worth its title's Overfin score. Only real obligations hold a bundle back: what the operator kept (the Kept list, which replaces keep tags), a private tracker's seeding time (one setting, off by default), the minimum ratio. When a disk is over its threshold, torrents with no title behind them go first, the one inactive the longest first, then the lowest bundles, until it's back under. Overtide reads the scores of every title the libraries have (`/apps/v1/titles?inLibrary=true&fields=score`) and plans from that copy. Overfin out of reach: nothing is planned automatically.

**Adding.** Adding was a separate app, Overswell, from 2026-10-30 until 2026-11; it's part of Overtide again: its Sonarr and Radarr drivers are packages, its jobs scheduler tasks, the room check a function call.

**With Overfin** (user, 2026-10-25). Overfin gives its opinion, the least valuable titles in the libraries; Overtide decides what actually goes out, and when, from space and the rules above.
- **Removing:** when it needs space, Overtide removes from the titles Overfin values least (it reads every title's score, and skips what it can't remove).
- **The goal:** use the space up to each disk's target and keep titles rolling, so the library keeps evolving toward the people using it. A title's size never matters in choosing.
- **The target is soft:** adding goes first, and removal brings the disk back under its target. Only the critical threshold stops adding, until removal catches up.
- **Import lists are the operator's business** (user, 2026-11-06): Overtide never reads or changes them, and nothing of them goes to Overfin; what a list adds is taken like anything added by hand. Radarr's and Sonarr's add events (a webhook Overtide makes, its address found by a test event or set in the Adding settings) start a run at once, which takes the title; what's added from outside since "Manage all" (by hand, by a list, by another app) is searched before anything else. The removal form's optional import-list exclusion stays for operators who use lists.
- **Thresholds** (built 2026-10-25): each disk's target and critical are set in its settings on the Storage page; critical must be above the target.
- **Removing** (built 2026-10-25): files deleted through Radarr or Sonarr, the entry kept, always unmonitored (a series, its removed episodes), its tags left as they are. Overfin's scores are read again before every plan, opened by hand or made by automatic removal (and at startup).

**Safety.** Dry run first and by default ("would remove Y and Z, 12 GB"); then confirm (proposals approved by hand) and automatic.

**Look.** Overfin's control panel: the menu on the left, the same colours, cards, tables, forms and sliders, light and dark, no operator branding. The pages are a Svelte app on Overfin UI, the panels' shared kit.

**To try first, then adjust.** Not built: keeping disks a few points under their threshold (plans stop at the target). Automatic removal is available, as the third mode.

## Building it

1. ~~**Overtide's name in the code.**~~ Done (0.6.0).
2. ~~**Overfin as a service.**~~ Done (0.6.0): address and a key with Values, a connection test.
3. ~~**Built like Overfin.**~~ Done (0.6.0): module, pure-Go SQLite, distroless image, CI and releases, `deploy/`.
4. ~~**Overfin's look.**~~ Done (0.7.0): the menu on the left, Overfin's theme values, light and dark.
5. ~~**No valuation of its own.**~~ Done (0.7.0): the Jellyfin, Seerr, TMDB and IMDb adapters, their tasks and settings, the IMDb table and the Retention Value weights are gone. A title's value is Overfin's score, unchanged; what Overfin didn't score is protected, and without Overfin nothing is removed automatically. Overtide keeps a copy of Overfin's scores between reads (decided 2026-11-01): its pages show them, and asking only the least valuable titles couldn't fill those.
6. ~~**The removal planner.**~~ Done (0.7.0): lone torrents first, then titles by Overfin's score with their hardlinked torrents; no torrent care and no share per service; titles are kept by the `overtide-keep` tag, torrents by the Kept list; private torrents recognized, with an optional seeding time.
7. **Adding** (built; apart in Overswell from 2026-10-30, back in Overtide in 2026-11): following titles through the stack (states); trying a title (monitor, search, keep it monitored if a download started, else unmonitor it and retry later: monitor, never delete); Overfin's suggestions as the only source; pace; every action covered by dry run (built 2026-10-25). Then the gap. Download limits come after.
8. **Requests from Overfin** (built, as Overfin's wishlists, in turns Overfin decides): they're added first; Overtide only sees them in Overfin's scores.
9. **Leaving soon**, told to Overfin before a removal, so people get a last chance to watch.

## Later

- **Degrade, never fail closed.** Overtide must run with any *arr stack and any filesystem: no service, no direct filesystem access and no filesystem type is mandatory. A missing piece loses specific, documented abilities (hardlink bundles, unmanaged files, real disk usage, a torrent's link to its title), never the ones that don't depend on it. A root that can't be read is skipped, not the whole inventory (built); the rest is a standing rule.
- **Other services:** Lidarr, Readarr, Transmission, Deluge, rTorrent.
- **Per-tracker retention:** a hit-and-run window or ratio floor per tracker, on top of the global ratio floor and the Kept list. `Torrent.Tracker` is already known: this is policy on existing data.
- **Freed space checked:** after a removal, confirm the space predicted was actually freed, not only that the plan ran.
- **More filesystems:** reflinks and extents, ZFS, network filesystems, where they can be inspected reliably; elsewhere, fall back to reported sizes rather than fail.
- **A review list for confirm mode:** what confirm mode chose, approved or kept from one place, instead of finding it on the Storage page.
- **Probing services:** detect what an added service supports (API version, features), beyond the connection test.
