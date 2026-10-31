<!-- After a service is saved: where the setup scan stands (inventory, then
     files), asked again on every live update. It runs in the background:
     closing this window doesn't stop it. -->
<script lang="ts">
  import { Modal } from '@overfin/ui';
  import { get } from '../../lib/api';
  import { follow } from '../../lib/live.svelte';
  import type { ConsistencyStatus } from '../../lib/types-services';

  let { close }: { close: () => void } = $props();

  let label = $state('Starting…');
  let finished = $state(false);

  async function refresh() {
    try {
      const status = await get<ConsistencyStatus>('/api/services/consistency');
      finished = status.state === 'succeeded' || status.state === 'attention';
      label = finished ? 'Done.' : `Step ${status.currentStep + 1} of ${status.totalSteps}: ${status.stepLabel || 'Working…'}`;
    } catch {
      // A failed status poll leaves the last-known label in place; the
      // next revision tries again.
    }
  }

  $effect(() => {
    refresh();
    return follow('immediate', refresh);
  });
</script>

<Modal title="Setting up your service" description={label} {close}>
  {#if !finished}<progress class="bar" aria-label="Setting up your service"></progress>{/if}
  <p class="hint">This runs in the background — you can close this and keep using Overtide while it finishes.</p>
  {#snippet foot()}
    <span class="grow"></span>
    <button type="button" class="primary" onclick={close}>Close</button>
  {/snippet}
</Modal>
