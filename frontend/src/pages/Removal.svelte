<!-- Removing, plan: each disk's cleanup plan, on the page: what would be removed to
     reach its target, each row skippable for this time or kept for good. -->
<script lang="ts">
  import '../styles/storage.css';
  import { Empty, Loading, Notice, PageHead } from '@overfin/ui';
  import { get } from '../lib/api';
  import { follow } from '../lib/live.svelte';
  import type { Storage } from '../lib/types-storage';
  import CleanupPlan from '../parts/storage/CleanupPlan.svelte';

  let data = $state<Storage | null>(null);
  let failure = $state('');

  async function load() {
    try {
      data = await get<Storage>('/api/storage');
      failure = '';
    } catch (err) {
      failure = (err as Error).message;
    }
  }
  // Live: on each update, the disks' stamp is asked (cheap), and the disks
  // only when it changed; each plan follows on its own (CleanupPlan). The
  // disks' bytes ticking changes neither.
  let checking = false;
  async function check() {
    if (!data || checking) return;
    checking = true;
    try {
      const now = await get<{ stamp: string }>('/api/storage/disks/stamp');
      if (data && now.stamp !== data.disksStamp) await load();
    } catch {
      // The next update asks again.
    } finally {
      checking = false;
    }
  }
  $effect(() => {
    load();
    return follow('immediate', check, () => {});
  });

  const devices = $derived(data ? data.devices.filter((d) => d.enabled) : []);
</script>

<PageHead title="Plan" description="What Overtide would remove to bring each disk back to its target: torrents first, then titles. Skip a row this time (another takes its place), or keep it: it goes on the Kept list." />

{#if failure}<Notice warn>{failure}</Notice>{/if}

{#if !data}
  <Loading />
{:else if data.autoRemovalDisabled}
  <Notice>Automatic removal is off: turn it on in <a href="/settings">Settings</a> to see what it would remove.</Notice>
{:else if devices.length}
  {#each devices as d (d.path)}
    <CleanupPlan inline path={d.path} title={d.name || d.path} cleaned={load} />
  {/each}
{:else}
  <Empty>No disk has removal on: turn it on for a disk in Storage, in its settings (🔧).</Empty>
{/if}
