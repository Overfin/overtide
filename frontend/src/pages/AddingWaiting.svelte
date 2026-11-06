<!-- Adding, waiting: the titles in line, those not out yet, and those
     imported. -->
<script lang="ts">
  import { PageHead, Busy, ask, toast } from '@overfin/ui';
  import { post } from '../lib/api';
  import type { Adding } from '../lib/types-adding';
  import AddingGroups from '../parts/adding/AddingGroups.svelte';

  let data = $state<Adding | null>(null);
  let reload = $state(0);

  async function clear() {
    const sure = await ask(
      "Take every waiting title out of the list, except wishes and the titles taken from Radarr? Overfin's suggestions come back at its next reading, in its order. Titles downloading, imported or monitored for their release stay.",
      { title: 'Clear the waiting list?', yes: 'Clear', danger: true },
    );
    if (!sure) return;
    await post('/api/adding/clear');
    toast('The waiting list is cleared.');
    reload++;
  }
</script>

<PageHead title="Waiting" description="The titles in line to be tried, in their order: wishes, then the films taken from Radarr, then Overfin's suggestions. Then those not out yet, and those imported.">
  {#snippet actions()}
    {#if data?.total}<Busy kind="danger" action={clear}>Clear the waiting list</Busy>{/if}
  {/snippet}
</PageHead>

<AddingGroups states={['waiting', 'not released', 'imported']} sourceFilter bind:data {reload} />
