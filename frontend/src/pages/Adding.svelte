<!-- Adding: the titles Overfin suggests and their state, and the live
     panel. -->
<script lang="ts">
  import { PageHead, Notice, Badge, Busy, Empty, Loading, ask, toast } from '@overfin/ui';
  import { get, post } from '../lib/api';
  import type { Adding, WaitingImport } from '../lib/types-adding';
  import { groups, kindLabels, sourceLabels, short, clock } from '../lib/words-adding';
  import NowPanel from '../parts/adding/NowPanel.svelte';
  import ImportForm from '../parts/adding/ImportForm.svelte';

  let data = $state<Adding | null>(null);
  let failure = $state('');
  let q = $state('');
  let source = $state('');
  let all = $state('');
  let waiting = $state<WaitingImport[]>([]);
  let choosing = $state<WaitingImport | null>(null);

  const fileName = (path: string) => path.slice(path.lastIndexOf('/') + 1);

  async function loadWaiting() {
    try {
      waiting = (await get<{ imports: WaitingImport[] }>('/api/adding/imports')).imports;
    } catch (err) {
      failure = (err as Error).message;
    }
  }

  async function another(item: WaitingImport) {
    await post(`/api/adding/imports/${encodeURIComponent(item.serviceId)}/${encodeURIComponent(item.downloadId)}`, { action: 'another' });
    toast('Another release will be tried: the next run looks for one.');
    setTimeout(loadWaiting, 3000);
  }

  async function ignore(item: WaitingImport) {
    const sure = await ask(
      `Stop trying for this release of ${item.series}? Its waiting files stay in the download client, and aren't imported. Nothing else is touched.`,
      { title: 'Ignore this release?', yes: 'Ignore', danger: true },
    );
    if (!sure) return;
    await post(`/api/adding/imports/${encodeURIComponent(item.serviceId)}/${encodeURIComponent(item.downloadId)}`, { action: 'ignore' });
    toast('Ignored: the next run leaves it alone.');
    setTimeout(loadWaiting, 3000);
  }

  $effect(() => {
    void loadWaiting();
  });

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
    const timer = setTimeout(load, 250);
    return () => clearTimeout(timer);
  });

  async function run() {
    await post('/api/adding/run');
    toast('Running: the suggestions are read, then the titles tried.');
    setTimeout(load, 3000);
  }

  async function clear() {
    const sure = await ask(
      "Take every waiting title out of the list, except wishes and the titles taken from Radarr? Overfin's suggestions come back at its next reading, in its order. Titles downloading, imported or monitored for their release stay.",
      { title: 'Clear the waiting list?', yes: 'Clear', danger: true },
    );
    if (!sure) return;
    await post('/api/adding/clear');
    toast('The waiting list is cleared.');
    await load();
  }
</script>

<PageHead title="Adding" description="What Overtide brings in, searched for a few at a time.">
  {#snippet actions()}
    {#if data && data.mode !== 'off'}
      <Busy action={run} label="Run now: reads the suggestions, then tries the titles whose turn it is">Run now</Busy>
    {/if}
    {#if data?.total}
      <Busy kind="danger" action={clear}>Clear the waiting list</Busy>
    {/if}
  {/snippet}
</PageHead>

{#if failure}<Notice warn>{failure}</Notice>{/if}
{#if data?.lastError}<Notice warn>Last run: {data.lastError}</Notice>{/if}

<NowPanel />

{#if waiting.length}
  <details class="card" open>
    <summary>Waiting to be imported <Badge kind="plain">{waiting.reduce((n, item) => n + item.files.length, 0)}</Badge></summary>
    <p class="hint">Nothing says which episode these files are, so they aren't imported on a guess. Nothing is needed from you: Overtide keeps trying, with other releases too. You can settle them yourself.</p>
    {#each waiting as item (item.serviceId + item.downloadId)}
      <div class="waiting">
        <div class="waiting-head">
          <div>
            <span class="name">{item.series || item.release}</span>
            <div class="muted release">{item.release}</div>
          </div>
          <div class="waiting-actions">
            <button type="button" onclick={() => (choosing = item)}>Choose episodes</button>
            <Busy action={() => another(item)}>Try another release</Busy>
            <Busy kind="danger" action={() => ignore(item)}>Ignore</Busy>
          </div>
        </div>
        <details>
          <summary class="muted">{item.files.length} {item.files.length === 1 ? 'file' : 'files'}</summary>
          <div class="table-wrap">
            <table>
              <thead><tr><th>File</th><th>Why it waits</th></tr></thead>
              <tbody>
                {#each item.files as file (file.path)}
                  <tr><td class="filename">{fileName(file.path)}</td><td class="muted">{file.why}</td></tr>
                {/each}
              </tbody>
            </table>
          </div>
        </details>
      </div>
    {/each}
  </details>
{/if}

{#if choosing}
  <ImportForm
    item={choosing}
    saved={() => {
      choosing = null;
      toast('Noted: the next run imports them.');
      setTimeout(loadWaiting, 3000);
    }}
    close={() => (choosing = null)}
  />
{/if}

{#if !data}
  <Loading />
{:else}
  <p class="muted">
    {#if data.mode === 'off'}
      Adding is off.
    {:else}
      {#if data.total}<b>{data.matching}</b>{#if data.matching !== data.total} of {data.total}{/if} titles{:else}Nothing yet: Overfin's suggestions are read every hour.{/if}{#if data.nextRead}{' · '}suggestions read next at {clock(data.nextRead)}{/if}{#if data.nextRun}{' · '}titles tried next at {clock(data.nextRun)}{/if}
    {/if}
  </p>

  {#if data.total}
    <div class="toolbar">
      <label class="field"><span>Search</span><input type="search" bind:value={q} placeholder="Title…" /></label>
      <label class="field"><span>Source</span>
        <select bind:value={source}>
          <option value="">All ({data.total})</option>
          {#each data.sources as s (s.source)}<option value={s.source}>{sourceLabels[s.source] ?? s.source} ({s.count})</option>{/each}
        </select>
      </label>
    </div>
  {/if}

  {#each data.groups as group (group.state)}
    {@const about = groups[group.state] ?? { label: group.state, hint: '', when: 'When', open: false }}
    <details class="card" open={about.open || all === group.state}>
      <summary>{about.label} <Badge kind="plain">{group.count}</Badge></summary>
      {#if about.hint}<p class="hint">{about.hint}</p>{/if}
      <div class="table-wrap">
        <table>
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
    {#if data.total && data.mode !== 'off'}<Empty>No title matches these filters.</Empty>{/if}
  {/each}
{/if}

<style>
  .waiting {
    border-top: 1px solid var(--line, rgba(128, 128, 128, 0.25));
    padding: 0.75rem 0;
  }
  .waiting-head {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem 1rem;
    justify-content: space-between;
    align-items: flex-start;
  }
  .waiting-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .release,
  .filename {
    overflow-wrap: anywhere;
  }
</style>
