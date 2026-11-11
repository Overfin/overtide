<!-- A bar chart of values by period: one bar per period, its series
     stacked (stacked), or side by side. Hovering or focusing a period shows
     every series' value there; the table under it has them all. -->
<script module lang="ts">
  export interface Series {
    label: string;
    /** color: a CSS color, by role (var(--series-1)…). */
    color: string;
    values: number[];
  }
</script>

<script lang="ts">
  let {
    labels,
    long,
    series,
    stacked = false,
    bytes = false,
    format,
    title,
  }: {
    /** labels: each period's short name (axis); long, its full one (tooltip, table). */
    labels: string[];
    long: string[];
    series: Series[];
    stacked?: boolean;
    /** bytes: values are bytes (the axis steps in their units). */
    bytes?: boolean;
    format: (v: number) => string;
    title: string;
  } = $props();

  let width = $state(600);
  const height = 220;
  const pad = { top: 8, right: 8, bottom: 22, left: 56 };

  const totals = $derived(labels.map((_, i) => series.reduce((n, s) => n + (s.values[i] ?? 0), 0)));
  const peak = $derived(stacked ? Math.max(0, ...totals) : Math.max(0, ...series.flatMap((s) => s.values)));
  // The axis: four even steps of a round size (1, 2, 2.5 or 5 times a
  // power of ten, of the unit: 1024's powers for bytes); whole steps for
  // counts.
  const unit = $derived(bytes && peak > 0 ? Math.pow(1024, Math.floor(Math.log(peak) / Math.log(1024))) : 1);
  const step = $derived.by(() => {
    const raw = peak / unit / 4;
    if (raw <= 0) return unit;
    const p = Math.pow(10, Math.floor(Math.log10(raw)));
    let m = [1, 2, 2.5, 5, 10].find((m) => m * p >= raw) ?? 10;
    let s = m * p;
    if (!bytes && s < 1) s = 1;
    if (!bytes && !Number.isInteger(s)) s = Math.ceil(s);
    return s * unit;
  });
  const top = $derived(step * 4);
  const ticks = $derived([0, 1, 2, 3, 4].map((k) => k * step));

  const plotW = $derived(Math.max(10, width - pad.left - pad.right));
  const plotH = height - pad.top - pad.bottom;
  const band = $derived(plotW / Math.max(1, labels.length));
  const gap = 2; // the surface gap between fills
  const barW = $derived(Math.max(1, (stacked ? band * 0.7 : (band * 0.8) / series.length) - gap));
  const y = (v: number) => pad.top + plotH - (v / top) * plotH;
  // Every nth label, so they don't run into each other.
  const every = $derived(Math.max(1, Math.ceil(labels.length / Math.max(1, Math.floor(plotW / 70)))));

  // A bar's rectangle, its top end rounded (4px) when tall enough.
  function bar(x: number, y0: number, y1: number, w: number, round: boolean): string {
    const h = y0 - y1;
    if (h <= 0) return '';
    const r = round ? Math.min(4, w / 2, h) : 0;
    return `M${x},${y0} V${y1 + r} Q${x},${y1} ${x + r},${y1} H${x + w - r} Q${x + w},${y1} ${x + w},${y1 + r} V${y0} Z`;
  }

  const marks = $derived.by(() => {
    const out: { d: string; color: string }[] = [];
    labels.forEach((_, i) => {
      const x0 = pad.left + i * band;
      if (stacked) {
        let base = 0;
        const last = series.map((s) => (s.values[i] ?? 0) > 0).lastIndexOf(true);
        series.forEach((s, k) => {
          const v = s.values[i] ?? 0;
          if (v <= 0) return;
          const yBottom = y(base) - (base > 0 ? gap : 0);
          base += v;
          out.push({ d: bar(x0 + (band - barW) / 2, yBottom, y(base), barW, k === last), color: s.color });
        });
      } else {
        const groupW = series.length * (barW + gap) - gap;
        series.forEach((s, k) => {
          const v = s.values[i] ?? 0;
          out.push({ d: bar(x0 + (band - groupW) / 2 + k * (barW + gap), y(0), y(v), barW, true), color: s.color });
        });
      }
    });
    return out;
  });

  let hover = $state<number | null>(null);
  const tipLeft = $derived(hover === null ? 0 : Math.min(width - 180, Math.max(0, pad.left + hover * band + band / 2 - 80)));
</script>

<div class="chart" bind:clientWidth={width}>
  {#if series.length > 1}
    <div class="legend">
      {#each series as s (s.label)}<span><span class="key" style:background={s.color}></span>{s.label}</span>{/each}
    </div>
  {/if}
  {#if labels.length === 0 || peak === 0}
    <div class="empty">Nothing in this range.</div>
  {:else}
    <svg {height} role="img" aria-label={title}>
      {#each ticks as t (t)}
        <line class={t === 0 ? 'baseline' : 'gridline'} x1={pad.left} x2={pad.left + plotW} y1={y(t)} y2={y(t)} />
        <text class="axis" x={pad.left - 8} y={y(t) + 4} text-anchor="end">{format(t)}</text>
      {/each}
      {#each marks as m, i (i)}{#if m.d}<path d={m.d} fill={m.color} />{/if}{/each}
      {#each labels as l, i (i)}
        {#if i % every === 0}<text class="axis" x={pad.left + i * band + band / 2} y={height - 6} text-anchor="middle">{l}</text>{/if}
        <rect class="hit" x={pad.left + i * band} y={pad.top} width={band} height={plotH} tabindex="0" role="button"
          aria-label={`${long[i]}: ${series.map((s) => `${s.label} ${format(s.values[i] ?? 0)}`).join(', ')}`}
          onpointerenter={() => (hover = i)} onpointerleave={() => (hover = null)} onfocus={() => (hover = i)} onblur={() => (hover = null)} />
      {/each}
    </svg>
    {#if hover !== null}
      <div class="tip" style:left={`${tipLeft}px`} style:top="0">
        <div class="when">{long[hover]}</div>
        {#each series as s (s.label)}
          <div class="row"><span class="stroke" style:background={s.color}></span><b>{format(s.values[hover] ?? 0)}</b><span>{s.label}</span></div>
        {/each}
      </div>
    {/if}
  {/if}
  <details>
    <summary>Table</summary>
    <table>
      <thead><tr><th>Period</th>{#each series as s (s.label)}<th>{s.label}</th>{/each}</tr></thead>
      <tbody>
        {#each labels as _, i (i)}
          <tr><td>{long[i]}</td>{#each series as s (s.label)}<td>{format(s.values[i] ?? 0)}</td>{/each}</tr>
        {/each}
      </tbody>
    </table>
  </details>
</div>
