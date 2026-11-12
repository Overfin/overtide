# Security

This file describes what Overtide does today, as the code stands, not what
it plans to do. A change that makes one of these statements untrue updates
this file in the same commit. Each rule is one promise that can be checked
against the code; what the code does beyond it, on purpose, is listed under
it as an exception.

Overtide has one admin account and works for one operator. It holds keys to
Radarr, Sonarr, Prowlarr, qBittorrent and Overfin, and it can delete files
through them: the rules below are about who can make it do that, and what it
deletes on its own.

Words used below:
- **removal** is what deletes titles and torrents to free disk space: by
  hand from the Storage page, or by automatic removal;
- **a plan** is what removal proposes before deleting anything;
- **adding** is what fetches titles (wishes, suggestions, upgrades).

## Rules

### Who can reach what

1. Overtide listens on plain HTTP (`server.listen`, `:8088` by default). For
   HTTPS, put a reverse proxy in front of it.
2. Open to anyone who reaches the port:
   - the panel's own files (they hold no data, and show the sign-in page);
   - `/healthz` (it answers `ok`);
   - `/api/session`, `/api/setup` and `/api/login`, what signing in needs;
   - `/events/arr`, which needs the webhook's secret instead (rule 17).

   Every other `/api/` and `/ui/` route needs a valid session. Without a
   database, none of them answers.
3. Until the admin account exists, whoever reaches Overtide first creates
   it. Once it exists, setup is refused: there is no second account and no
   reset from the web. Erasing `auth` from `config.json` and restarting puts
   Overtide back in first-run setup, and ends every session of the former
   account.

### Signing in and sessions

