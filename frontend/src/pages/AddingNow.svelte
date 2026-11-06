<!-- Adding, now: what it's doing, what's downloading, and what waits to
     be imported. The waiting titles and the seasons and episodes have their
     own pages. -->
<script lang="ts">
  import { PageHead, Notice, Badge, Busy, ask, toast } from '@overfin/ui';
  import { get, post } from '../lib/api';
  import type { Adding, WaitingImport } from '../lib/types-adding';
  import { clock } from '../lib/words-adding';
  import NowPanel from '../parts/adding/NowPanel.svelte';
  import ImportForm from '../parts/adding/ImportForm.svelte';

  let data = $state<Adding | null>(null);
  let failure = $state('');
  let waiting = $state<WaitingImport[]>([]);
  let choosing = $state<WaitingImport | null>(null);

  const fileName = (path: string) => path.slice(path.lastIndexOf('/') + 1);

  async function load() {
    try {
      data = await get<Adding>('/api/adding');
      failure = '';
    } catch (err) {
      failure = (err as Error).message;
    }
  }

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

  async function run() {
    await post('/api/adding/run');
    toast('Running: the suggestions are read, then the titles tried.');
    setTimeout(load, 3000);
  }

  load();
  loadWaiting();

  const seasons = $derived(data ? data.groups.filter((g) => g.state === 'seasons' || g.state === 'episodes').reduce((n, g) => n + g.count, 0) : 0);
  const titles = $derived(data ? data.groups.filter((g) => g.state === 'waiting' || g.state === 'not released').reduce((n, g) => n + g.count, 0) : 0);
</script>

<PageHead title="Adding" description="What Overtide is doing now, what's downloading, and what waits to be imported.">
  {#snippet actions()}
    {#if data && data.mode !== 'off'}
      <Busy action={run} label="Run now: reads the suggestions, then tries the titles whose turn it is">Run now</Busy>
    {/if}
  {/snippet}
</PageHead>

{#if failure}<Notice warn>{failure}</Notice>{/if}
{#if data?.lastError}<Notice warn>Last run: {data.lastError}</Notice>{/if}
{#if data}
  <p class="muted">
    {#if data.mode === 'off'}
      Adding is off.
    {:else}
      <a href="/adding/waiting">{titles} {titles === 1 ? 'title waits' : 'titles wait'}</a>{' · '}<a href="/adding/seasons">{seasons} {seasons === 1 ? 'season or episode is' : 'seasons and episodes are'} wanted</a>{#if data.nextRead}{' · '}suggestions read next at {clock(data.nextRead)}{/if}{#if data.nextRun}{' · '}titles tried next at {clock(data.nextRun)}{/if}
    {/if}
  </p>
{/if}

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
