<!-- Removing, plan: each disk's cleanup plan, on the page: what would be removed to
     reach its target, each row skippable for this time or kept for good. -->
<script lang="ts">
  import '../styles/storage.css';
  import { Empty, Loading, Notice, PageHead } from '@overfin/ui';
  import { get } from '../lib/api';
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
  $effect(() => {
    load();
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
    <CleanupPlan inline path={d.path} title={d.path} cleaned={load} />
  {/each}
{:else}
  <Empty>No disk has removal on: turn it on for a disk in Storage, in its settings (🔧).</Empty>
{/if}
