<!-- Home: storage, library, torrents, what Overtide removed so far and the
     services, at a glance. -->
<script lang="ts">
  import '../styles/storage.css';
  import { PageHead, Card, Loading, Notice } from '@overfin/ui';
  import { get } from '../lib/api';
  import { follow } from '../lib/live.svelte';
  import { human, identity, patchBytes, type Home, type StatsDevice } from '../lib/types-storage';
  import StorageBar from '../parts/storage/StorageBar.svelte';

  let data = $state<Home | null>(null);
  let failure = $state('');

  async function load() {
    try {
      data = await get<Home>('/api/home');
      failure = '';
    } catch (err) {
      failure = (err as Error).message;
    }
  }

  // A disk's bytes ticking only updates the bars and their lines.
  async function tick() {
    if (!data) return;
    try {
      const stats = await get<StatsDevice[]>('/api/storage/stats');
      for (const s of stats) {
        const device = data.devices.find((d) => d.path === s.representativePath);
        if (device) patchBytes(device, s);
      }
    } catch {
      // The next tick tries again; the last numbers stay.
    }
  }

  $effect(() => {
    load();
    return follow('immediate', load, tick);
  });
</script>

<PageHead title="Home" description="Your storage, library and torrents at a glance." />

{#if failure}<Notice warn>{failure}</Notice>{/if}
{#if !data}
  {#if !failure}<Loading />{/if}
{:else}
  {#if data.error}<Notice warn>{data.error}</Notice>{/if}
  <div class="home-grid">
    <Card title="Storage">
      {#snippet badge()}<a class="card-link" href="/storage">View</a>{/snippet}
      {#each data.devices as d (d.path)}
        <div class="storage-device">
          {#if d.name}<div>{d.name}</div>{/if}
          <div class="muted">{identity(d)}</div>
          {#if d.available}
            <StorageBar device={d} />
            <div class="muted">{human(d.overtideUsedBytes)} used · {human(d.freeBytes)} free{#if d.elsewhereBytes > 0}{' · '}{human(d.elsewhereBytes)} used elsewhere{/if}, of {human(d.capacityBytes)}</div>
          {:else}
            <p class="bad">UNAVAILABLE{#if d.error}: {d.error}{/if}</p>
          {/if}
        </div>
      {:else}
        <p class="muted">No storage devices are known yet; file reconciliation has not completed.</p>
      {/each}
    </Card>

    <Card title="Library">
      {#snippet badge()}<a class="card-link" href="/library">View</a>{/snippet}
      <div class="big">{data.totalMedia}</div>
      <div class="kv">
        {#if data.hasMovieLibrary}<span>Movies</span><b>{data.movies}</b>{/if}
        {#if data.hasSeriesLibrary}<span>Series</span><b>{data.series}</b>{/if}
        <span>Library size</span><b>{human(data.libraryBytes)}</b>
      </div>
    </Card>

    <Card title="Torrents">
      {#snippet badge()}<a class="card-link" href="/torrents">View</a>{/snippet}
      <div class="big">{data.totalTorrents}</div>
      <div class="kv">
        <span>Current</span><b class="current-status">{data.current}</b>
        <span>Superseded</span><b class="superseded">{data.superseded}</b>
        <span>Orphaned</span><b class="orphaned">{data.orphaned}</b>
        <span>Unassociated</span><b class="unassociated">{data.unassociated}</b>
        {#if data.obsoleteKnown > 0}<span>Known reclaimable</span><b>{human(data.obsoleteReclaimable)}</b>{/if}
      </div>
      {#if data.unmanagedError}<p class="warn">Unmanaged scan unavailable: {data.unmanagedError}</p>{/if}
    </Card>

    <Card title="Lifetime statistics">
      {#snippet badge()}<a class="card-link" href="/history">View</a>{/snippet}
      <div class="big">{human(data.stats.reclaimedBytes)}</div>
      <div class="muted">actual space reclaimed</div>
      <div class="kv">
        <span>Cleanup runs</span><b>{data.stats.runs}</b>
        <span>Media removed</span><b>{data.stats.mediaRemoved}</b>
        <span>Torrents removed</span><b>{data.stats.torrentsRemoved}</b>
        <span>Library bytes removed</span><b>{human(data.stats.mediaBytes)}</b>
        <span>Last 30 days</span><b>{human(data.stats.last30Bytes)}</b>
      </div>
    </Card>

    <Card title="Services">
      {#snippet badge()}<a class="card-link" href="/services">View</a>{/snippet}
      {#if data.services.length}
        <div class="kv">
          {#each data.services as s (s.name)}<span>{s.name}</span><b class={s.ok ? 'good' : 'bad'}>{s.ok ? 'Connected' : 'Unavailable'}</b>{/each}
        </div>
      {:else}
        <div class="muted">No services configured yet.</div>
      {/if}
    </Card>
  </div>
{/if}
