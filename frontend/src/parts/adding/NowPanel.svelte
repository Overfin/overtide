<!-- What Overtide is doing now, asked again every 5 seconds while the
     page is shown. -->
<script lang="ts">
  import { Card, Progress } from '@overfin/ui';
  import '../../styles/adding.css';
  import { get } from '../../lib/api';
  import type { Now } from '../../lib/types-adding';

  let now = $state<Now | null>(null);

  async function refresh() {
    if (document.hidden) return;
    try {
      now = await get<Now>('/api/adding/now');
    } catch {
      // Not answering now: the next turn tries again.
    }
  }

  $effect(() => {
    refresh();
    const timer = setInterval(refresh, 5000);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', refresh);
    };
  });
</script>

<Card title="Now">
  {#if now}
    <div aria-live="polite">
      <div class="now-doing">
        <span class="pulse" class:on={now.running} aria-hidden="true"></span>
        <div>
          <b>{now.doing}</b>{#if now.dryRun} <span class="badge warn">Dry run</span>{/if}
          {#if now.detail}<div>{now.detail} <span class="muted">· {now.since}</span></div>{/if}
          {#if now.budget}<div class="muted">{now.searches} of {now.budget} searches this run</div>{/if}
        </div>
      </div>
      {#if now.holding.length}
        <ul class="holding">{#each now.holding as h, i (i)}<li>{h}</li>{/each}</ul>
      {/if}
    </div>
    <h3>Tried next</h3>
    {#each now.next as n, i (i)}
      <div class="next"><b>{n.title}</b> <span class="muted">· {n.kind === 'series' ? 'Series' : 'Film'} · {n.when}</span></div>
    {:else}
      <p class="muted">Nothing waits.</p>
    {/each}
  {:else}
    <p class="muted">Asking Overtide…</p>
  {/if}
</Card>

{#if now}
  <Card title="Downloading">
    {#each now.downloads as d, i (i)}
      <div class="download">
        <div><b>{d.title}</b> <span class="muted">· {d.kind === 'series' ? 'Series' : 'Film'}</span></div>
        {#if d.queued}<Progress value={d.percent} label={`${d.title}: ${d.percent}%`} />{/if}
        <div class="hint">{#if d.queued}{d.percent}%{' · '}{/if}{d.status}{#if d.done}{' · '}{d.done}{/if}</div>
      </div>
    {:else}
      <p class="muted">Nothing of Overtide's is downloading.</p>
    {/each}
  </Card>
{/if}
