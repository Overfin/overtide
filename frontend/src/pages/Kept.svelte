<!-- Removing, kept: what's never planned for removal until taken off this
     list. -->
<script lang="ts">
  import '../styles/adding.css';
  import { Busy, Empty, Loading, Notice, PageHead, toast } from '@overfin/ui';
  import { get, post } from '../lib/api';
  import type { Storage } from '../lib/types-storage';
  import { day } from '../lib/types-library';

  let data = $state<Storage | null>(null);
  let failure = $state('');
  let q = $state('');

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

  async function unkeep(key: string, name: string) {
    await post('/api/storage/keep', { key, unkeep: true });
    toast(`${name} may be removed again.`);
    await load();
  }

  const shown = $derived(data ? data.kept.filter((k) => k.name.toLowerCase().includes(q.trim().toLowerCase())) : []);
</script>

<PageHead title="Kept" description={'Never planned for removal until taken off this list. Keep a title from the plan, from its page, or with the tag "overtide-keep" in Radarr or Sonarr; a torrent from its page.'} />

{#if failure}<Notice warn>{failure}</Notice>{/if}

{#if !data}
  <Loading />
{:else}
  <div class="toolbar">
    <label class="field"><span>Search</span><input type="search" bind:value={q} placeholder="Title…" /></label>
  </div>
  {#if shown.length}
    <details class="card" open>
      <summary>Kept <span class="badge plain">{shown.length}</span></summary>
      <div class="table-wrap">
        <table class="waiting-table">
          <thead><tr><th>Title</th><th class="hide-narrow">From</th><th>Kept since</th><th><span class="visually-hidden">Actions</span></th></tr></thead>
          <tbody>
            {#each shown as k (k.key)}
              <tr>
                <td><span class="name">{k.name}</span></td>
                <td class="muted hide-narrow">{k.from ?? ''}</td>
                <td class="muted nowrap">{day(k.keptAt)}</td>
                <td class="actions"><Busy small action={() => unkeep(k.key, k.name)} label={`Stop keeping ${k.name}`}>Stop keeping</Busy></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </details>
  {:else}
    <Empty>{q.trim() ? 'Nothing matches.' : 'Nothing is kept.'}</Empty>
  {/if}
{/if}
