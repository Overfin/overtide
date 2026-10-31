<!-- Storage: where the services keep files, how much is free, each
     device's cleanup plan and settings, and the titles never to remove. -->
<script lang="ts">
  import '../styles/storage.css';
  import { PageHead, Card, Loading, Notice, Busy } from '@overfin/ui';
  import { get, post } from '../lib/api';
  import { follow } from '../lib/live.svelte';
  import { human, identity, patchBytes, share, type StatsDevice, type Storage, type StorageDevice } from '../lib/types-storage';
  import StorageBar from '../parts/storage/StorageBar.svelte';
  import DeviceSettings from '../parts/storage/DeviceSettings.svelte';
  import CleanupPlan from '../parts/storage/CleanupPlan.svelte';

  let data = $state<Storage | null>(null);
  let failure = $state('');
  // The windows open: a device's settings, or its cleanup plan.
  let settingsFor = $state<string | null>(null);
  let planFor = $state<string | null>(null);

  async function load() {
    try {
      data = await get<Storage>('/api/storage');
      failure = '';
    } catch (err) {
      failure = (err as Error).message;
    }
  }

  // A disk's bytes ticking only updates the numbers: the plans, which are
  // real work, are built again only on a real change.
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

  const usedPercent = (d: StorageDevice) => (d.usableBytes ? (d.overtideUsedBytes / d.usableBytes) * 100 : 0);
  const planShown = (d: StorageDevice) => d.enabled && !!d.plan && d.plan.needBytes > 0 && d.plan.actions > 0;

  async function unkeep(key: string) {
    await post('/api/storage/keep', { key, unkeep: true });
    await load();
  }
</script>

<PageHead title="Storage" description="Where your services keep files, how much is free, and the titles never to remove." />

