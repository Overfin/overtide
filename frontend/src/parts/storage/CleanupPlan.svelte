<!-- A device's cleanup plan: what would be removed to reach its target,
     torrents first, then titles. A row can be skipped this time (another
     candidate takes its place) or kept (it goes on the Kept list). Clean
     removes the rows shown, after a confirmation; dry run only simulates. -->
<script lang="ts">
  import { untrack } from 'svelte';
  import { Card, Modal, Loading, Notice, Status, ask } from '@overfin/ui';
  import { get, post } from '../../lib/api';
  import '../../styles/adding.css';
  import { human, identity, type CleanupPlan, type CleanupRow } from '../../lib/types-storage';

  // inline: on a page (the Removal page), not in a window; close is then
  // not needed.
  let { path, cleaned, close = () => {}, inline = false, title = 'Cleanup plan' }: {
    path: string;
    cleaned: () => void;
    close?: () => void;
    inline?: boolean;
    title?: string;
  } = $props();

  let plan = $state<CleanupPlan | null>(null);
  let failure = $state('');
  let busy = $state(false);
  // excluded: the rows skipped so far in this window.
  let excluded = $state<string[]>([]);
  let oldScores = $state(false);

  const rows = $derived(plan ? [...plan.torrents, ...plan.media] : []);

  async function load() {
    const query = new URLSearchParams({ path });
    for (const key of excluded) query.append('excluded', key);
    try {
      plan = await get<CleanupPlan>(`/api/storage/plan?${query}`);
      excluded = plan.excluded;
      // Asked when the window opens: it stays true for the rows skipped after.
      oldScores = oldScores || !!plan.oldScores;
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
    if (inline) await load();
    else close();
  });

  // Loaded once when the window opens: load reads and sets excluded, which
  // would otherwise load it again, and again (each time reading Overfin's
  // scores).
  $effect(() => {
    untrack(load);
  });
</script>

{#snippet body()}
  {#if !plan}
    {#if failure}<Notice warn>{failure}</Notice>{:else}<Loading text="Asking Overfin for current scores…" />{/if}
  {:else if rows.length}
    {#if plan.dryRun}<Notice warn>Dry run — nothing will be removed.</Notice>{/if}
    {#if oldScores}<Notice warn>Overfin couldn't be reached: this plan uses the scores it last gave.</Notice>{/if}
    <p>{plan.message} <span class="muted">· {rows.length} to remove, {human(plan.selectedBytes)} in all</span></p>
    <p class="muted">Skip a row for this time: another takes its place, so the target is still met. Keep it: it goes on the Kept list, and is never planned for removal until taken off it.</p>
    {#each [['Torrents', plan.torrents], ['Titles', plan.media]] as [label, group] (label)}
      {#if (group as CleanupRow[]).length}
        <details class="card" open>
          <summary>{label} <span class="badge plain">{(group as CleanupRow[]).length}</span></summary>
          <div class="table-wrap">
            <table class="waiting-table">
              <thead><tr><th>Title</th><th>What</th><th><span class="visually-hidden">Actions</span></th></tr></thead>
              <tbody>
                {#each group as CleanupRow[] as row (row.key)}
                  <tr>
                    <td><span class="name">{row.title}</span></td>
                    <td class="muted">{row.meta}{#if row.with}<br />{row.with}{/if}</td>
                    <td class="actions nowrap">
                      <button type="button" class="small ghost" disabled={busy} aria-label={`Skip ${row.title} this time`} onclick={() => skip(row)}>Skip this time</button>
                      <button type="button" class="small" disabled={busy} aria-label={`Keep ${row.title}`} onclick={() => keep(row)}>Keep</button>
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </details>
      {/if}
    {/each}
  {:else}
    <p class="good">Nothing currently needs cleanup on this device.</p>
  {/if}
{/snippet}

{#snippet buttons()}
  <Status text={plan ? failure : ''} kind={failure ? 'error' : ''} />
  {#if !inline}<button type="button" disabled={busy} onclick={close}>Close</button>{/if}
  {#if rows.length}
    <button type="button" class={plan?.dryRun ? 'primary' : 'danger'} disabled={busy} onclick={clean}>{plan?.dryRun ? 'Simulate' : 'Clean'}</button>
  {/if}
{/snippet}

{#if inline}
  <Card {title} description={plan ? identity(plan) : ''}>
    {@render body()}
    <div class="plan-foot">{@render buttons()}</div>
  </Card>
{:else}
  <Modal title="Cleanup plan" description={plan ? identity(plan) : ''} size="wide" {busy} {close}>
    {@render body()}
    {#snippet foot()}{@render buttons()}{/snippet}
  </Modal>
{/if}
