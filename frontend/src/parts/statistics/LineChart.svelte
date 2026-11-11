<!-- A line chart of percentages by day, one line per series (2px), with a
     crosshair that finds the day under the pointer and shows every line's
     value there; the table under it has them all. -->
<script lang="ts">
  import type { Series } from './BarChart.svelte';

  let { labels, long, series, title }: { labels: string[]; long: string[]; series: Series[]; title: string } = $props();

  let width = $state(600);
  const height = 220;
  const pad = { top: 8, right: 8, bottom: 22, left: 44 };
  const plotW = $derived(Math.max(10, width - pad.left - pad.right));
  const plotH = height - pad.top - pad.bottom;
  const x = (i: number) => pad.left + (labels.length > 1 ? (i / (labels.length - 1)) * plotW : plotW / 2);
  const y = (v: number) => pad.top + plotH - (v / 100) * plotH;
  const every = $derived(Math.max(1, Math.ceil(labels.length / Math.max(1, Math.floor(plotW / 70)))));
  const path = (values: number[]) =>
    values.map((v, i) => (Number.isFinite(v) ? `${i === 0 || !Number.isFinite(values[i - 1]) ? 'M' : 'L'}${x(i)},${y(v)}` : '')).join(' ');

  let hover = $state<number | null>(null);
  let svg = $state<SVGSVGElement | null>(null);
  function move(e: PointerEvent) {
    if (!svg || labels.length === 0) return;
    const box = svg.getBoundingClientRect();
    const at = ((e.clientX - box.left - pad.left) / plotW) * (labels.length - 1);
    hover = Math.max(0, Math.min(labels.length - 1, Math.round(at)));
  }
  const pct = (v: number) => (Number.isFinite(v) ? `${v.toFixed(1)}%` : '—');
  const tipLeft = $derived(hover === null ? 0 : Math.min(width - 180, Math.max(0, x(hover) - 80)));
</script>

<div class="chart" bind:clientWidth={width}>
  {#if series.length > 1}
    <div class="legend">
      {#each series as s (s.label)}<span><span class="line-key" style:background={s.color}></span>{s.label}</span>{/each}
    </div>
  {/if}
  {#if labels.length === 0}
    <div class="empty">No sample in this range yet.</div>
  {:else}
    <svg bind:this={svg} {height} role="img" aria-label={title} onpointermove={move} onpointerleave={() => (hover = null)}>
      {#each [0, 25, 50, 75, 100] as t (t)}
        <line class={t === 0 ? 'baseline' : 'gridline'} x1={pad.left} x2={pad.left + plotW} y1={y(t)} y2={y(t)} />
        <text class="axis" x={pad.left - 8} y={y(t) + 4} text-anchor="end">{t}%</text>
      {/each}
      {#each labels as l, i (i)}
        {#if i % every === 0}<text class="axis" x={x(i)} y={height - 6} text-anchor="middle">{l}</text>{/if}
      {/each}
      {#if hover !== null}<line class="baseline" x1={x(hover)} x2={x(hover)} y1={pad.top} y2={pad.top + plotH} />{/if}
      {#each series as s (s.label)}
        <path d={path(s.values)} fill="none" stroke={s.color} stroke-width="2" stroke-linejoin="round" stroke-linecap="round" />
        {#if labels.length === 1 || hover !== null}
          {@const i = hover ?? 0}
          {#if Number.isFinite(s.values[i])}<circle cx={x(i)} cy={y(s.values[i])} r="4" fill={s.color} stroke="var(--panel)" stroke-width="2" />{/if}
        {/if}
      {/each}
    </svg>
    {#if hover !== null}
      <div class="tip" style:left={`${tipLeft}px`} style:top="0">
        <div class="when">{long[hover]}</div>
        {#each series as s (s.label)}
          <div class="row"><span class="stroke" style:background={s.color}></span><b>{pct(s.values[hover])}</b><span>{s.label}</span></div>
        {/each}
      </div>
    {/if}
  {/if}
  <details>
    <summary>Table</summary>
    <table>
      <thead><tr><th>Day</th>{#each series as s (s.label)}<th>{s.label}</th>{/each}</tr></thead>
      <tbody>
        {#each labels as _, i (i)}
          <tr><td>{long[i]}</td>{#each series as s (s.label)}<td>{pct(s.values[i])}</td>{/each}</tr>
        {/each}
      </tbody>
    </table>
  </details>
</div>
