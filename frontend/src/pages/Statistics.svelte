<!-- Statistics: what came and what went over a range, by period, and each
     disk's use over time. -->
<script lang="ts">
  import '../styles/statistics.css';
  import { Card, Loading, Notice, PageHead } from '@overfin/ui';
  import { get } from '../lib/api';
  import { human } from '../lib/types-storage';
  import BarChart from '../parts/statistics/BarChart.svelte';
  import LineChart from '../parts/statistics/LineChart.svelte';

  interface Period {
    start: string;
    films: number;
    episodes: number;
    filmBytes: number;
    episodeBytes: number;
    byOvertide: number;
    overtideBytes: number;
    titlesRemoved: number;
    torrentsRemoved: number;
    otherRemoved: number;
    freedBytes: number;
  }
  interface Stats {
    range: string;
    period: 'day' | 'week' | 'month';
    since: string;
    periods: Period[];
    totals: Period;
    disks: { device: string; samples: { day: string; used: number; services: number; total: number }[] }[];
    samplesSince?: string;
  }

  const ranges = [
    ['30d', 'Last 30 days'],
    ['90d', 'Last 90 days'],
    ['1y', 'Last year'],
    ['all', 'Since the start'],
  ] as const;

  let range = $state('90d');
  let data = $state<Stats | null>(null);
  let failure = $state('');
  let loading = $state(false);

  async function load() {
    loading = true;
    try {
      data = await get<Stats>(`/api/statistics?range=${range}`);
      failure = '';
    } catch (err) {
      failure = (err as Error).message;
    } finally {
      loading = false;
    }
  }
  $effect(() => {
    void range;
    load();
  });

  const day = (s: string) => new Date(`${s}T12:00:00Z`);
  const fmt = (s: string, o: Intl.DateTimeFormatOptions) => day(s).toLocaleDateString(undefined, { timeZone: 'UTC', ...o });
  // Each period's short name (axis) and full one (tooltip, table).
  const short = (s: string) => (data?.period === 'month' ? fmt(s, { month: 'short', year: '2-digit' }) : fmt(s, { month: 'short', day: 'numeric' }));
  const long = (s: string) =>
    data?.period === 'month' ? fmt(s, { month: 'long', year: 'numeric' }) : data?.period === 'week' ? `Week of ${fmt(s, { month: 'long', day: 'numeric', year: 'numeric' })}` : fmt(s, { weekday: 'short', month: 'long', day: 'numeric', year: 'numeric' });

  const rows = $derived(data?.periods ?? []);
  const labels = $derived(rows.map((p) => short(p.start)));
  const longs = $derived(rows.map((p) => long(p.start)));
  const count = (n: number) => Math.round(n).toLocaleString();

  const t = $derived(data?.totals);
  const added = $derived(t ? t.filmBytes + t.episodeBytes : 0);
  const files = $derived(t ? t.films + t.episodes : 0);
  const removed = $derived(t ? t.titlesRemoved + t.torrentsRemoved + t.otherRemoved : 0);

  // Disk use: every day sampled, each disk's used share of its size.
  const diskDays = $derived([...new Set((data?.disks ?? []).flatMap((d) => d.samples.map((s) => s.day)))].sort());
  const diskSeries = $derived(
    (data?.disks ?? []).slice(0, 3).map((d, i) => {
      const byDay = new Map(d.samples.map((s) => [s.day, s.total > 0 ? (s.used / s.total) * 100 : NaN]));
      return { label: d.device, color: `var(--series-${i + 1})`, values: diskDays.map((x) => byDay.get(x) ?? NaN) };
    }),
  );
</script>

<PageHead title="Statistics" description="What Radarr and Sonarr imported, what was removed, and how full each disk is, over time." />

<div class="statistics">
  <div class="ranges" role="group" aria-label="Range">
    {#each ranges as [value, label] (value)}
      <button type="button" class={range === value ? 'small primary' : 'small'} aria-pressed={range === value} onclick={() => (range = value)}>{label}</button>
    {/each}
  </div>

  {#if failure}<Notice warn>{failure}</Notice>{/if}

  {#if !data}
    <Loading />
  {:else}
    <div style:opacity={loading ? 0.5 : 1}>
      <div class="tiles">
        <div class="tile"><div class="label">Added</div><div class="value">{human(added)}</div><div class="sub">{count(files)} files: {count(t!.films)} films, {count(t!.episodes)} episodes</div></div>
        <div class="tile"><div class="label">Freed</div><div class="value">{human(t!.freedBytes)}</div><div class="sub">{count(removed)} removals: {count(t!.titlesRemoved)} titles, {count(t!.torrentsRemoved)} torrents</div></div>
        <div class="tile"><div class="label">Net change</div><div class="value">{added >= t!.freedBytes ? '+' : '−'}{human(Math.abs(added - t!.freedBytes))}</div><div class="sub">added minus freed</div></div>
        <div class="tile"><div class="label">Brought by Overtide</div><div class="value">{count(t!.byOvertide)} files</div><div class="sub">{human(t!.overtideBytes)}{files ? `, ${Math.round((t!.byOvertide / files) * 100)}% of the files added` : ''}</div></div>
      </div>

      <div class="charts">
        <Card title="Space added and freed" description={`By ${data.period}: what Radarr and Sonarr imported, and what removals gave back.`}>
          <BarChart title="Space added and freed" {labels} long={longs} format={human} bytes
            series={[
              { label: 'Added', color: 'var(--series-1)', values: rows.map((p) => p.filmBytes + p.episodeBytes) },
              { label: 'Freed', color: 'var(--series-2)', values: rows.map((p) => p.freedBytes) },
            ]} />
        </Card>
        <Card title="Files imported" description={`By ${data.period}, films and episodes.`}>
          <BarChart title="Files imported" {labels} long={longs} format={count} stacked
            series={[
              { label: 'Films', color: 'var(--series-1)', values: rows.map((p) => p.films) },
              { label: 'Episodes', color: 'var(--series-2)', values: rows.map((p) => p.episodes) },
            ]} />
        </Card>
        <Card title="Removals" description={`By ${data.period}: titles (films, series, seasons) and torrents removed. Dry runs aren't counted.`}>
          <BarChart title="Removals" {labels} long={longs} format={count} stacked
            series={[
              { label: 'Titles', color: 'var(--series-1)', values: rows.map((p) => p.titlesRemoved) },
              { label: 'Torrents', color: 'var(--series-2)', values: rows.map((p) => p.torrentsRemoved) },
              ...(t!.otherRemoved ? [{ label: 'Other files', color: 'var(--series-3)', values: rows.map((p) => p.otherRemoved) }] : []),
            ]} />
        </Card>
        <Card title="Disk use" description={data.samplesSince ? `How full each disk is, each day. Sampled since ${fmt(data.samplesSince, { month: 'long', day: 'numeric', year: 'numeric' })}.` : 'How full each disk is, each day: sampled from now on.'}>
          <LineChart title="Disk use" labels={diskDays.map((d) => fmt(d, { month: 'short', day: 'numeric' }))} long={diskDays.map((d) => fmt(d, { weekday: 'short', month: 'long', day: 'numeric', year: 'numeric' }))} series={diskSeries} />
        </Card>
      </div>
    </div>
  {/if}
</div>
