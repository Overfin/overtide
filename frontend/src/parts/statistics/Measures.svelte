<!-- Whether the library follows the people using it, as Overfin measures
     it over the last 90 days: totals for the whole server, nothing about
     one person. Asked apart, so the rest of the page doesn't wait. -->
<script lang="ts">
  import { Card, Loading, Notice } from '@overfin/ui';
  import { get } from '../../lib/api';

  interface Measures {
    days: number;
    arrived: number;
    started: number;
    finished: number;
    left: number;
    missed: number;
    library: number;
    watched: number;
  }

  let data = $state<{ connected: boolean; measures?: Measures; problem?: string } | null>(null);
  let failure = $state('');
  get<{ connected: boolean; measures?: Measures; problem?: string }>('/api/statistics/measures').then(
    (d) => (data = d),
    (err) => (failure = (err as Error).message),
  );

  const share = (part: number, whole: number) => (whole ? `${Math.round((part / whole) * 100)}%` : '—');
  const count = (n: number) => n.toLocaleString('en');
</script>

<Card title="How the library is used" description="From Overfin, over the last 90 days, for the whole server: whether what comes in gets watched, and whether what goes out is missed. Films and seasons count as one each.">
  {#if failure}
    <Notice warn>{failure}</Notice>
  {:else if !data}
    <Loading />
  {:else if !data.connected}
    <p class="muted">Connect Overfin in Services to see these.</p>
  {:else if !data.measures}
    <Notice warn>Overfin didn't answer: {data.problem}. Its key needs "What came and went".</Notice>
  {:else}
    {@const m = data.measures}
    <div class="tiles">
      <div class="tile"><div class="label">Arrived, then started</div><div class="value">{share(m.started, m.arrived)}</div><div class="sub">{count(m.started)} of {count(m.arrived)} films and seasons; {share(m.finished, m.arrived)} watched to the end</div></div>
      <div class="tile"><div class="label">Library in use</div><div class="value">{share(m.watched, m.library)}</div><div class="sub">{count(m.watched)} of {count(m.library)} watched, some at least</div></div>
      <div class="tile"><div class="label">Left, then wished for</div><div class="value">{count(m.missed)}</div><div class="sub">of {count(m.left)} films and seasons that left</div></div>
    </div>
  {/if}
</Card>
