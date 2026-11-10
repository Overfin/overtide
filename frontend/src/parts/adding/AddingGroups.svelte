<!-- Adding's lists of one page, by state (states): the titles waiting, or
     the seasons and episodes; with a search, and the source filter where
     the titles have sources. data is what the server sent, for the page's
     own buttons; reload, when it changes, reads it again. -->
<script lang="ts">
  import { Badge, Empty, Loading, Notice } from '@overfin/ui';
  import { get } from '../../lib/api';
  import type { Adding } from '../../lib/types-adding';
  import { groups, kindLabels, sourceLabels, short } from '../../lib/words-adding';
  import '../../styles/adding.css';

  let {
    states,
    sourceFilter = false,
    data = $bindable(null),
    reload = 0,
  }: { states: string[]; sourceFilter?: boolean; data?: Adding | null; reload?: number } = $props();

  let failure = $state('');
  let q = $state('');
  let source = $state('');
  let all = $state('');

  async function load() {
    const query = new URLSearchParams();
    if (q.trim()) query.set('q', q.trim());
    if (source) query.set('source', source);
    if (all) query.set('all', all);
    try {
      data = await get<Adding>(`/api/adding?${query}`);
      failure = '';
    } catch (err) {
      failure = (err as Error).message;
    }
  }

  // The list follows the filters, a moment after typing stops.
  $effect(() => {
    void q;
    void source;
    void all;
    void reload;
    const timer = setTimeout(load, 250);
    return () => clearTimeout(timer);
  });

  const shown = $derived(data ? data.groups.filter((g) => states.includes(g.state)) : []);
  const count = $derived(shown.reduce((n, g) => n + g.count, 0));
</script>

{#if failure}<Notice warn>{failure}</Notice>{/if}

{#if !data}
  <Loading />
{:else}
  <div class="toolbar">
    <label class="field"><span>Search</span><input type="search" bind:value={q} placeholder="Title…" /></label>
    {#if sourceFilter && data.total}
      <label class="field"><span>Source</span>
        <select bind:value={source}>
          <option value="">All ({data.total})</option>
          {#each data.sources as s (s.source)}<option value={s.source}>{sourceLabels[s.source] ?? s.source} ({s.count})</option>{/each}
        </select>
      </label>
    {/if}
  </div>

  {#each shown as group (group.state)}
    {@const about = groups[group.state] ?? { label: group.state, hint: '', when: 'When', open: false }}
    <details class="card" open={about.open || all === group.state}>
      <summary>{about.label} <Badge kind="plain">{group.count}</Badge></summary>
      {#if about.hint}<p class="hint">{about.hint}</p>{/if}
      <div class="table-wrap">
        <table class="waiting-table">
          <thead><tr><th>Title</th><th class="hide-narrow">Source</th><th>{about.when}</th><th>Last result</th></tr></thead>
          <tbody>
            {#each group.rows as row, i (i)}
              <tr>
                <td><span class="name">{row.title}</span>{#if row.year} <span class="muted">{row.year}</span>{/if} <span class="muted">· {kindLabels[row.kind]}</span></td>
                <td class="muted hide-narrow">{sourceLabels[row.source] ?? row.source}</td>
                <td class="muted nowrap">{short(row.when)}</td>
                <td class:dry={row.dryRun}>{row.result}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if group.count > group.rows.length}
        <p><button type="button" class="link" onclick={() => (all = group.state)}>Show all {group.count}</button></p>
      {/if}
    </details>
  {:else}
    <Empty>{q.trim() || source ? 'Nothing matches these filters.' : 'Nothing here now.'}</Empty>
  {/each}
  {#if count && shown.length > 1}<p class="muted">{count} in all.</p>{/if}
{/if}
