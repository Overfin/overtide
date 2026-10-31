<!-- Torrents: what qBittorrent holds, and which of it is still current.
     Filters, sort and page are in the address. -->
<script lang="ts">
  import '../styles/torrents.css';
  import { PageHead, Card, Badge, Empty, Loading, Notice } from '@overfin/ui';
  import { get } from '../lib/api';
  import { follow } from '../lib/live.svelte';
  import { go, route } from '../lib/router.svelte';
  import { openRemoval } from '../lib/removal.svelte';
  import { message } from '../lib/format';
  import { listURL, mediaHref } from '../lib/types-torrents';
  import type { TorrentList } from '../lib/types-torrents';
  import Pager from '../parts/torrents/Pager.svelte';
  import FilesMissing from '../parts/torrents/FilesMissing.svelte';

  let data = $state<TorrentList | null>(null);
  let failure = $state('');
  let missing = $state(false);
  // The search field, as typed; the address follows a moment later.
  let q = $state(new URLSearchParams(route.search).get('q') ?? '');

  const statusKinds: Record<string, 'ok' | 'warn' | 'err' | 'plain'> = { CURRENT: 'ok', SUPERSEDED: 'warn', ORPHANED: 'err' };
  // Columns whose first click sorts the largest first.
  const descFirst = ['size', 'reclaimable', 'ratio', 'seeds', 'leechers', 'activity'];
  const columns: [string, string][] = [['status', 'Status'], ['name', 'Torrent'], ['media', 'Media'], ['state', 'State'], ['size', 'Size'], ['reclaimable', 'Reclaimable'], ['ratio', 'Ratio'], ['seeds', 'Seeds'], ['leechers', 'Leechers'], ['activity', 'Last activity']];

  async function fetchList() {
    return get<TorrentList>(`/api/torrent-list${route.search}`);
  }

  async function load() {
    try {
      data = await fetchList();
      failure = '';
    } catch (err) {
      failure = message(err);
    }
  }

  // The summary follows every change at once; the list waits for a click
  // on "Updates available", unless it's urgent.
  async function refreshSummary() {
    try {
      const fresh = await fetchList();
      if (!data) {
        data = fresh;
        return;
      }
      const { torrents: _keep, paging: _paging, ...summary } = fresh;
      Object.assign(data, summary);
    } catch {
      // The next change tries again.
    }
  }

  $effect(() => {
    void route.search;
    load();
  });
  $effect(() => {
    const stopSummary = follow('immediate', refreshSummary);
    const stopList = follow('stable-list', load);
    return () => {
      stopSummary();
      stopList();
    };
  });

  const filters = (d: TorrentList, change: Record<string, string> = {}) => {
    const f: Record<string, string> = {
      q: d.q.trim().toLowerCase(),
      status: d.status !== 'any' ? d.status : '',
      reclaimable: d.reclaimable !== 'any' ? d.reclaimable : '',
      activity: d.activity !== 'any' ? d.activity : '',
      ...change,
    };
    for (const k of ['status', 'reclaimable', 'activity']) if (f[k] === 'any') f[k] = '';
    return f;
  };
  const pageURL = (page: number, size: number) => listURL('/torrents', filters(data!), page, size, data!.paging.sort, data!.paging.order);
  function sortURL(key: string) {
    const p = data!.paging;
    let order = descFirst.includes(key) ? 'desc' : 'asc';
    if (p.sort === key) order = p.order === 'asc' ? 'desc' : 'asc';
    return listURL('/torrents', filters(data!), 1, p.pageSize, key, order);
  }
  function filter(change: Record<string, string>) {
    const p = data!.paging;
    go(listURL('/torrents', filters(data!, change), 1, p.pageSize, p.sort, p.order), true);
  }

  // Typing filters a moment after it stops.
  $effect(() => {
    const typed = q;
    if (!data || typed.trim().toLowerCase() === data.q.trim().toLowerCase()) return;
    const timer = setTimeout(() => filter({ q: typed.trim().toLowerCase() }), 300);
    return () => clearTimeout(timer);
  });

  const s = (n: number) => (n !== 1 ? 's' : '');
</script>

<PageHead title="Torrents" description="What qBittorrent holds, and which of it is still current." />