{#if failure}<Notice warn>{failure}</Notice>{/if}
{#if !data}
  {#if !failure}<Loading />{/if}
{:else}
  {#if data.capabilities.length}
    <Card title="Storage capabilities" description="Every path your services have reported, and what Overtide currently knows about it.">
      <ul class="capability-list">
        {#each data.capabilities as c (c.path + c.serviceName)}
          <li>
            <code>{c.path}</code> — {c.serviceName} ({c.serviceType}){#if c.purpose}{' · '}{c.purpose}{/if}
            {#if c.reachable}
              <span class="muted">· {c.filesystem}, {human(c.freeBytes)} free{#if c.reservedBytes > 0}, {human(c.reservedBytes)} reserved{/if}</span>
            {:else}
              <span class="warn">· not reachable from Overtide's own container</span>
              {#if c.selfReportedCount > 0}
                <span class="muted">— {c.serviceName} reports {c.selfReportedCount} item(s), {human(c.selfReportedSizeBytes)}, that would become visible once mounted</span>
              {/if}
            {/if}
          </li>
        {/each}
      </ul>
      <p class="muted">Add a matching volume mount to Overtide's own container for any path marked not reachable — the host-side path is whatever directory you already mounted into that service's own container; Overtide can't see or guess that part. Reconciliation keeps running for everything else in the meantime: media and torrents under these paths are simply excluded from automatic removal until the mount is fixed.</p>
    </Card>
  {/if}

  {#each data.devices as d (d.path)}
    <section>
      <div class="card-head">
        <div>
          {#if d.name}<h2>{d.name}</h2>{/if}
          <p>{identity(d)}</p>
        </div>
        <div class="actions-row">
          {#if planShown(d)}
            <button type="button" class="small ghost" aria-label="Cleanup plan" title="Cleanup plan" onclick={() => (planFor = d.path)}>📋</button>
          {/if}
          <button type="button" class="small ghost" aria-label="Device settings" title="Device settings" onclick={() => (settingsFor = d.path)}>🔧</button>
        </div>
      </div>
      {#if d.available}
        <p class="muted">{human(d.overtideUsedBytes)} used of {human(d.usableBytes)} usable ({usedPercent(d).toFixed(1)}%, target {(d.plan?.targetUsagePercent ?? 0).toFixed(1)}%){#if d.reservedBytes > 0}{' · '}{human(d.reservedBytes)} reserved by the filesystem{/if}{#if d.elsewhereBytes > 0}{' · '}{human(d.elsewhereBytes)} used elsewhere on this {human(d.totalBytes)} disk{/if}</p>
        <StorageBar device={d} detail target={d.enabled ? (d.plan?.targetUsagePercent ?? null) : null} />
        <ul class="storage-legend">
          {#each d.claimed as c, i (c.service)}
            <li class="claim c{i % 4}">
              <div class="claim-label"><span class="swatch"></span>{c.service} · {human(c.bytes)} · {share(c.bytes, d.capacityBytes)}%</div>
              {#if c.roots.length}
                <ul class="service-roots">{#each c.roots as root (root)}<li><span class="mono">{root}</span></li>{/each}</ul>
              {/if}
            </li>
          {/each}
          {#if d.unmanagedBytes > 0}<li class="unmanaged"><span class="swatch"></span>Unmanaged · {human(d.unmanagedBytes)} · {share(d.unmanagedBytes, d.capacityBytes)}%</li>{/if}
          <li class="free"><span class="swatch"></span>Free · {human(d.freeBytes)} · {share(d.freeBytes, d.capacityBytes)}%</li>
          {#if d.elsewhereBytes > 0}<li class="elsewhere"><span class="swatch"></span>Used elsewhere · {human(d.elsewhereBytes)} · {share(d.elsewhereBytes, d.capacityBytes)}%</li>{/if}
        </ul>
        {#if data.autoRemovalDisabled}
          <p class="muted">Automatic removal is off. Enable it in <a href="/settings">Settings</a> to see suggestions.</p>
        {:else if !d.enabled}
          <p class="muted">Automatic removal is off for this device. Enable it in Device settings (🔧) to see suggestions.</p>
        {:else if d.plan && d.plan.needBytes > 0}
          <p class="bad"><b>Cleanup active.</b><br />{d.plan.message}<br />{d.plan.actions} action(s) selected · {human(d.plan.selectedBytes)} planned.{#if d.plan.actions} See the cleanup plan (📋) for the full list.{/if}</p>
        {:else}
          <p class="good"><b>Cleanup inactive.</b><br />{d.plan?.message}</p>
        {/if}
      {:else}
        <p class="bad">UNAVAILABLE{#if d.error}: {d.error}{/if}</p>
        {#if d.planError}<span class="bad">{d.planError}</span>{/if}
      {/if}
    </section>
  {:else}
    <p class="muted">No storage devices are known yet; file reconciliation has not completed.</p>
  {/each}

  <Card title="Kept" description={'Never planned for removal until taken off this list. Keep a title from its page, from the cleanup plan, or with the tag "overtide-keep" in Radarr or Sonarr; a torrent from its page.'}>
    {#if data.kept.length}
      <table id="kept">
        <tbody>
          {#each data.kept as k (k.key)}
            <tr>
              <td><b>{k.name}</b>{#if k.from}<br /><span class="muted">from {k.from}</span>{/if}</td>
              <td class="muted">{k.keptAt ? new Date(k.keptAt).toLocaleDateString('en-CA') : ''}</td>
              <td class="row-actions"><Busy kind="ghost" small action={() => unkeep(k.key)} label={`Unkeep ${k.name}`}>Unkeep</Busy></td>
            </tr>
          {/each}
        </tbody>
      </table>
    {:else}
      <p class="muted">Nothing is kept.</p>
    {/if}
  </Card>
{/if}

{#if settingsFor}
  <DeviceSettings path={settingsFor} saved={load} close={() => (settingsFor = null)} />
{/if}
{#if planFor}
  <CleanupPlan path={planFor} cleaned={load} close={() => (planFor = null)} />
{/if}
