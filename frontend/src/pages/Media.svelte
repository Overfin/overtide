<!-- A title (/library/<type>/<id>?service_id=…, or /media/…): its value
     and why, its files, its torrents; keeping it, removing it. -->
<script lang="ts">
  import '../styles/library.css';
  import { Card, Badge, Busy, Notice, Loading } from '@overfin/ui';
  import { get, post } from '../lib/api';
  import { follow } from '../lib/live.svelte';
  import { route } from '../lib/router.svelte';
  import { openRemoval } from '../lib/removal.svelte';
  import type { MediaDetail, Operation, TorrentRow } from '../lib/types-library';
  import { day } from '../lib/types-library';
  import OperationState from '../parts/OperationState.svelte';

  let data = $state<MediaDetail | null>(null);
  let operation = $state<Operation | null>(null);
  let problem = $state('');

  // /library/movie/12 and /media/movie/12 are the same title.
  const parts = $derived(route.path.replace(/^\/(library|media)\//, '').replace(/\/+$/, '').split('/'));

  async function load() {
    if (parts.length !== 2) {
      problem = 'No such title.';
      return;
    }
    const service = new URLSearchParams(route.search).get('service_id') || '';
    try {
      const answer = await get<MediaDetail | { operation: Operation }>(
        `/api/library/${encodeURIComponent(parts[0])}/${encodeURIComponent(parts[1])}${service ? `?service_id=${encodeURIComponent(service)}` : ''}`);
      if ('operation' in answer) {
        operation = answer.operation;
        data = null;
      } else {
        data = answer;
        operation = null;
      }
      problem = '';
    } catch (err) {
      problem = (err as Error).message;
    }
  }

  $effect(() => {
    void route.search;
    load();
  });
  $effect(() => follow('immediate', load));

  async function keep() {
    const d = data!;
    await post('/api/storage/keep', { key: d.keptKey, name: d.keepName, unkeep: d.kept });
    await load();
  }

  function remove() {
    const m = data!.media;
    openRemoval('media', { type: m.type, id: String(m.sourceId), service_id: m.serviceId });
  }

  const noTorrents = $derived(!!data && !data.current.length && !data.superseded.length && !data.orphaned.length && !data.unassociated.length);
</script>

{#if problem}<Notice warn>{problem}</Notice>{/if}

{#if operation}
  <OperationState notice={operation} back="/library" backLabel="Library" />
{:else if !data}
  {#if !problem}<Loading />{/if}
{:else}
  {@const m = data.media}
  {#if data.refreshing}<Notice warn>Refreshing…</Notice>{/if}
  {#if data.error}<Notice warn>{data.error}</Notice>{/if}

  <div class="title-head">
    <div>
      <h1>{m.title} {#if m.year}({m.year}){/if}</h1>
      <div class="muted">{m.type}</div>
    </div>
    <div>
      <div class="value">{m.value.toFixed(1)}</div>
      <div class="muted">Overfin value</div>
    </div>
    <div class="actions">
      {#if data.kept}
        <Busy kind="ghost" title="Kept: never planned for removal" action={keep}>Unkeep</Busy>
      {:else}
        <Busy kind="ghost" title="Never plan it for removal" action={keep}>Keep</Busy>
      {/if}
      {#if data.removable}
        <button type="button" class="danger" title="Remove" aria-label={`Remove ${m.title}`} onclick={remove}>Remove</button>
      {:else}
        <button type="button" title="Nothing to remove" disabled>Remove</button>
      {/if}
    </div>
  </div>

  <div class="cards">
    <Card title="Value">
      <ul class="list reasons">
        {#each m.reasons as r, i (i)}
          <li>
            <strong>{r.label}</strong><br /><span class="muted">{r.value}</span>
            {#if r.note}<br /><small class="hint">{r.note}</small>{/if}
          </li>
        {/each}
      </ul>
    </Card>
    <Card title="Media">
      <dl class="facts">
        <dt>Service</dt><dd>{#if m.serviceName}{m.serviceName}{:else}<span class="muted">Unknown</span>{/if}</dd>
        <dt>Size</dt><dd>{m.size}</dd>
        <dt>Path</dt><dd class="mono">{m.path}</dd>
        <dt>Added</dt><dd>{m.addedAt ? day(m.addedAt) : 'Unknown'}</dd>
      </dl>
    </Card>
    <Card title="IDs">
      <dl class="facts">
        <dt>TMDB</dt><dd>{m.tmdbId || '—'}</dd>
        <dt>TVDB</dt><dd>{m.tvdbId || '—'}</dd>
        <dt>IMDb</dt><dd>{m.imdbId || '—'}</dd>
      </dl>
    </Card>
  </div>

  <Card title="Files">
    {#snippet badge()}<Badge kind="plain">{data!.fileCount}</Badge>{/snippet}
    {#if data.filesError}
      <p class="dry">Files unavailable: {data.filesError}</p>
    {:else if !data.filesKnown}
      <p class="muted">No file information yet.</p>
    {:else if !data.files.length}
      <p class="muted">No files.</p>
    {:else}
      {#each data.files as f, i (i)}
        <div class="file-row">
          <div class="mono" title={f.path}>{f.short}{#if f.owner} <span class="muted owner">· {f.owner}</span>{/if}</div>
          {#each f.sharedWith || [] as s, j (j)}
            <div class="mono" title={s.path}>{s.short}{#if s.owner} <span class="muted owner">· {s.owner}</span>{/if}</div>
          {/each}
          <div class="hint">
            {f.size}{#if f.exists}{#if f.identityKnown && (f.links || 0) > 1} · <strong>hardlinked · {f.links} paths / 1 physical file</strong>{/if}{:else} · missing{/if}
          </div>
        </div>
      {/each}
      {#if data.fileCount > data.files.length}<p class="muted">Showing first {data.files.length} of {data.fileCount} files.</p>{/if}
      <p class="hint spaced">
        {#if data.removeMedia}Removing media frees <strong>{data.removeMedia}</strong>{/if}{#if data.removeWithCurrent} · Removing media and current torrents frees <strong>{data.removeWithCurrent}</strong>{/if}
      </p>
    {/if}
  </Card>

  {#snippet group(title: string, rows: TorrentRow[])}
    {#if rows.length}
      <h3 class="group-title">{title} <span class="count">({rows.length})</span></h3>
      <ul class="list">
        {#each rows as t (t.hash)}
          <li>
            <div>
              <div class="name">{t.name}</div>
              <div class="hint">{t.size} · ratio {t.ratio.toFixed(2)} · {t.seeds} seeds · {t.leechers} leechers{#if t.reclaimable} · {t.reclaimable} reclaimable{/if}</div>
            </div>
            <a href={`/torrents/${t.hash}`}>View →</a>
          </li>
        {/each}
      </ul>
    {/if}
  {/snippet}

  <Card title="Torrents">
    {@render group('Current', data.current)}
    {@render group('Superseded', data.superseded)}
    {@render group('Orphaned', data.orphaned)}
    {@render group('Unassociated', data.unassociated)}
    {#if noTorrents}<p class="muted">No torrents.</p>{/if}
  </Card>
{/if}
