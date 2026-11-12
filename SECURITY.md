# Security

This file describes what Overtide does today, as the code stands, not what
it plans to do. A change that makes one of these statements untrue updates
this file in the same commit.

Overtide has one admin account and works for one operator. It holds keys to
Radarr, Sonarr, Prowlarr, qBittorrent and Overfin, and it can delete files
through them: the rules below are about who can make it do that, and what it
deletes on its own.

## Rules

### Who can reach what
1. Overtide listens on plain HTTP (`server.listen`, `:8088` by default). For
   HTTPS, put a reverse proxy in front of it.
2. Open to anyone who reaches the port: the panel's own files (they hold no
   data, and show the sign-in page), `/healthz` (it answers `ok`), and of
   the API only what signing in needs: `/api/session`, `/api/setup`,
   `/api/login`. Every other `/api/` and `/ui/` route needs a valid session.
   Without a database, none of them answers.
3. Until the admin account exists, whoever reaches Overtide first creates
   it. Once it exists, setup is refused; there is no second account and no
   reset from the web: erasing `auth` from `config.json` and restarting
   puts Overtide back in first-run setup.
4. `/events/arr`, where Radarr and Sonarr send their events, is outside the
   sign-in and needs the webhook's secret instead (rule 15).

### Signing in and sessions
5. The password is kept as a bcrypt hash (Go's default cost) in
   `config.json`, at least 8 characters. A login always runs the bcrypt
   check, even when the username is wrong, and answers the same message
   either way, so the time taken doesn't say whether the username is right.
   At most two password checks run at once.
6. Failed logins are limited per address, in memory: four tries free, then
   each try locks that address out for 15 seconds more per try past four, up
   to 5 minutes. A try is counted before its password is checked, so tries
   sent all at once get no more than tries sent one after the other. A
   correct login clears the address's count. The limiter holds at most 1,000
   addresses.
7. The address counted is the connection's. Behind a reverse proxy, list
   the proxy in `server.trusted_proxies` (addresses or CIDR ranges, empty by
   default): only for a connection from one of them is `X-Forwarded-For`
   read, and the address counted is the rightmost entry that isn't a listed
   proxy. Entries further left, which the client can write, are never read,
   unless the client is itself inside a listed range: then it can name any
   address, so the ranges are the operator's to keep to their proxies.
   From any other connection, and with the setting empty, the header is
   ignored. Without the setting, everyone behind a proxy shares one count.
8. A session is a random 32-byte token in the `overtide_session` cookie:
   `HttpOnly`, `SameSite=Strict`, `Path=/`, and `Secure` when the request came
   over HTTPS (`X-Forwarded-Proto: https`, set by the proxy in front). It lasts 30 days. The database keeps only the token's SHA-256,
   so a copy of it holds no cookie that signs in. Every request looks the
   session up; signing out deletes it, and changing the password deletes
   every session but the new one of the browser that changed it.

### Requests from the browser
9. A write (anything but GET, HEAD and OPTIONS) whose `Origin` header names
   another host than the request's is refused. A POST, PUT or PATCH must be
   `application/json`, which a page of another site can't send without the
   browser asking first. JSON bodies are read up to 1 MB, and unknown
   fields are refused.
10. Every answer carries a Content-Security-Policy allowing only Overtide's
    own files (no inline script, no frames around it), `X-Frame-Options:
    DENY`, `X-Content-Type-Options: nosniff` and `Referrer-Policy:
    no-referrer`.
11. Saved API keys and passwords never go back to the browser: the Services
    page shows only whether a service has one. When a service's address is
    changed to another server, its saved key or password is dropped and must
    be given again, so it's never sent to the new address.

### Calls to other services
12. Overtide calls the addresses set under Services, and, before grabbing
    a whole season, the `.torrent` links (http or https only) of up to five
    releases Sonarr's search found, to read their file lists. It follows a
    redirect only to the same host and port (or from http to https on their
    usual ports), never from https to http, and only when the request stays
    the same method; otherwise the call fails and says where it was sent.
13. Every call has a timeout (20 seconds for Radarr and Sonarr, 30 for
    Prowlarr, qBittorrent and Overfin, 40 for a `.torrent`). Answers are
    read up to 512 MB, a `.torrent` file up to 20 MB, error bodies up to
    64 KB.

### Overfin
14. Overtide signs in to Overfin with a key made in Overfin's control panel
    (under Apps), sent as a bearer token, and kept in `config.json`. With
    it, Overtide reads titles' values, what Overfin suggests adding, what
    left the libraries, categories, wishes and episode orders; the one thing
    it writes is what became of each wish. Overfin decides what the key may
    read.

### The Radarr and Sonarr webhook
15. With adding on (not in dry run) and "Everything in Radarr and Sonarr"
    managed, Overtide gives each Radarr and Sonarr a webhook named
    "Overtide", which signs in with the user `overtide` and a random secret
    (32 bytes) as the password, by HTTP Basic authentication. The secret is
    made once and kept in Overtide's database, not in `config.json`, and
    written into Radarr's and Sonarr's Connect settings. An event without
    the right secret (compared in constant time) is refused.
16. What the secret protects: an event can only start the adding run at
    once (a title was added) or confirm that the webhook's address reaches
    Overtide (a test event). Nothing else in the event is read: the run
    itself asks Radarr and Sonarr what they hold, and keeps every limit of a
    scheduled run. An event's body is read up to 1 MB. The arrs send the
    secret the way their webhook is set up: over plain HTTP when the address
    is `http://`.

### What Overtide deletes, and what holds it back
17. Removal is in dry run by default (a `config.json` without the setting
    too): plans are made and recorded in History, and nothing is deleted.
    Dry run is read when a removal is admitted and again when it runs; either
    one in dry run keeps it dry.
18. Library files are deleted through Radarr or Sonarr, torrents through
    qBittorrent (with their data, or without it when another torrent uses
    the same files). Overtide deletes a file itself only when it is an
    unmanaged file (no service claims it, removed by hand only) or a file of
    a removed torrent that no other torrent uses. Just before, it checks the
    path is still the regular file the plan showed (same device and inode)
    and that no service claims it now.
19. Automatic removal has three modes: off (the default), confirm (it
    plans, a human removes) and automatic. It runs only while a disk is over
    its target, only for disks and services it's allowed on, and only with
    Overfin set: before each plan it reads Overfin's scores again, and makes
    no plan when that fails. Without Overfin, plans propose nothing at all.
    A removal queued by automatic mode doesn't run if the mode was turned off
    since.
20. A plan never includes: a title Overfin hasn't scored; what the operator
    kept (the Kept list, or the `overtide-keep` tag, asked of Radarr or Sonarr
    again just before; an app that can't answer keeps its title); what
    adding brought in, for 30 days after its files came (a series season by
    season); a torrent added less than 7 days ago, or with no known date
    added, on its own; a torrent under the minimum ratio or, when set, a
    private tracker's seeding days, nor the title it shares files with.
    Unassociated and incomplete torrents are left out of automatic removal
    unless their settings allow them.
21. Torrents sharing files (cross-seeds, or several torrents on the very
    same path) are removed together or not at all: a file goes only when
    every torrent using it goes. Before deleting, Overtide asks qBittorrent
    again which torrents use the files; one that joined since the plan holds
    the group back, and when qBittorrent can't say, nothing is removed.
    Automatic removal takes such a group only when every torrent in it has
    met its seeding time and ratio.
22. Every removal is rebuilt and checked again just before it runs, and
    recorded in History before anything is deleted.
23. By hand, the operator may remove one title or torrent even without
    Overfin, or before a private tracker's seeding time (with a warning).
24. Adding has its own switch (off by default, dry run, on), apart from
    removal's dry run. On, it deletes in two cases: a download of Overtide's
    titles that hasn't progressed for 48 hours is removed from Radarr's or
    Sonarr's queue with its data and its release blocklisted; and a
    whole-season pack replaces a season's files, only when none of the pack's
    files is of a lower quality, in the series' profile, than the season's
    worst file. When those qualities can't be read, no pack is taken.

### Files on disk
25. `config.json` holds the admin's password hash and the services' API keys
    and passwords, in clear, as Radarr and Sonarr keep theirs. Overtide
    writes it readable by its own user only (0600), on every save (a temporary
    file, made 0600, renamed over it), and creates its folder 0700 when the
    folder is missing.
26. The official image runs as a non-root user on distroless; the Compose
    file runs it read-only, without capabilities, with `no-new-privileges`.

## Reporting a vulnerability

Please report it privately, through this repository's **Security** tab
(*Report a vulnerability*), not in a public issue.
