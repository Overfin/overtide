<!-- Services: the apps Overtide works with, and whether it can reach them.
     /services?add=<category> opens the Add-service window at once
     (category: torrentclient, medialibrary, or any other for every type). -->
<script lang="ts">
  import { PageHead, Card, Badge, Loading, Notice } from '@overfin/ui';
  import { app, folders, states } from '../lib/words-services';
  import { get } from '../lib/api';
  import { follow } from '../lib/live.svelte';
  import { params, go, route } from '../lib/router.svelte';
  import { shortPath, updated } from '../lib/types-services';
  import type { ServiceRow } from '../lib/types-services';
  import AddService from '../parts/services/AddService.svelte';
  import EditService from '../parts/services/EditService.svelte';
  import ServiceProgress from '../parts/services/ServiceProgress.svelte';
  import Trackers from '../parts/services/Trackers.svelte';

  let services = $state<ServiceRow[] | null>(null);
  let failure = $state('');
  // adding: the category of the open Add-service window, null when closed.
  let adding = $state<string | null>(null);
  let editing = $state<string | null>(null);
  let progress = $state(false);

  async function load() {
    try {
      ({ services } = await get<{ services: ServiceRow[] }>('/api/services'));
      failure = '';
    } catch (err) {
      failure = (err as Error).message;
    }
  }

  $effect(() => {
    load();
    return follow('immediate', load);
  });

  // ?add= opens the window, then leaves the address.
  $effect(() => {
    const add = params().get('add');
    if (add === null) return;
    adding = add;
    go(route.path, true);
  });

  function added() {
    adding = null;
    progress = true;
    load();
  }
</script>

<PageHead title="Services" description="The apps Overtide works with, and how each one says it is doing: its version, and the problems it reports.">
  {#snippet actions()}<button type="button" class="primary" onclick={() => (adding = '')}>Add service</button>{/snippet}
</PageHead>

{#if failure}<Notice warn>{failure}</Notice>{/if}

{#if !services}
  {#if !failure}<Loading />{/if}
{:else if !services.length}
  <p class="muted">No services are configured yet.</p>
{:else}
  <div class="cards">
    {#each services as s (s.id)}
      <Card title={s.name}>
        {#snippet badge()}<Badge kind={states[s.state]?.kind ?? 'err'}>{states[s.state]?.label ?? s.state}</Badge>{/snippet}
        <div><b>{app(s.type, s.version)}</b></div>
        {#if s.problems?.length}
          <ul class="list">{#each s.problems as p, i (i)}<li class="hint">{p}</li>{/each}</ul>
        {:else if s.state !== 'running' && s.message}<div class="hint">{s.message}</div>{/if}
        {#if s.checkedAt}<div class="hint">Checked {updated(s.checkedAt)}</div>{/if}
        {#if s.roots.length}
          <div class="label spaced">{folders(s.type)}</div>
          <ul class="list">
            {#each s.roots as root (root)}<li><div class="mono" title={root}>{shortPath(root)}</div></li>{/each}
          </ul>
        {/if}
        {#snippet foot()}
          <span class="grow"></span>
          <button type="button" class="small" aria-label={`Edit ${s.name}`} onclick={() => (editing = s.id)}>Edit</button>
        {/snippet}
      </Card>
    {/each}
  </div>
  <Trackers />
{/if}

{#if adding !== null}
  <AddService category={adding} saved={added} close={() => (adding = null)} />
{/if}
{#if editing !== null}
  <EditService id={editing} saved={() => { editing = null; load(); }} removed={() => { editing = null; load(); }} close={() => (editing = null)} />
{/if}
{#if progress}
  <ServiceProgress close={() => (progress = false)} />
{/if}
