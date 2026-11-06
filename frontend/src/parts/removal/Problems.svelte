<!-- The removals that didn't go through in full and still have something
     left: each leftover removed again through the removal window (its plan
     made anew), or the operation dismissed. -->
<script lang="ts">
  import { Card, Notice, Busy, Empty, Loading, toast } from '@overfin/ui';
  import { get, post } from '../../lib/api';
  import { follow } from '../../lib/live.svelte';
  import { openRemoval } from '../../lib/removal.svelte';
  import { bytes } from '../../lib/format';
  import { stamp } from '../../lib/types-library';

  interface Leftover {
    kind: 'torrent' | 'media' | 'unmanaged';
    label: string;
    hash?: string;
    serviceId?: string;
    mediaType?: string;
    mediaId?: string;
    paths?: string[];
    bytes: number;
  }
  interface Problem {
    id: number;
    createdAt?: string;
    label: string;
    status: string;
    error?: string;
    leftovers: Leftover[];
    unknown?: boolean;
  }

  let problems = $state<Problem[] | null>(null);
  let failure = $state('');

  async function load() {
    try {
      problems = (await get<{ problems: Problem[] }>('/api/history/problems')).problems;
      failure = '';
    } catch (err) {
      failure = (err as Error).message;
    }
  }
  load();
  $effect(() => follow('stable-list', load));

  const kinds = { torrent: 'Torrent', media: 'Title', unmanaged: 'Files' };

  function removeAgain(l: Leftover) {
    if (l.kind === 'torrent') openRemoval('torrent', { hash: l.hash ?? '', service_id: l.serviceId ?? '' });
    else if (l.kind === 'media') openRemoval('media', { type: l.mediaType ?? '', id: l.mediaId ?? '', service_id: l.serviceId ?? '' });
    else openRemoval('unmanaged', { path: l.paths ?? [] });
  }

  async function dismiss(p: Problem) {
    await post(`/api/history/${p.id}/dismiss`);
    toast('Dismissed.');
    await load();
  }
</script>

{#if failure}<Notice warn>{failure}</Notice>{/if}
{#if !problems}
  <Loading />
{:else}
  {#each problems as p (p.id)}
    <Card title={p.label}>
      <p class="muted">{stamp(p.createdAt)} · {p.status}</p>
      {#if p.error}<p class="error problem-error">{p.error}</p>{/if}
      {#if p.unknown}
        <p>What it planned wasn't recorded, so what's left can't be said: check by hand, then dismiss it.</p>
      {:else}
        <table>
          <thead><tr><th>Still there</th><th>Kind</th><th class="num">Size</th><th><span class="visually-hidden">Actions</span></th></tr></thead>
          <tbody>
            {#each p.leftovers as l, i (i)}
              <tr>
                <td class="problem-name">{l.label}</td>
                <td>{kinds[l.kind]}</td>
                <td class="num">{bytes(l.bytes)}</td>
                <td class="num"><button type="button" class="small danger" onclick={() => removeAgain(l)}>Remove again</button></td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
      {#snippet foot()}
        <span class="grow"></span>
        <Busy action={() => dismiss(p)}>Dismiss</Busy>
      {/snippet}
    </Card>
  {:else}
    <Empty>No failed removal has anything left.</Empty>
  {/each}
{/if}

<style>
  .problem-error,
  .problem-name {
    overflow-wrap: anywhere;
  }
</style>
