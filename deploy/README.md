# Overtide beside your *arr stack

This folder runs Overtide with Docker Compose, next to the Radarr, Sonarr
and torrent client you already have, and the Overfin whose values it reads.

## Starting it

```sh
# config/ made by you, not by Docker (which would make it root's)
mkdir -p overtide/config && cd overtide
curl -fsSLO https://raw.githubusercontent.com/overfin/overtide/main/deploy/compose.yaml
curl -fsSL -o .env https://raw.githubusercontent.com/overfin/overtide/main/deploy/.env.example
```

In `.env`, set `MEDIA_DIR` to the folder holding your media and downloads,
and `MEDIA_PATH` to the path Radarr, Sonarr and your torrent client see it
at. Then:

```sh
docker compose up -d
```

Open this machine's address on port 8088: the first screen creates the admin
account. Then, under **Services**, add Radarr, Sonarr, your torrent client,
and Overfin (its control panel address, and a key made there under **Apps**
with **Values** ticked; for adding, also **What came and went**, **Wishes to
fetch**, and **Categories and episode orders**).

Give each service the address it answers on itself. Overtide follows a
redirect only on the same host and port (or from http to https), so its keys
never reach another server: a service behind a sign-in page needs its direct
address.

Overtide starts in dry run: it plans, and records what it would remove, but
removes nothing until you turn dry run off in **Settings**.
Adding is off too until you switch it on in **Settings**.

## Behind a reverse proxy

Signing in is limited per address: a few wrong passwords, and that address
waits before trying again. Behind a reverse proxy, every browser comes from
the proxy's address, so someone guessing would lock you out too. List the
proxy in `config.json`, under `server`, and restart Overtide:

```json
"server": {
  "listen": ":8088",
  "refresh_interval": "30m",
  "trusted_proxies": ["192.0.2.10", "198.51.100.0/24"]
}
```

Each entry is an address or a CIDR range. Only for a connection from one of
them does Overtide read `X-Forwarded-For`, taking the rightmost address in it
that isn't a listed proxy: entries a browser sent itself, at the left, are
never believed. From anyone else, the header is ignored. Empty (the default),
it's never read. List only your own proxies, and have them add the address
they heard from to `X-Forwarded-For` (most do by default). Keep the entries
narrow: a machine inside a listed range that reaches Overtide directly can
write any address in the header, and so get past the sign-in limit.

## Paths

Overtide finds files where Radarr, Sonarr and your torrent client say they
are, so it must see them at the same paths. If your downloads are in another
folder than your library, add a second volume line in `compose.yaml`, mounted
at the same path as in your torrent client. The **Storage** page says when a
path can't be seen.

## Updating

```sh
docker compose pull && docker compose up -d
```

Overtide keeps everything in its config folder (`./config` by default):
back that up.