4. The password is kept as a bcrypt hash (Go's default cost) in
   `config.json`, at least 8 bytes. A login always runs the bcrypt check,
   even when the username is wrong, and answers the same message either way.
5. At most two password checks run at once: signing in, and changing the
   password.
6. Failed logins are limited per address, in memory: four tries free, then
   each try locks that address out for 15 seconds more per try past four,
   up to 5 minutes. A try is counted before its password is checked, so
   tries sent all at once get no more than tries sent one after the other.
   A correct login clears the address's count. The limiter holds at most
   1,000 addresses. Each IPv6 address counts on its own.
7. The address counted is the connection's. Behind a reverse proxy, list it
   in `server.trusted_proxies` (addresses or CIDR ranges, empty by
   default). From a listed proxy, the address counted is the rightmost
   `X-Forwarded-For` entry that isn't a listed proxy; from anyone else, the
   header is ignored. Without the setting, everyone behind a proxy shares
   one count.
   - Exception: a client inside a listed range can name any address. List
     only your proxies.
8. A session is a random 32-byte token in the `overtide_session` cookie:
   `HttpOnly`, `SameSite=Strict`, `Path=/`, for 30 days. The database keeps
   only the token's SHA-256. Every request looks the session up; signing out
   deletes it, and changing the password deletes every session but the new
   one of the browser that changed it.
9. The cookie is `Secure` when the request came over HTTPS: Overtide's own
   connection, or `X-Forwarded-Proto: https`, believed from anyone (it can
   only make that client's own cookie stricter).

### Requests from the browser

10. A write (anything but GET, HEAD and OPTIONS) whose `Origin` names
    another host than the request's is refused. A POST, PUT or PATCH must be
    `application/json`, which another site's page can't send without the
    browser asking first.
11. The panel's JSON bodies are read up to 1 MB, and unknown fields are
    refused.
    - Exception: Radarr's and Sonarr's events (rule 17) carry many fields
      Overtide doesn't read; it reads only the event's type.
12. Every answer carries a Content-Security-Policy allowing only
    Overtide's own files (no inline script, no frames around it),
    `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff` and
    `Referrer-Policy: no-referrer`.
13. Saved API keys and passwords never go back to the browser: the Services
    page shows only whether a service has one. When a service's address
    changes to another server, its saved key or password is dropped and
    must be given again, so it's never sent to the new address.

### Calls to other services

14. Overtide calls only: the addresses set under Services, and, before
    grabbing a whole season, the `.torrent` links (http or https) of up to
    five releases Sonarr's search found, to read their file lists.
15. It follows a redirect only to the same host and port (or from http to
    https on their usual ports), never from https to http, and only with
    the same method. Every call has a timeout: 20 seconds for Radarr and
    Sonarr, 30 for Prowlarr, qBittorrent and Overfin, 40 for a `.torrent`.
    Answers are read up to 512 MB, a `.torrent` up to 20 MB, error bodies up
    to 64 KB. A failed call says where it was sent.
    - Exception: a failed `.torrent` fetch doesn't name its address, which
      may hold a tracker's key.

### Overfin

16. Overtide reads Overfin with a key made in Overfin's control panel
    (Apps), sent as a bearer token and kept in `config.json`. With it,
    Overtide reads Overfin's scores, what Overfin suggests adding, what left
    the libraries, categories, wishes, episode orders and the server's
    name; the one thing it writes is what became of each wish. Overfin
    decides what the key may read.

### The Radarr and Sonarr webhook

17. With adding on (not in dry run) and "Everything in Radarr and Sonarr"
    managed, each Radarr and Sonarr gets a webhook named "Overtide", which
    signs in by HTTP Basic authentication with the user `overtide` and a
    random 32-byte secret. The secret is made once, kept in Overtide's
    database, and written into Radarr's and Sonarr's Connect settings. An
    event without the right secret (compared in constant time) is refused;
    its body is read up to 1 MB.
18. An event can only start the adding run at once (a title was added) or
    confirm that the webhook reaches Overtide (a test event). Nothing else
    in it is read: the run asks Radarr and Sonarr what they hold, and keeps
    every limit a scheduled run has. The arrs send the secret as their webhook is set up: in clear
    when its address is `http://`.

### What removal deletes, and what holds it back

19. Removal is in dry run by default (a `config.json` without the setting
    too): plans are made and recorded in History, and nothing is deleted.
    Dry run is read when a removal is admitted and again when it runs;
    either one in dry run keeps it dry. Removal's dry run covers removal
    only; adding has its own switch (rule 27).
20. Library files are deleted through Radarr or Sonarr; complete torrents
    through qBittorrent (with their data, or without it when another
    torrent uses the same files); incomplete torrents of a title through
    Radarr's or Sonarr's queue. Overtide deletes a file itself only when
    it's an unmanaged file (no service claims it; by hand only) or a file of
    a removed torrent that no other torrent uses. Just before, it checks
    the path is still the regular file the plan showed (same device and
    inode) and that no service claims it now.
21. Automatic removal has three modes: off (the default), confirm (it
    plans, a human removes) and automatic. It runs only while a disk is over
    its target, only on the disks and services its settings allow,
    and only with Overfin set: before each plan it reads Overfin's scores
    again, and makes no plan when that fails. Without Overfin, plans propose
    nothing. A removal queued by automatic mode doesn't run if the mode was
    turned off since.
22. A plan never includes:
    - a title Overfin hasn't scored;
    - a kept title (the Kept list, or the `overtide-keep` tag, asked of
      Radarr or Sonarr again just before; an app that can't answer keeps
      its title);
    - what adding brought in, for 30 days after its files came (a series
      season by season);
    - on its own, a torrent added less than 7 days ago, or with no known
      date;
    - a torrent under the minimum ratio or, when set, a private tracker's
      seeding days, nor the title it shares files with.
23. Automatic removal also leaves out unassociated and incomplete torrents,
    unless their settings allow them.
    - Exception: a torrent that shares its files with one Overtide knows
      counts as known.
24. In a plan, torrents sharing files (cross-seeds, or several torrents on
    the same path) go together or not at all, and a file goes only when
    every torrent using it goes. Before deleting, Overtide asks qBittorrent
    again which torrents use the files; one that joined since holds the
    group back, and when qBittorrent can't say, nothing is removed.
    Automatic removal takes such a group only when every torrent in it met
    its seeding time and ratio.
    - Exception: by hand, one torrent of a group can be removed alone; its
      shared files stay.
25. Every removal is rebuilt and checked again just before it runs, and
    recorded in History before anything is deleted.
    - Exception: "Forget missing torrents" takes torrents whose files are
      gone out of qBittorrent; it deletes no file and records nothing in
      History.
26. By hand, the operator may remove anything, even without Overfin: kept,
    unscored, just-added titles and torrents, or ones under their ratio. A
    private tracker's seeding time shows a warning.

### What adding deletes

27. Adding has its own switch (off by default, dry run, on), apart from
    removal's dry run. On, it deletes in two cases:
    - a download of one of Overtide's titles that hasn't moved for 48 hours
      is removed from Radarr's or Sonarr's queue with its data, and its
      release blocklisted;
    - a whole-season pack replaces a season's files only when none of the
      pack's files is of a lower quality, in the series' profile, than the
      season's worst file. When those qualities can't be read, no pack is
      taken.
28. Overtide's titles are those adding brought in (tagged `overtide-added`),
    and with "Manage all" on, every title in Radarr and Sonarr not tagged
    `overtide-ignore`.
    - Exception: a stalled download is given up even for a title tagged
      `overtide-keep`.

### Files on disk

29. `config.json` holds the admin's password hash and the services' API
    keys and passwords, in clear, as Radarr and Sonarr keep theirs.
    Overtide writes it readable by its own user only (0600), on every save
    (a temporary file, made 0600, renamed over it), and creates its folder
    0700 when it's missing.
30. Overtide's database (with the webhook's secret) and its logs are its
    user's alone: their folders 0700, their files 0600, set again at every
    start.
    - Exception: a folder another user owns (the operator's choice) is left
      as it is.
31. The official image runs as a non-root user on distroless, unless
    compose's `user:` makes it root; the Compose file runs it read-only,
    without capabilities, with `no-new-privileges`.

## Reporting a vulnerability

Please report it privately, through this repository's **Security** tab
(*Report a vulnerability*), not in a public issue.