{#if failure}<Notice warn>{failure}</Notice>{/if}
{#if !data}
  {#if !failure}<Loading />{/if}
{:else}
  {#if data.refreshing}<p class="warn">Refreshing…</p>{/if}
  {#if data.lastError}<Notice warn>{data.lastError}</Notice>{/if}

  {#if !data.hasTorrentClient}
    <Card>
      <Empty>
        <p class="muted">No torrent client registered.</p>
        <a class="button primary" href="/services?add=torrentclient">Add torrent client</a>
      </Empty>
    </Card>
  {:else}
    <p class="list-summary">
      <b>{data.allItems}</b> torrents · {data.current} current · {data.superseded} superseded · {data.orphaned} orphaned · {data.unassociated} unassociated
      {#if data.obsoleteKnown > 0}<br /><b>{data.obsoleteReclaimable}</b> reclaimable across {data.obsoleteKnown} torrents{/if}
    </p>

    {#if data.filesMissing}
      <Card title="Files missing" description={`qBittorrent still lists ${data.filesMissing} torrent${s(data.filesMissing)} whose files are gone from disk. They take no space. Removing them from qBittorrent keeps any file.`}>
        {#snippet badge()}<button type="button" onclick={() => (missing = true)}>Review and remove</button>{/snippet}
      </Card>
    {/if}

    <div class="toolbar">
      <label class="field"><span>Search</span><input type="search" bind:value={q} placeholder="Name, hash, tracker, category, media…" /></label>
      <label class="field"><span>Status</span>
        <select value={data.status} onchange={(e) => filter({ status: e.currentTarget.value })}>
          <option value="any">Any</option>
          <option value="current">Current</option>
          <option value="superseded">Superseded</option>
          <option value="orphaned">Orphaned</option>
          <option value="unassociated">Unassociated</option>
        </select>
      </label>
      <label class="field"><span>Reclaimable</span>
        <select value={data.reclaimable} onchange={(e) => filter({ reclaimable: e.currentTarget.value })}>
          <option value="any">Any</option>
          <option value="positive">Greater than 0</option>
          <option value="known">Known</option>
          <option value="unknown">Unknown</option>
        </select>
      </label>
      <label class="field"><span>Activity</span>
        <select value={data.activity} onchange={(e) => filter({ activity: e.currentTarget.value })}>
          <option value="any">Any</option>
          <option value="active">Active</option>
          <option value="inactive">Inactive</option>
        </select>
      </label>
      <a href="/torrents" onclick={() => (q = '')}>Clear</a>
    </div>

    <p class="muted"><b>{data.totalItems}</b>{data.totalItems !== data.allItems ? ' results' : ' torrents'}</p>
    <Pager paging={data.paging} url={pageURL} full />
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            {#each columns as [key, label] (key)}
              <th><a class:on={data.paging.sort === key} href={sortURL(key)}>{label}</a></th>
            {/each}
            <th></th>
          </tr>
        </thead>
        <tbody>
          {#each data.torrents as t (t.serviceId + t.hash)}
            <tr>
              <td><Badge kind={statusKinds[t.status] ?? 'plain'}>{t.status}</Badge></td>
              <td class="breakable name-col"><a href={`/torrents/${t.hash}?service_id=${encodeURIComponent(t.serviceId)}`}><strong>{t.name}</strong></a></td>
              <td class="breakable">
                {#if t.media.length}
                  {#each t.media as m, i (i)}{#if i}<br />{/if}<a href={mediaHref(m)}>{m.title} {#if m.year}({m.year}){/if}</a>{/each}
                {:else if t.formerMedia.length}
                  {#each t.formerMedia as m, i (i)}{#if i}<br />{/if}{#if m.title}<a href={mediaHref(m)}>{m.title} {#if m.year}({m.year}){/if}</a>{:else}Former {m.type} {m.sourceId}{/if} <span class="muted">(historical)</span>{/each}
                {:else}—{/if}
              </td>
              <td>{t.state} · {t.progress}</td>
              <td>{#if t.filesMissing}<span class="warn">files missing</span>{:else}{t.size}{/if}</td>
              <td>
                {#if t.filesMissing}—
                {:else if t.crossSeeds && t.groupKnown}{t.groupReclaimable}<br /><span class="muted">with {t.crossSeeds} cross-seed{s(t.crossSeeds)}</span>
                {:else if t.crossSeeds}{t.reclaimableKnown ? t.reclaimable : '—'}<br /><span class="muted">{t.crossSeeds} cross-seed{s(t.crossSeeds)}</span>
                {:else if t.reclaimableKnown}{t.reclaimable}{:else}—{/if}
              </td>
              <td class="num">{t.ratio}</td>
              <td class="num">{t.seeds}</td>
              <td class="num">{t.leechers}</td>
              <td>{t.lastActivity}</td>
              <td class="row-actions">
                <button type="button" class="small danger" title="Remove" aria-label={`Remove ${t.name}`} onclick={() => openRemoval('torrent', { hash: t.hash, service_id: t.serviceId })}>Remove</button>
              </td>
            </tr>
          {:else}
            <tr><td colspan="11"><Empty>No torrents match these filters.</Empty></td></tr>
          {/each}
        </tbody>
      </table>
    </div>
    <Pager paging={data.paging} url={pageURL} />
  {/if}
{/if}

{#if missing}
  <FilesMissing close={() => { missing = false; load(); }} />
{/if}
