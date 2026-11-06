<!-- Says which episode each waiting file of a download is. Each starts on
     what its number says; "Leave waiting" leaves it out. -->
<script lang="ts">
  import { FormModal, Field, Notice, Loading } from '@overfin/ui';
  import { get, post } from '../../lib/api';
  import type { ImportOptions, WaitingImport } from '../../lib/types-adding';

  let { item, saved, close }: { item: WaitingImport; saved: () => void; close: () => void } = $props();

  let options = $state<ImportOptions | null>(null);
  let failure = $state('');
  let chosen = $state<Record<string, string>>({});

  const address = $derived(`/api/adding/imports/${encodeURIComponent(item.serviceId)}/${encodeURIComponent(item.downloadId)}`);
  const fileName = (path: string) => path.slice(path.lastIndexOf('/') + 1);
  const pad = (n: number) => String(n).padStart(2, '0');

  $effect(() => {
    get<ImportOptions>(address).then(
      (o) => {
        options = o;
        for (const file of item.files) {
          const guess = o.guesses[file.path];
          // A file of several episodes can only be left as it is, or chosen as one.
          chosen[file.path] = guess?.length === 1 ? String(guess[0]) : '';
        }
      },
      (err) => (failure = err.message),
    );
  });

  async function save() {
    const files: Record<string, number[]> = {};
    for (const [path, id] of Object.entries(chosen)) {
      if (id) files[path] = [Number(id)];
    }
    await post(address, { action: 'episodes', files });
    saved();
  }
</script>

<FormModal title="Choose episodes" {save} {close}>
  {#if failure}
    <Notice warn>{failure}</Notice>
  {:else if !options}
    <Loading />
  {:else}
    <p class="muted">Each file starts on what its number says, which may be wrong. Files on "Leave waiting" stay as they are.</p>
    {#each item.files as file (file.path)}
      <Field label={fileName(file.path)}>
        <select bind:value={chosen[file.path]}>
          <option value="">Leave waiting</option>
          {#each options.episodes as e (e.id)}
            <option value={String(e.id)}>S{pad(e.season)}E{pad(e.episode)} · {e.title}</option>
          {/each}
        </select>
      </Field>
    {/each}
  {/if}
</FormModal>
