# Overtide beside your *arr stack

This folder runs Overtide with Docker Compose, next to the Radarr, Sonarr
and torrent client you already have, and the Overfin whose values it reads.

## Starting it

```sh
mkdir overtide && cd overtide
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
with **Values** ticked).

Overtide starts in dry run: it plans, and records what it would remove, but
removes nothing until you turn dry run off in **Settings**.

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
