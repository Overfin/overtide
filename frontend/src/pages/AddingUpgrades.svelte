<!-- Adding, upgrades: what waits for a search of a better copy, in the
     order they're searched, each removable for good. -->
<script lang="ts">
  import { Busy, Empty, Loading, Notice, PageHead, ask, toast } from '@overfin/ui';
  import { get, post } from '../lib/api';
  import { short } from '../lib/words-adding';
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
    perDay: number;
    today: number;
  }

  let data = $state<Upgrades | null>(null);
  let failure = $state('');
  let q = $state('');

  async function load() {
    try {
      data = await get<Upgrades>('/api/adding/upgrades');
      failure = '';
    } catch (err) {
      failure = (err as Error).message;
    }
  }
  $effect(() => {
    load();
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

  const shown = $derived(data ? data.rows.filter((r) => r.title.toLowerCase().includes(q.trim().toLowerCase())) : []);
</script>

<PageHead title="Upgrades" description="Films and seasons searched for a better copy, in the order they're searched: seasons that came without their whole-season pack first, then what's below its quality cutoff, the most valued first. Each one at most once in 30 days." />

{#if failure}<Notice warn>{failure}</Notice>{/if}

{#if !data}
  <Loading />
{:else}
  {#if data.perDay === 0}
    <Notice>Upgrades are off: set how many a day in Settings, under Adding.</Notice>
  {:else}
    <p class="muted">{data.today} of {data.perDay} upgrade searches used in the last 24 hours.</p>
  {/if}
  <div class="toolbar">
    <label class="field"><span>Search</span><input type="search" bind:value={q} placeholder="Title…" /></label>
  </div>
  {#if shown.length}
    <details class="card" open>
      <summary>Upgrades <span class="badge plain">{shown.length}</span></summary>
      <div class="table-wrap">
        <table class="waiting-table">
          <thead><tr><th>Title</th><th class="hide-narrow">Why</th><th>Last search</th><th>Next search</th><th><span class="visually-hidden">Actions</span></th></tr></thead>
          <tbody>
            {#each shown as row (row.id)}
              <tr>
                <td><span class="name">{row.title}</span> <span class="muted">· {row.kind === 'film' ? 'Film' : 'Season'}</span></td>
                <td class="muted hide-narrow">{row.why}</td>
                <td class="muted nowrap">{row.lastSearched ? short(row.lastSearched) : 'Never'}</td>
                <td class="muted nowrap">{row.next ? short(row.next) : 'A run with room'}</td>
                <td class="actions"><Busy kind="danger" small action={() => remove(row)}>Remove</Busy></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </details>
  {:else}
    <Empty>{q.trim() ? 'Nothing matches.' : 'Nothing waits for an upgrade.'}</Empty>
  {/if}
{/if}
