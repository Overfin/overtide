<!-- History: what was added (a title's files on one day) and removed, the
     newest first, with the period, what to show, the search and the page in
     the address; ?operation=<id> shows one removal, dry run or not. -->
<script lang="ts">
  import '../styles/library.css';
  import { PageHead, Notice, Loading, Badge } from '@overfin/ui';
  import { get } from '../lib/api';
  import { follow } from '../lib/live.svelte';
  import { route, params, go } from '../lib/router.svelte';
  import Problems from '../parts/removal/Problems.svelte';
  import type { Activity, ActivityPage, ImportedFile, HistoryEvent } from '../lib/types-library';
  import { stamp } from '../lib/types-library';

  let data = $state<ActivityPage | null>(null);
  let problem = $state('');
  let q = $state('');
  // The row open, with its files or its removal's details.
  let open = $state('');
  let files = $state<ImportedFile[] | null>(null);
  let operation = $state<HistoryEvent | null>(null);
  let detailProblem = $state('');
  let shown = $state<HistoryEvent | null>(null);

  const days = $derived(params().get('days') ?? '7');
  const show = $derived(params().get('show') ?? '');
  const operationId = $derived(params().get('operation') ?? '');

  async function load() {
    if (params().get('problems')) return;
    try {
      const query = new URLSearchParams(route.search);
      query.delete('operation');
      data = await get<ActivityPage>(`/api/history?${query}`);
      problem = '';
      if (document.activeElement?.getAttribute('name') !== 'q') q = params().get('q') ?? '';
    } catch (err) {
      problem = (err as Error).message;
    }
  }

  $effect(() => {
    void route.search;
    load();
  });
  $effect(() => follow('stable-list', load));

  // A link to one operation shows it above the table.
  $effect(() => {
    const id = operationId;
    shown = null;
    if (id) get<HistoryEvent>(`/api/history/operations/${encodeURIComponent(id)}`).then((e) => (shown = e), (err) => (problem = (err as Error).message));
  });

  function url(change: Record<string, string | number>): string {
    const values = new URLSearchParams(route.search);
    values.delete('operation');
    for (const [key, value] of Object.entries(change)) {
      const v = String(value);
      if (v === '' || (key === 'days' && v === '7') || (key === 'page' && v === '1')) values.delete(key);
      else values.set(key, v);
    }
    const s = values.toString();
    return s ? `/history?${s}` : '/history';
  }

  function filter(key: string, value: string) {
    go(url({ [key]: value, page: 1 }), true);
  }

  let typing: ReturnType<typeof setTimeout> | undefined;
  function search() {
    clearTimeout(typing);
    typing = setTimeout(() => filter('q', q.trim()), 300);
  }

  const rowKey = (r: Activity) => (r.action === 'added' ? `a/${r.serviceId}/${r.kind}/${r.sourceId}/${r.day}` : `r/${r.operation}`);

  async function toggle(r: Activity) {
    const key = rowKey(r);
    if (open === key) {
      open = '';
      return;
    }
    open = key;
    files = null;
    operation = null;
    detailProblem = '';
    try {
      if (r.action === 'added') {
        const query = new URLSearchParams({ service: r.serviceId!, kind: r.kind, id: String(r.sourceId), day: r.day! });
        files = (await get<{ files: ImportedFile[] }>(`/api/history/added?${query}`)).files;
      } else {
        operation = await get<HistoryEvent>(`/api/history/operations/${r.operation}`);
      }
    } catch (err) {
      detailProblem = (err as Error).message;
    }
  }

  const kinds: Record<string, string> = { movie: 'Film', series: 'Series', media: 'Title', torrent: 'Torrent', unmanaged: 'Files' };
  const origins: Record<string, string> = { adding: 'Overtide', wish: 'Wish' };
  const statuses: Record<string, string> = { success: 'Done', partial: 'Partly done', failed: 'Failed', interrupted: 'Interrupted', attention: 'Needs attention', queued: 'Queued', started: 'Running', dry_run: 'Dry run' };
  const statusKind = (s: string) => (s === 'success' ? 'ok' : s === 'failed' || s === 'interrupted' ? 'err' : s === 'partial' || s === 'attention' ? 'warn' : 'info');
  const plural = (n: number) => (n === 1 ? '' : 's');
  // A title with its year, unless its name already ends with it.
  const named = (r: Activity) => (r.year && !r.title.endsWith(`(${r.year})`) ? `${r.title} (${r.year})` : r.title);
  const about = (r: Activity) => {
    if (r.action === 'removed') return r.kind === 'media' ? '' : kinds[r.kind] ?? r.kind;
    const kind = kinds[r.kind] ?? r.kind;
    if (r.episodes) return `${kind} · ${r.episodes}`;
    return r.files > 1 ? `${kind} · ${r.files} files` : kind;
  };
  const fileName = (f: ImportedFile) => (f.path ? f.path.slice(f.path.lastIndexOf('/') + 1) : f.release ?? '');
</script>

{#snippet details(e: HistoryEvent)}
  <div class="history-files">
    {#if e.error}<div class="error">{e.error}</div>{/if}
    {#if e.files.length}
      <table class="files">
        <tbody>
          {#each e.files as f, i (i)}
            <tr>
              <td class="nowrap muted">{f.owner} · {f.selected ? 'to remove' : 'kept'}</td>
              <td class="mono" title={f.path}>{f.path.slice(f.path.lastIndexOf('/') + 1)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
    {#each e.results as r, i (i)}<div>{r}</div>{/each}
    {#each e.errors as r, i (i)}<div class="error">{r}</div>{/each}
    {#if !e.files.length && !e.results.length && !e.errors.length && !e.error}<div class="muted">Nothing more recorded.</div>{/if}
  </div>
{/snippet}

{#if params().get('problems')}
  <PageHead title="Failed removals" description="Removals that didn't go through in full, with what's still there of them. Once nothing is left, an operation leaves this list on its own." />
  <p><a href="/history">All the history</a></p>
  <Problems />
{:else}
<PageHead title="History" description="What came into the library and what Overtide removed, the newest first." />
{#if problem}<Notice warn>{problem}</Notice>{/if}

{#if shown}
  <div class="card shown-operation">
    <p><b>{shown.label || 'Removal'}</b> · {stamp(shown.createdAt)}{#if shown.dryRun && shown.status !== 'dry_run'}{' · '}<span class="dry">dry run</span>{/if}
      · <Badge kind={statusKind(shown.status)}>{statuses[shown.status] ?? shown.status}</Badge> · {shown.reclaimable}
      · <a href={url({})}>Close</a></p>
    {@render details(shown)}
  </div>
{/if}

{#if !data}
  {#if !problem}<Loading />{/if}
{:else}
  <div class="toolbar">
    <label class="field"><span>Search</span><input type="search" name="q" bind:value={q} oninput={search} placeholder="Title…" /></label>
    <label class="field"><span>Period</span>
      <select value={days} onchange={(e) => filter('days', e.currentTarget.value)}>
        <option value="1">Last 24 hours</option>
        <option value="7">Last 7 days</option>
        <option value="30">Last 30 days</option>
        <option value="90">Last 90 days</option>
        <option value="0">Everything</option>
      </select>
    </label>
    <label class="field"><span>Show</span>
      <select value={show} onchange={(e) => filter('show', e.currentTarget.value)}>
        <option value="">Added and removed</option>
        <option value="added">Added</option>
        <option value="removed">Removed</option>
        <option value="failed">Failed removals</option>
      </select>
    </label>
    <a href="/history">Clear</a>
  </div>

  {#snippet pager()}
    <div class="pager">
      {#if data!.page > 1}<a href={url({ page: data!.page - 1 })}>← Previous</a>{:else}<span class="disabled">← Previous</span>{/if}
      <span>Page {data!.page} of {data!.pages}</span>
      {#if data!.page < data!.pages}<a href={url({ page: data!.page + 1 })}>Next →</a>{:else}<span class="disabled">Next →</span>{/if}
    </div>
  {/snippet}

  <p class="muted"><b>{data.total}</b> row{plural(data.total)}</p>
  {#if data.rows.length}
    {#if data.pages > 1}{@render pager()}{/if}
    <div class="table-wrap">
      <table class="activity">
        <thead><tr><th>When</th><th class="what">What</th><th>Title</th><th class="service">Service</th><th class="num">Size</th><th>Status</th></tr></thead>
        <tbody>
          {#each data.rows as r (rowKey(r))}
            <tr class:opened={open === rowKey(r)}>
              <td class="when">{stamp(r.at)}</td>
              <td class="what">{#if r.action === 'added'}<span class="added">Added</span>{:else}<span class="removed">Removed</span>{/if}</td>
              <td class="title">
                <button type="button" class="link" aria-expanded={open === rowKey(r)} onclick={() => toggle(r)}>{named(r)}</button>
                <span class="action-inline {r.action}">{r.action === 'added' ? 'Added' : 'Removed'}</span>
                <span class="muted">{about(r)}</span>
              </td>
              <td class="service">{r.service ?? ''}</td>
              <td class="num nowrap">{r.size}</td>
              <td>
                {#if r.action === 'removed'}<Badge kind={statusKind(r.status ?? '')}>{statuses[r.status ?? ''] ?? r.status}</Badge>
                {:else if r.origin}<span class="muted">{origins[r.origin] ?? r.origin}</span>{/if}
              </td>
            </tr>
            {#if open === rowKey(r)}
              <tr class="detail">
                <td colspan="6">
                  {#if detailProblem}<span class="error">{detailProblem}</span>
                  {:else if r.action === 'added'}
                    {#if !files}<Loading />
                    {:else}
                      <table class="files">
                        <tbody>
                          {#each files as f, i (i)}
                            <tr>
                              {#if f.episode}<td class="nowrap"><strong>{f.episode}</strong></td>{/if}
                              <td class="mono" title={f.path || f.release}>{fileName(f)}</td>
                              <td class="num nowrap">{f.size}</td>
                              <td class="nowrap muted">{stamp(f.at)}</td>
                            </tr>
                          {/each}
                        </tbody>
                      </table>
                    {/if}
                  {:else if !operation}<Loading />
                  {:else}{@render details(operation)}{/if}
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
    {#if data.pages > 1}{@render pager()}{/if}
  {:else}
    <p class="muted">Nothing in this period.</p>
  {/if}

  {#if data.runs.length && !show && !params().get('q')}
    <h2>Legacy cleanup history</h2>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Completed</th><th>Status</th><th>Storage</th><th>Media</th><th>Torrents</th><th>Reclaimed</th></tr></thead>
        <tbody>
          {#each data.runs as r, i (i)}
            <tr>
              <td class="nowrap">{stamp(r.completedAt)}</td>
              <td>{r.status}</td>
              <td>{r.usageBefore.toFixed(2)}% → {r.usageAfter.toFixed(2)}%</td>
              <td class="num">{r.mediaRemoved}</td>
              <td class="num">{r.torrentsRemoved}</td>
              <td class="num">{r.reclaimed}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
{/if}
{/if}

<style>
  .activity > tbody > tr > td { padding-top: 0.3rem; padding-bottom: 0.3rem; }
  .files td { padding: 0.15rem 0.6rem 0.15rem 0; border: 0; font-size: 0.9em; }
  .files .mono { word-break: break-all; }
  .activity .when { white-space: nowrap; }
  .action-inline { display: none; }
  @media (max-width: 720px) {
    .activity .service, .activity .what { display: none; }
    .action-inline { display: inline; }
    .activity .when { white-space: normal; font-size: 0.85em; }
    .activity th, .activity > tbody > tr > td { padding: 0.45rem 0.5rem; }
  }
  .activity .title .link { color: var(--text); font-weight: 600; padding: 0; text-align: left; white-space: normal; overflow-wrap: anywhere; }
  .activity .title .muted { margin-left: 0.4rem; }
  .added { color: var(--ok); font-weight: 600; }
  .removed { color: var(--danger); font-weight: 600; }
  .activity tr.opened td { border-bottom: 0; }
  .activity tr.detail td { padding-top: 0; }
  .shown-operation { margin-bottom: 1rem; }
</style>
