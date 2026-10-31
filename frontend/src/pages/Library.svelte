<!-- Library: the films and series the services manage, with their files;
     filters, sorting and paging in the address. -->
<script lang="ts">
  import '../styles/library.css';
  import { PageHead, Notice, Loading } from '@overfin/ui';
  import { get } from '../lib/api';
  import { follow } from '../lib/live.svelte';
  import { route, go } from '../lib/router.svelte';
  import { openRemoval } from '../lib/removal.svelte';
  import type { LibraryList } from '../lib/types-library';

  let data = $state<LibraryList | null>(null);
  let problem = $state('');
  let q = $state('');

  async function load() {
    try {
      data = await get<LibraryList>(`/api/library${route.search}`);
      problem = '';
      if (document.activeElement?.getAttribute('name') !== 'q') q = data.query;
    } catch (err) {
      problem = (err as Error).message;
    }
  }

  $effect(() => {
    void route.search;
    load();
  });
  $effect(() => follow('stable-list', load));

  // url is this page with the filters, sorting and paging given.
  function url(change: Record<string, string | number | boolean | undefined>): string {
    const d = data!;
    const values: Record<string, string> = { page: String(d.page), page_size: String(d.pageSize), sort: d.sort, order: d.order };
    if (d.query.trim()) values.q = d.query.trim().toLowerCase();
    if (d.type !== 'any') values.type = d.type;
    if (d.source && d.source !== 'any') values.source = d.source;
    if (d.torrent !== 'any') values.torrent = d.torrent;
    if (d.showNoFiles) values.show_no_files = '1';
    for (const [key, value] of Object.entries(change)) {
      if (value === undefined || value === '' || value === 'any' || value === false) delete values[key];
      else values[key] = value === true ? '1' : String(value);
    }
    return `/library?${new URLSearchParams(values)}`;
  }

  // A filter changed: back to the first page, the sort and size kept.
  function filter(key: string, value: string | boolean) {
    go(url({ [key]: value, page: 1 }), true);
  }

  let typing: ReturnType<typeof setTimeout> | undefined;
  function search() {
    clearTimeout(typing);
    typing = setTimeout(() => filter('q', q.trim()), 300);
  }

  const defaultOrder = (key: string) => (key === 'value' || key === 'title' || key === 'type' ? 'asc' : 'desc');
  function sortURL(key: string): string {
    const d = data!;
    const order = d.sort === key ? (d.order === 'asc' ? 'desc' : 'asc') : defaultOrder(key);
    return url({ page: 1, sort: key, order });
  }
  const pages = $derived(data ? Array.from({ length: Math.min(data.totalPages, data.page + 2) - Math.max(1, data.page - 2) + 1 }, (_, i) => Math.max(1, data!.page - 2) + i) : []);
</script>

<PageHead title="Library" description="The films and series your services manage, with their files." />
{#if data?.refreshing}<Notice warn>Refresh in progress…</Notice>{/if}
{#if data?.error}<Notice warn>{data.error}</Notice>{/if}
{#if problem}<Notice warn>{problem}</Notice>{/if}

{#if !data}
  {#if !problem}<Loading />{/if}
{:else if !data.hasMediaLibrary}
  <div class="card empty">
    <p class="muted">No media library registered.</p>
    <button type="button" class="primary" onclick={() => go('/services?add=medialibrary')}>Add media library</button>
  </div>
{:else}
  <div class="toolbar">
    <label class="field"><span>Search</span><input type="search" name="q" bind:value={q} oninput={search} placeholder="Title, path, tag…" /></label>
    {#if data.typeOptions.length > 1}
      <label class="field"><span>Type</span>
        <select value={data.type} onchange={(e) => filter('type', e.currentTarget.value)}>
          <option value="any">Any</option>
          {#each data.typeOptions as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
        </select>
      </label>
    {/if}
    {#if data.sourceOptions?.length}
      <label class="field"><span>Source</span>
        <select value={data.source} onchange={(e) => filter('source', e.currentTarget.value)}>
          <option value="any">Any</option>
          {#each data.sourceOptions as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
        </select>
      </label>
    {/if}
    {#if data.hasTorrentClient}
      <label class="field"><span>Torrent</span>
        <select value={data.torrent} onchange={(e) => filter('torrent', e.currentTarget.value)}>
          <option value="any">Any</option>
          <option value="yes">Current</option>
          <option value="no">None</option>
        </select>
      </label>
    {/if}
    <label class="inline"><input type="checkbox" checked={data.showNoFiles} onchange={(e) => filter('show_no_files', e.currentTarget.checked)} /> Show media with no files</label>
    <a href="/library">Clear</a>
  </div>

  <p class="muted"><b>{data.total}</b>{#if data.total !== data.all}{' of '}{data.all}{/if} media</p>

  {#snippet pager(withSizes: boolean)}
    <div class="pager">
      {#if data!.page > 1}<a href={url({ page: data!.page - 1 })}>← Previous</a>{:else}<span class="disabled">← Previous</span>{/if}
      <span>Page {data!.page} of {data!.totalPages}</span>
      {#if withSizes}
        {#each pages as p (p)}{#if p === data!.page}<span class="current">{p}</span>{:else}<a href={url({ page: p })}>{p}</a>{/if}{/each}
      {/if}
      {#if data!.page < data!.totalPages}<a href={url({ page: data!.page + 1 })}>Next →</a>{:else}<span class="disabled">Next →</span>{/if}
      {#if withSizes}
        <div class="pagesize">Per page: {#each [25, 50, 100, 250] as size (size)}<a class:active={size === data!.pageSize} href={url({ page: 1, page_size: size })}>{size}</a>{/each}</div>
      {/if}
    </div>
  {/snippet}

  {@render pager(true)}
  <div class="table-wrap">
    <table>
      <thead>
        <tr>
          <th><a href={sortURL('title')}>Media</a></th>
          <th><a href={sortURL('value')}>Overfin value</a></th>
          <th><a href={sortURL('type')}>Type</a></th>
          <th><a href={sortURL('size')}>Size</a></th>
          <th><a href={sortURL('torrents')}>Torrents</a></th>
          <th><span class="visually-hidden">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        {#each data.rows as m (`${m.serviceId}/${m.type}/${m.sourceId}`)}
          <tr>
            <td><a href={`/library/${m.type}/${m.sourceId}?service_id=${encodeURIComponent(m.serviceId)}`}>{m.title} {#if m.year}({m.year}){/if}</a></td>
            <td class="num">{m.value.toFixed(1)}</td>
            <td>{m.type}</td>
            <td class="num">{m.size}</td>
            <td class="num">{m.torrents}</td>
            <td class="row-actions">
              {#if m.removable}
                <button type="button" class="small danger" title="Remove" aria-label={`Remove ${m.title}`}
                  onclick={() => openRemoval('media', { type: m.type, id: String(m.sourceId), service_id: m.serviceId })}>Remove</button>
              {:else}
                <button type="button" class="small" disabled title="No files to remove">Remove</button>
              {/if}
            </td>
          </tr>
        {:else}
          <tr><td colspan="6" class="muted">No media match these filters.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
  {@render pager(false)}
{/if}
