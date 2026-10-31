<!-- History: what Overtide removed, or would have in a dry run, and how it
     went; an operation's own row is #operation-<id>. -->
<script lang="ts">
  import '../styles/library.css';
  import { tick } from 'svelte';
  import { PageHead, Notice, Loading } from '@overfin/ui';
  import { get } from '../lib/api';
  import { follow } from '../lib/live.svelte';
  import { route } from '../lib/router.svelte';
  import type { CleanupRun, HistoryEvent } from '../lib/types-library';
  import { stamp } from '../lib/types-library';

  let data = $state<{ events: HistoryEvent[]; runs: CleanupRun[] } | null>(null);
  let problem = $state('');
  let scrolled = '';

  async function load() {
    try {
      data = await get('/api/history');
      problem = '';
      // A link to one operation (#operation-12) shows it, once.
      if (route.hash && route.hash !== scrolled) {
        scrolled = route.hash;
        await tick();
        document.getElementById(route.hash.slice(1))?.scrollIntoView({ block: 'center' });
      }
    } catch (err) {
      problem = (err as Error).message;
    }
  }

  $effect(() => {
    void route.hash;
    load();
  });
  $effect(() => follow('stable-list', load));

  const plural = (n: number) => (n === 1 ? '' : 's');
</script>

<PageHead title="History" description="What Overtide removed, or would have in a dry run, and how it went." />
{#if problem}<Notice warn>{problem}</Notice>{/if}

{#if !data}
  {#if !problem}<Loading />{/if}
{:else}
  <h2>Removal events</h2>
  {#if data.events.length}
    <div class="table-wrap">
      <table>
        <thead><tr><th>When</th><th>Object</th><th>Mode</th><th>Status</th><th>Reclaimable</th><th>Error</th></tr></thead>
        <tbody>
          {#each data.events as e (e.id)}
            <tr id={`operation-${e.id}`} class:selected={route.hash === `#operation-${e.id}`}>
              <td class="nowrap">{stamp(e.createdAt)}</td>
              <td class="history-object"><b>{e.label}</b><br /><span class="muted">{e.kind}{#if e.serviceName} · {e.serviceName}{/if}</span></td>
              <td>{#if e.dryRun}<span class="dry">dry run</span>{:else}live{/if}</td>
              <td>{e.status}</td>
              <td class="num">{e.reclaimable}</td>
              <td class="error">{e.error || ''}</td>
            </tr>
            {#if e.files.length || e.results.length || e.errors.length}
              <tr>
                <td colspan="6">
                  <details>
                    <summary>Operation details · {e.files.length} path{plural(e.files.length)}</summary>
                    <div class="history-files">
                      {#each e.files as f, i (i)}
                        <div class="mono"><strong>{f.owner}{f.selected ? ' · selected' : ' · preserved'}:</strong> {f.path}</div>
                      {/each}
                      {#each e.results as r, i (i)}<div>{r}</div>{/each}
                      {#each e.errors as r, i (i)}<div class="error">{r}</div>{/each}
                    </div>
                  </details>
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <p class="muted">No removal events yet.</p>
  {/if}

  {#if data.runs.length}
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
