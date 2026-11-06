<!-- Where titles go: one of Overfin's categories (or every title of a
     kind), a Radarr's or Sonarr's root folder and quality profile. -->
<script lang="ts">
  import { FormModal, Field, Notice, Loading } from '@overfin/ui';
  import { get, post, put } from '../../lib/api';
  import type { DestinationOptions } from '../../lib/types-adding';

  let { index, saved, close }: { index: number | null; saved: () => void; close: () => void } = $props();

  let options = $state<DestinationOptions | null>(null);
  let failure = $state('');
  let category = $state('');
  let folder = $state('');
  let profile = $state('');

  $effect(() => {
    get<DestinationOptions>(`/api/destinations/options${index === null ? '' : `?index=${index}`}`).then(
      (o) => {
        options = o;
        category = o.current?.category ?? '';
        folder = o.current?.folder ?? o.folders[0]?.value ?? '';
        profile = o.current?.profile ?? o.profiles[0]?.value ?? '';
      },
      (err) => (failure = err.message),
    );
  });

  async function save() {
    const body = { category, folder, profile };
    if (index === null) await post('/api/destinations', body);
    else await put(`/api/destinations/${index}`, body);
    saved();
  }
</script>

<FormModal title={index === null ? 'Add a destination' : 'Edit a destination'} {save} {close}>
  {#if failure}
    <Notice warn>{failure}</Notice>
  {:else if !options}
    <Loading />
  {:else}
    {#each options.unreachable as name (name)}<Notice warn>{name} couldn't be asked for its folders and profiles.</Notice>{/each}
    <Field label="For" hint={'One of Overfin\'s categories, or every title of the kind the service takes (films in a Radarr, series in a Sonarr). A title goes to the first destination of a category it fits, else to its kind\'s "every title".'}>
      <select bind:value={category}>{#each options.categories as c (c.value)}<option value={c.value}>{c.label}</option>{/each}</select>
    </Field>
    <Field label="Root folder">
      <select bind:value={folder} required>{#each options.folders as c (c.value)}<option value={c.value}>{c.label}</option>{/each}</select>
    </Field>
    <Field label="Quality profile" hint="Of the same Radarr or Sonarr as the folder.">
      <select bind:value={profile} required>{#each options.profiles as c (c.value)}<option value={c.value}>{c.label}</option>{/each}</select>
    </Field>
  {/if}
</FormModal>
