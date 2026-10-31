<!-- A device's cleanup plan: what would be removed to reach its target,
     torrents first, then titles. A row can be skipped this time (another
     candidate takes its place) or kept (it goes on the Kept list). Clean
     removes the rows shown, after a confirmation; dry run only simulates. -->
<script lang="ts">
  import { Modal, Loading, Notice, Status, ask } from '@overfin/ui';
  import { get, post } from '../../lib/api';
  import { human, identity, type CleanupPlan, type CleanupRow } from '../../lib/types-storage';

  let { path, cleaned, close }: { path: string; cleaned: () => void; close: () => void } = $props();

  let plan = $state<CleanupPlan | null>(null);
  let failure = $state('');
  let busy = $state(false);
  // excluded: the rows skipped so far in this window.
  let excluded = $state<string[]>([]);

  const rows = $derived(plan ? [...plan.torrents, ...plan.media] : []);

  async function load() {
    const query = new URLSearchParams({ path });
    for (const key of excluded) query.append('excluded', key);
    try {
      plan = await get<CleanupPlan>(`/api/storage/plan?${query}`);
      excluded = plan.excluded;
    } catch (err) {
      failure = (err as Error).message;
    }
  }

  async function act(action: () => Promise<void>) {
    busy = true;
    failure = '';
    try {
      await action();
    } catch (err) {
      failure = (err as Error).message;
    } finally {
      busy = false;
    }
  }

  // Skip for this run: the plan is built again without it, and another
  // candidate takes its place so the target is still met.
  const skip = (row: CleanupRow) => act(async () => {
    excluded = [...excluded, row.key];
    await load();
  });

  // Keep: on the Kept list until taken off it; the plan is built again.
  const keep = (row: CleanupRow) => act(async () => {
    await post('/api/storage/plan/protect', { path, excluded, key: row.key });
    await load();
  });

  const clean = () => act(async () => {
    const dry = plan!.dryRun;
    const sure = await ask(
      dry ? `Simulate removing the ${rows.length} item(s) listed? Dry run is on: nothing will be removed.` : `Remove the ${rows.length} item(s) listed, with their files?`,
      { title: dry ? 'Simulate cleanup' : 'Clean up', yes: dry ? 'Simulate' : 'Clean', danger: !dry },
    );
    if (!sure) return;
    await post('/api/storage/plan/clean', { path, excluded, shown: rows.map((r) => r.shown) });
    cleaned();
    close();
  });

  $effect(() => {
    load();
  });
</script>

<Modal title="Cleanup plan" description={plan ? identity(plan) : ''} size="wide" {busy} {close}>
  {#if plan?.dryRun}<p><span class="badge warn">DRY RUN</span></p>{/if}
  {#if !plan}
    {#if failure}<Notice warn>{failure}</Notice>{:else}<Loading />{/if}
  {:else if rows.length}
    {#if plan.dryRun}<Notice warn>Dry run — nothing will be removed.</Notice>{/if}
    <p class="bad"><b>Cleanup active.</b><br />{plan.message}<br />{rows.length} action(s) selected · {human(plan.selectedBytes)} planned.</p>
    <p class="muted">Skip anything you don't want cleaned this time (➖ — a different candidate immediately takes its place so the target is still met), or keep it (🛡️): it goes on the Kept list on the Storage page and stops being a candidate until you take it off.</p>
    {#each [['Torrents', plan.torrents], ['Media', plan.media]] as [label, group] (label)}
      {#if (group as CleanupRow[]).length}
        <div class="cleanup-group">
          <h4>{label} <span class="badge plain">{(group as CleanupRow[]).length}</span></h4>
          <ul class="cleanup-actions">
            {#each group as CleanupRow[] as row (row.key)}
              <li>
                <div class="cleanup-row-body">
                  <div class="row-title">{row.title}</div>
                  <div class="row-meta">{row.meta}</div>
                  {#if row.with}<div class="row-meta">{row.with}</div>{/if}
                </div>
                <div class="cleanup-row-actions">
                  <button type="button" class="small ghost" disabled={busy} title="Skip for this run" aria-label={`Skip ${row.title} for this run`} onclick={() => skip(row)}>➖</button>
                  <button type="button" class="small ghost" disabled={busy} title="Keep" aria-label={`Keep ${row.title}`} onclick={() => keep(row)}>🛡️</button>
                </div>
              </li>
            {/each}
          </ul>
        </div>
      {/if}
    {/each}
  {:else}
    <p class="good">Nothing currently needs cleanup on this device.</p>
  {/if}
  {#snippet foot()}
    <Status text={plan ? failure : ''} kind={failure ? 'error' : ''} />
    <button type="button" disabled={busy} onclick={close}>Close</button>
    {#if rows.length}
      <button type="button" class={plan?.dryRun ? 'primary' : 'danger'} disabled={busy} onclick={clean}>{plan?.dryRun ? 'Simulate' : 'Clean'}</button>
    {/if}
  {/snippet}
</Modal>
