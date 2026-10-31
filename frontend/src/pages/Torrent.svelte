<!-- A torrent: where it stands, what removing it frees, its title, its files.
     While it's being removed, the removal shows instead. -->
<script lang="ts">
  import '../styles/torrents.css';
  import { Card, Badge, Notice, Loading } from '@overfin/ui';
  import { get } from '../lib/api';
  import { follow } from '../lib/live.svelte';
  import { route } from '../lib/router.svelte';
  import { openRemoval } from '../lib/removal.svelte';
  import { message } from '../lib/format';
  import { mediaHref } from '../lib/types-torrents';
  import type { TorrentDetailAnswer } from '../lib/types-torrents';
  import OperationState from '../parts/OperationState.svelte';

  let data = $state<TorrentDetailAnswer | null>(null);
  let failure = $state('');

  const hash = $derived(decodeURIComponent(route.path.replace(/^\/torrents\//, '').replace(/\/+$/, '')));

  async function load() {
    try {
      const serviceID = new URLSearchParams(route.search).get('service_id') ?? '';
      data = await get<TorrentDetailAnswer>(`/api/torrents/${encodeURIComponent(hash)}?service_id=${encodeURIComponent(serviceID)}`);
      failure = '';
    } catch (err) {
      failure = message(err);
    }
  }

  $effect(() => {
    void route.search;
    load();
  });
  $effect(() => follow('immediate', load));

  const s = (n: number) => (n !== 1 ? 's' : '');
</script>

{#if failure}
  <Notice warn>{failure}</Notice>
  <p><a href="/torrents">Back to Torrents →</a></p>
{:else if !data}
  <Loading />
{:else if data.operation}
  <OperationState notice={data.operation} back="/torrents" backLabel="Torrents" />
{:else if data.torrent}
  {@const t = data.torrent}
  <div class="page-head title-row">
    <h1>{t.name}</h1>
    <button type="button" class="danger" title="Remove" onclick={() => openRemoval('torrent', { hash: t.hash, service_id: t.serviceId })}>Remove</button>
  </div>
  {#if data.refreshing}<p class="warn">Refreshing…</p>{/if}
  {#if data.lastError}<Notice warn>{data.lastError}</Notice>{/if}
  {#if data.detailUnavailable}<p class="warn">Some torrent details are unavailable.</p>{/if}
  <p><Badge kind="plain">{t.status}</Badge> · {t.client} · {t.state} · {t.progress}</p>

  <Card title="Removal">
    <dl class="kv">
      <dt>Protected</dt>
      <dd>{#if t.protected}{t.protectionReason}{#if t.obligation} <span class="muted">· holds its title back too</span>{/if}{:else}no{/if}</dd>
      {#if t.crossSeeds}
        <dt>Cross-seeds</dt>
        <dd>{t.crossSeeds} torrent{s(t.crossSeeds)} with the same files{#if t.groupKnown} · removing them together frees {t.groupReclaimable}{/if}</dd>
      {/if}
      {#if t.filesMissing}<dt>Files</dt><dd class="warn">gone from disk; it takes no space</dd>{/if}
      <dt>Last activity</dt>
      <dd>{t.lastActivity}</dd>
    </dl>
  </Card>

  {#if t.reclaimableKnown || t.storageError}
    <Card title="Storage">
      <dl class="kv">
        <dt>Reclaimable</dt>
        <dd>{t.reclaimableKnown ? t.reclaimable : 'Unknown'}</dd>
        {#if t.hardlinked}<dt>Hardlinked</dt><dd>{t.hardlinked}</dd>{/if}
        {#if t.storageError}<dt>Status</dt><dd class="error">Storage information unavailable</dd>{/if}
      </dl>
    </Card>
  {/if}

  {#if t.media.length}
    <Card title={`Current media${t.media.length > 1 ? ' items' : ''}`}>
      {#each t.media as m, i (i)}
        <div>{#if m.title}<a href={mediaHref(m)}>{m.title} {#if m.year}({m.year}){/if}</a>{:else}<span>Former {m.type} {m.sourceId} <span class="muted">(no longer managed)</span></span>{/if}</div>
      {/each}
    </Card>
  {:else if t.formerMedia.length}
    <Card title="Historical media relationship">
      {#each t.formerMedia as m, i (i)}
        <div>{#if m.title}<a href={mediaHref(m)}>{m.title} {#if m.year}({m.year}){/if}</a>{:else}<span>Former {m.type} {m.sourceId} <span class="muted">(no longer managed)</span></span>{/if}</div>
      {/each}
      {#if t.supersededByHash}<p class="muted">Superseded by torrent hash <span class="mono">{t.supersededByHash}</span></p>{/if}
    </Card>
  {/if}

  {#if data.filesError}
    <Card title="Files"><p class="warn">Files unavailable: {data.filesError}</p></Card>
  {:else if data.filesRead}
    <Card title={`Files ${data.fileCount ? `(${data.fileCount})` : ''}`}>
      {#each data.files as f, i (i)}
        <div class="file-row">
          <div class="mono" title={f.path}>{f.short}{#if f.owner} <span class="muted">· {f.owner}</span>{/if}</div>
          {#each f.peers as peer, j (j)}
            <div class="mono" title={peer.path}>{peer.short}{#if peer.owner} <span class="muted">· {peer.owner}</span>{/if}</div>
          {/each}
          <span class="muted">{f.size}{#if f.exists}{#if f.identityKnown && f.links > 1} · <strong>hardlinked · {f.links} paths / 1 physical file</strong>{/if}{:else} · missing{/if}</span>
        </div>
      {:else}
        <p class="muted">No files.</p>
      {/each}
      {#if data.fileCount > data.files.length}<p class="muted">Showing first {data.files.length} of {data.fileCount} files.</p>{/if}
      {#if data.removeKnown}<p class="muted">Removing this torrent frees <strong>{data.removeFrees}</strong>.</p>{/if}
    </Card>
  {/if}

  <details class="card">
    <summary>More details</summary>
    <dl class="kv">
      <dt>Hash</dt><dd class="mono">{t.hash}</dd>
      {#if t.tracker}<dt>Tracker</dt><dd class="mono">{t.tracker}</dd>{/if}
      {#if t.category}<dt>Category</dt><dd>{t.category}</dd>{/if}
      {#if t.tags}<dt>Tags</dt><dd>{t.tags}</dd>{/if}
      <dt>Ratio</dt><dd>{t.ratio}</dd>
      <dt>Seeds</dt><dd>{t.seeds}</dd>
      <dt>Leechers</dt><dd>{t.leechers}</dd>
      <dt>Added</dt><dd>{t.added}</dd>
      <dt>Last activity</dt><dd>{t.lastActivity}</dd>
      {#if t.savePath}<dt>Save path</dt><dd class="mono">{t.savePath}</dd>{/if}
      {#if t.contentPath}<dt>Content path</dt><dd class="mono">{t.contentPath}</dd>{/if}
    </dl>
  </details>
{/if}
