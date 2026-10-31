<!-- Unmanaged files: paths no current service claims. Removing one deletes
     it from disk, every hardlinked path of it together. Filters, sort and
     page are in the address. -->
<script lang="ts">
  import '../styles/torrents.css';
  import { PageHead, Empty, Loading, Notice } from '@overfin/ui';
  import { get } from '../lib/api';
  import { follow } from '../lib/live.svelte';
  import { go, route } from '../lib/router.svelte';
  import { openRemoval } from '../lib/removal.svelte';
  import { message } from '../lib/format';
  import { listURL } from '../lib/types-torrents';
  import type { UnmanagedList, UnmanagedRow } from '../lib/types-torrents';
  import Pager from '../parts/torrents/Pager.svelte';

  let data = $state<UnmanagedList | null>(null);
  let failure = $state('');
  // The rows ticked, by their place on this page.
  let picked = $state<number[]>([]);
  const start = new URLSearchParams(route.search);
  let q = $state(start.get('q') ?? '');
  let minSize = $state(start.get('min_size_mib') ?? '');

  const columns: [string, string][] = [['path', 'Path'], ['size', 'Size'], ['reclaimable', 'Physical potential'], ['links', 'Links'], ['modified', 'Modified']];

  async function fetchList() {
    return get<UnmanagedList>(`/api/unmanaged-files${route.search}`);
  }

  async function load() {
    try {
      data = await fetchList();
      picked = [];
      failure = '';
    } catch (err) {
      failure = message(err);
    }
  }

  // The totals follow every change at once; the list waits for a click on
  // "Updates available", unless it's urgent.
  async function refreshSummary() {
    try {
      const fresh = await fetchList();
      if (!data) {
        data = fresh;
        return;
      }
      data.scanError = fresh.scanError;
      data.allItems = fresh.allItems;
      data.totalBytes = fresh.totalBytes;
      data.reclaimable = fresh.reclaimable;
      data.shared = fresh.shared;
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

  const filters = (d: UnmanagedList, change: Record<string, string> = {}) => ({
    q: d.q.trim().toLowerCase(),
    status: d.status !== 'any' ? d.status : '',
    root: d.root,
    min_size_mib: d.minSizeMib,
    ...change,
  });
  const pageURL = (page: number, size: number) => listURL('/unmanaged', filters(data!), page, size, data!.paging.sort, data!.paging.order);
  function sortURL(key: string) {
    const p = data!.paging;
    let order = key === 'path' ? 'asc' : 'desc';
    if (p.sort === key) order = p.order === 'asc' ? 'desc' : 'asc';
    return listURL('/unmanaged', filters(data!), 1, p.pageSize, key, order);
  }
  function filter(change: Record<string, string>) {
    const p = data!.paging;
    const f = filters(data!, change);
    if (f.status === 'any') f.status = '';
    go(listURL('/unmanaged', f, 1, p.pageSize, p.sort, p.order), true);
  }

  // Typing filters a moment after it stops.
  $effect(() => {
    const typed = q.trim().toLowerCase();
    const size = minSize.trim();
    if (!data || (typed === data.q.trim().toLowerCase() && size === data.minSizeMib)) return;
    const timer = setTimeout(() => filter({ q: typed, min_size_mib: size }), 300);
    return () => clearTimeout(timer);
  });

  const filtered = $derived(!!data && (!!data.q || data.status !== 'any' || !!data.root || !!data.minSizeMib));
  const allPicked = $derived(!!data && data.files.length > 0 && picked.length === data.files.length);
  const pathsOf = (rows: UnmanagedRow[]) => rows.flatMap((row) => row.paths.map((p) => p.path));

  function pickAll(on: boolean) {
    picked = on ? data!.files.map((_, i) => i) : [];
  }

  function removeSelected() {
    openRemoval('unmanaged', { path: pathsOf(picked.map((i) => data!.files[i])) });
  }

  function clear() {
    q = '';
    minSize = '';
  }
</script>

<PageHead title="Unmanaged files" description="No current service claims these paths. Removing one deletes it directly from disk — this is not delegated to any service." />

{#if failure}<Notice warn>{failure}</Notice>{/if}
{#if !data}
  {#if !failure}<Loading />{/if}
{:else}
  {#if data.scanError}<p class="warn">Scan unavailable: {data.scanError}</p>{/if}
  <p class="list-summary">
    <b>{data.allItems}</b> files · <b>{data.totalBytes}</b>{#if data.reclaimable} · <b>{data.reclaimable}</b> physically reclaimable if an authorized owner removes every link{/if}{#if data.shared} · {data.shared} shared through hardlinks{/if}
  </p>

  <div class="toolbar">
    <label class="field"><span>Search</span><input type="search" bind:value={q} placeholder="Path or filename…" /></label>
    <label class="field"><span>Status</span>
      <select value={data.status} onchange={(e) => filter({ status: e.currentTarget.value })}>
        <option value="any">Any</option>
        <option value="reclaimable">Reclaimable</option>
        <option value="shared">Shared (hardlinked elsewhere)</option>
        <option value="unknown">Unknown</option>
      </select>
    </label>
    {#if data.rootOptions.length}
      <label class="field"><span>Root</span>
        <select value={data.root} onchange={(e) => filter({ root: e.currentTarget.value })}>
          <option value="">Any</option>
          {#each data.rootOptions as o (o.key)}<option value={o.key}>{o.label}</option>{/each}
        </select>
      </label>
    {/if}
    <label class="field"><span>Min size (MiB)</span><input type="number" min="0" step="any" bind:value={minSize} placeholder="0" /></label>
    {#if filtered}<a href="/unmanaged" onclick={clear}>Clear</a>{/if}
  </div>

  <p class="muted"><b>{data.totalItems}</b>{#if data.totalItems !== data.allItems} of {data.allItems}{/if} files</p>
  <Pager paging={data.paging} url={pageURL} full />
  <div class="table-wrap">
    <table>
      <thead>
        <tr>
          <th><input type="checkbox" aria-label="Select all files" checked={allPicked} disabled={!data.files.length} onchange={(e) => pickAll(e.currentTarget.checked)} /></th>
          {#each columns as [key, label] (key)}
            <th><a class:on={data.paging.sort === key} href={sortURL(key)}>{label}</a></th>
          {/each}
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each data.files as f, i (f.paths[0]?.path ?? i)}
          <tr class:selected={picked.includes(i)}>
            <td><input type="checkbox" value={i} bind:group={picked} aria-label={`Select ${f.paths[0]?.short ?? ''}`} /></td>
            <td>{#each f.paths as p (p.path)}<div class="mono" title={p.path}>{p.short}</div>{/each}</td>
            <td class="nowrap">{f.size}</td>
            <td class="nowrap">{f.reclaimableKnown ? f.reclaimable : '—'}</td>
            <td>
              {#if f.paths.length > 1}hardlinked · {f.paths.length} paths to the same physical file{:else if f.links > 1}hardlinked{:else}—{/if}{#if f.missingLinks > 0} · {f.missingLinks} hardlink{f.missingLinks > 1 ? 's' : ''} outside known topology{/if}
            </td>
            <td class="nowrap">{f.modified}</td>
            <td class="row-actions">
              <button type="button" class="small danger" title="Remove" aria-label={`Remove ${f.paths[0]?.short ?? ''}`} onclick={() => openRemoval('unmanaged', { path: pathsOf([f]) })}>Remove</button>
            </td>
          </tr>
        {:else}
          <tr><td colspan="7"><Empty>No unmanaged files found.</Empty></td></tr>
        {/each}
      </tbody>
    </table>
  </div>
  <div class="selection-bar">
    <button type="button" class="danger" disabled={!picked.length} onclick={removeSelected}>Remove selected</button>
  </div>
  <Pager paging={data.paging} url={pageURL} />
{/if}
