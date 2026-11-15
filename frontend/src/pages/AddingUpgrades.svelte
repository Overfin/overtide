<!-- Adding, upgrades: what waits for a search of a better copy, in the
     order they're searched, each removable for good. Search, sort and page
     are in the address, as in the other lists. -->
<script lang="ts">
  import { Busy, Empty, Loading, Notice, PageHead, ask, toast } from '@overfin/ui';
  import { get, post } from '../lib/api';
  import { go, route } from '../lib/router.svelte';
  import { short } from '../lib/words-adding';
  import { listURL } from '../lib/types-torrents';
  import type { Paging } from '../lib/types-torrents';
  import Pager from '../parts/torrents/Pager.svelte';
  import '../styles/adding.css';

  interface UpgradeRow {
    id: string;
    title: string;
    kind: 'film' | 'series';
    why: string;
    value?: number;
    lastSearched?: string;
    next?: string;
  }
  interface Upgrades {
    rows: UpgradeRow[];
    folders: { root: string; perDay: number; today: number }[];
    allItems: number;
    totalItems: number;
    q: string;
    paging: Paging;
  }

  let data = $state<Upgrades | null>(null);
  let failure = $state('');
  let q = $state(new URLSearchParams(route.search).get('q') ?? '');

  async function load() {
    try {
      data = await get<Upgrades>(`/api/adding/upgrades${route.search}`);
      failure = '';
    } catch (err) {
      failure = (err as Error).message;
    }
  }
  $effect(() => {
    void route.search;
    load();
  });

  const filters = (d: Upgrades, change: Record<string, string> = {}) => ({ q: d.q.trim().toLowerCase(), ...change });
  const pageURL = (page: number, size: number) => listURL('/adding/upgrades', filters(data!), page, size, data!.paging.sort, data!.paging.order);
  // A column sorts by it; the value starts with the most valued.
  function sortURL(key: string) {
    const p = data!.paging;
    let order = key === 'value' ? 'desc' : 'asc';
    if (p.sort === key) order = p.order === 'asc' ? 'desc' : 'asc';
    return listURL('/adding/upgrades', filters(data!), 1, p.pageSize, key, order);
  }

  // Typing filters a moment after it stops.
  $effect(() => {
    const typed = q.trim().toLowerCase();
    if (!data || typed === data.q.trim().toLowerCase()) return;
    const timer = setTimeout(() => go(listURL('/adding/upgrades', { q: typed }, 1, data!.paging.pageSize, data!.paging.sort, data!.paging.order), true), 300);
    return () => clearTimeout(timer);
  });

  async function remove(row: UpgradeRow) {
    const sure = await ask(`Overtide never searches for a better copy of ${row.title}. Its files stay as they are.`, {
      title: `No upgrade for ${row.title}?`,
      yes: 'Remove',
      danger: true,
    });
    if (!sure) return;
    await post('/api/adding/remove', { kind: 'upgrade', id: row.id });
    toast(`${row.title} is out of the upgrades.`);
    await load();
  }

  const columns: [string, string, string][] = [['title', 'Title', ''], ['value', 'Value', 'hide-narrow'], ['last', 'Last search', ''], ['next', 'Next search', '']];
</script>

<PageHead title="Upgrades" description="Films and seasons searched for a better copy, in the order they're searched: seasons that came without their whole-season pack first, then what's below its quality cutoff, the most valued first. Each one at most once in 30 days." />

{#if failure}<Notice warn>{failure}</Notice>{/if}

{#if !data}
  {#if !failure}<Loading />{/if}
{:else}
  {#if !data.folders.some((f) => f.perDay > 0)}
    <Notice>Upgrades are off: set how many a day for each root folder in Storage, in its disk's settings.</Notice>
  {:else}
    {#each data.folders as folder (folder.root)}
      <p class="muted">
        {folder.root}: {folder.perDay === 0 ? 'no upgrades' : `${folder.today} of ${folder.perDay} upgrade searches used in the last 24 hours`}.
      </p>
    {/each}
  {/if}
  <div class="toolbar">
    <label class="field"><span>Search</span><input type="search" bind:value={q} placeholder="Title…" /></label>
    {#if data.paging.sort !== 'order'}<a href={listURL('/adding/upgrades', filters(data), 1, data.paging.pageSize, 'order', 'asc')}>Search order</a>{/if}
    {#if data.q}<a href="/adding/upgrades" onclick={() => (q = '')}>Clear</a>{/if}
  </div>
  {#if data.rows.length}
    <p class="muted"><b>{data.totalItems}</b>{#if data.totalItems !== data.allItems} of {data.allItems}{/if} waiting</p>
    <Pager paging={data.paging} url={pageURL} full />
    <div class="table-wrap">
      <table class="waiting-table">
        <thead>
          <tr>
            {#each columns as [key, label, cls] (key)}
              <th class={cls}><a class:on={data.paging.sort === key} href={sortURL(key)}>{label}</a></th>
            {/each}
            <th class="hide-narrow">Why</th>
            <th><span class="visually-hidden">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {#each data.rows as row (row.id)}
            <tr>
              <td><span class="name">{row.title}</span> <span class="muted">· {row.kind === 'film' ? 'Film' : 'Season'}</span></td>
              <td class="muted nowrap hide-narrow">{row.value ? row.value.toFixed(1) : '—'}</td>
              <td class="muted nowrap">{row.lastSearched ? short(row.lastSearched) : 'Never'}</td>
              <td class="muted nowrap">{row.next ? short(row.next) : 'A run with room'}</td>
              <td class="muted hide-narrow">{row.why}</td>
              <td class="actions"><Busy kind="danger" small action={() => remove(row)}>Remove</Busy></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <Pager paging={data.paging} url={pageURL} />
  {:else}
    <Empty>{data.q ? 'Nothing matches.' : 'Nothing waits for an upgrade.'}</Empty>
  {/if}
{/if}
