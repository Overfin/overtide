<!-- A service's edit window: its name, address and credentials (a saved
     password or key is never shown: left empty, it stays), "Allow automatic
     removal", and removing the service. saved and removed close it. -->
<script lang="ts">
  import { FormModal, Field, Notice, Loading, ask, toast } from '@overfin/ui';
  import { get, put, del } from '../../lib/api';
  import type { ServiceDetail } from '../../lib/types-services';

  let { id, saved, removed, close }: { id: string; saved: () => void; removed: () => void; close: () => void } = $props();

  let detail = $state<ServiceDetail | null>(null);
  let failure = $state('');
  let name = $state('');
  let url = $state('');
  let apiKey = $state('');
  let username = $state('');
  let password = $state('');
  let allowAutomaticRemoval = $state(false);

  $effect(() => {
    get<ServiceDetail>(`/api/services/${encodeURIComponent(id)}`).then(
      (d) => {
        detail = d;
        ({ name, url, username, allowAutomaticRemoval } = d);
      },
      (err) => (failure = err.message),
    );
  });

  const qbittorrent = $derived(detail?.type === 'qbittorrent');
  const removable = $derived(['radarr', 'sonarr', 'qbittorrent'].includes(detail?.type ?? ''));

  async function save() {
    if (!detail) return;
    await put(`/api/services/${encodeURIComponent(id)}`, { name, url, apiKey, username, password, allowAutomaticRemoval });
    saved();
  }

  async function remove() {
    if (!detail) return;
    if (!(await ask(`Remove ${detail.name}? Everything it owns becomes Unmanaged.`, { title: 'Remove service', yes: 'Remove service', danger: true }))) return;
    try {
      await del(`/api/services/${encodeURIComponent(id)}`);
      toast(`${detail.name} removed.`);
      removed();
    } catch (err) {
      toast((err as Error).message, true);
    }
  }
</script>

<FormModal title="Edit service" description="Overtide checks the connection before saving it. Moving it to a new URL keeps everything it already owns." saveLabel="Save changes" {save} {close}>
  {#if failure}
    <Notice warn>{failure}</Notice>
  {:else if !detail}
    <Loading />
  {:else}
    <Field label="Type"><input value={detail.type} disabled /></Field>
    <Field label="Name"><input bind:value={name} required /></Field>
    <Field label="URL"><input type="url" bind:value={url} required /></Field>
    {#if qbittorrent}
      <Field label="Username"><input bind:value={username} autocomplete="off" /></Field>
      <Field label="Password"><input type="password" bind:value={password} placeholder={detail.hasPassword ? 'Unchanged; type to replace' : ''} autocomplete="new-password" /></Field>
    {:else}
      <Field label="API key"><input type="password" bind:value={apiKey} placeholder={detail.hasApiKey ? 'Unchanged; type to replace' : ''} autocomplete="new-password" /></Field>
    {/if}
    {#if removable}
      <label class="inline"><input type="checkbox" bind:checked={allowAutomaticRemoval} /> Allow automatic removal</label>
      <p class="hint">Lets unattended cleanup remove what this service owns, once automatic removal is enabled in Settings.</p>
    {/if}
    <div class="danger-zone">
      <p class="hint">Removing this service lets go of everything it currently owns — those files become Unmanaged.</p>
      <button type="button" class="danger" onclick={remove}>Remove service</button>
    </div>
  {/if}
</FormModal>
