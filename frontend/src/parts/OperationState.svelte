<!-- A removal shown where what it removes was (a library title, a torrent):
     queued, running, done or failed, with where to read about it and where
     to go back. -->
<script lang="ts">
  import { Card, Badge } from '@overfin/ui';

  export interface Operation {
    id: number;
    label: string;
    status: string;
    message?: string;
  }

  let { notice, back, backLabel }: { notice: Operation; back: string; backLabel: string } = $props();
  const kind = $derived(notice.status === 'failed' ? 'err' : notice.status === 'partial' ? 'warn' : notice.status === 'success' || notice.status === 'dry_run' ? 'ok' : 'info');
</script>

<Card title={notice.label || 'Removal operation'}>
  {#snippet badge()}<Badge {kind}>{notice.status}</Badge>{/snippet}
  {#if notice.status === 'queued'}
    <p>Removal is queued. Overtide owns this operation now; closing this page will not interrupt it.</p>
  {:else if notice.status === 'started'}
    <p>Removal is in progress. Overtide will reconcile inventory and storage when it completes.</p>
  {:else if notice.status === 'success'}
    <p>Removal completed successfully. Overtide has reconciled this view with the operation result.</p>
  {:else if notice.status === 'dry_run'}
    <p>The removal simulation completed. No owner or filesystem changes were made.</p>
  {:else if notice.status === 'partial'}
    <p class="dry">Removal completed only partially. Review the result before taking another action.</p>
  {:else if notice.status === 'failed'}
    <p class="error">Removal failed. No successful removal is hidden from this view.</p>
  {:else}
    <p>This removal needs attention while Overtide restores a consistent view.</p>
  {/if}
  {#if notice.message}<p class="error">{notice.message}</p>{/if}
  <p><a href={`/history#operation-${notice.id}`}>View operation in History →</a></p>
  <p><a href={back}>Back to {backLabel} →</a></p>
</Card>
