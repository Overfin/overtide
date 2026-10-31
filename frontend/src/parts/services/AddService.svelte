<!-- Adding a service, in a window: its type, name, address and
     credentials; the connection is tested first, and only then can it be
     saved. Once saved, the setup progress window opens (saved). category
     narrows the types (torrentclient, medialibrary), as the Torrents and
     Library pages' empty states ask. -->
<script lang="ts">
  import { Modal, Field, Status } from '@overfin/ui';
  import { get, post } from '../../lib/api';
  import type { ServiceCategory } from '../../lib/types-services';

  let { category = '', saved, close }: { category?: string; saved: () => void; close: () => void } = $props();

  let choices = $state<ServiceCategory | null>(null);
  let type = $state('');
  let name = $state('');
  let url = $state('');
  let apiKey = $state('');
  let username = $state('');
  let password = $state('');
  let allowAutomaticRemoval = $state(false);
  let verified = $state(false);
  let busy = $state(false);
  let testing = $state(false);
  let failure = $state('');

  const shows = (types: string) => types.split(' ').includes(type);

  $effect(() => {
    get<ServiceCategory>(`/api/services/types${category ? `?category=${encodeURIComponent(category)}` : ''}`).then(
      (c) => {
        choices = c;
        type = c.types[0]?.value ?? '';
      },
      (err) => (failure = err.message),
    );
  });

  // A change to what's tested asks for a new test.
  $effect(() => {
    void [type, url, apiKey, username, password];
    verified = false;
  });

  const body = () => ({ type, name, url, apiKey, username, password, allowAutomaticRemoval });

  async function test() {
    failure = '';
    testing = true;
    try {
      await post('/api/services/test', body());
      verified = true;
    } catch (err) {
      failure = (err as Error).message;
    } finally {
      testing = false;
    }
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (!verified) {
      await test();
      return;
    }
    failure = '';
    busy = true;
    try {
      await post('/api/services', body());
      saved();
    } catch (err) {
      failure = (err as Error).message;
    } finally {
      busy = false;
    }
  }
</script>

<Modal title={choices?.title ?? 'Add service'} description={verified ? 'Connection verified. Save to finish.' : 'Overtide checks the connection before showing the rest.'} busy={busy || testing} {close}>
  <form id="add-service" class="modal-form" onsubmit={submit}>
    <Field label="Type">
      <select bind:value={type} required>
        {#each choices?.types ?? [] as t (t.value)}<option value={t.value}>{t.label}</option>{/each}
      </select>
    </Field>
    {#if shows('overfin')}
      <p class="hint">Overfin gives each film and season its value, from what people watch, ask for and like; Overtide uses it as it is. Without Overfin, nothing is valued, so nothing is removed automatically. Use Overfin's control panel address and a key made there under Apps, with Values ticked. It never owns removable storage.</p>
    {/if}
    <Field label="Name"><input bind:value={name} required /></Field>
    <Field label="URL"><input type="url" bind:value={url} required /></Field>
    {#if shows('radarr sonarr overfin')}
      <Field label="API key"><input type="password" bind:value={apiKey} autocomplete="off" /></Field>
    {/if}
    {#if shows('qbittorrent')}
      <Field label="Username"><input bind:value={username} autocomplete="off" /></Field>
      <Field label="Password"><input type="password" bind:value={password} autocomplete="off" /></Field>
    {/if}
    {#if shows('radarr sonarr qbittorrent')}
      <label class="inline"><input type="checkbox" bind:checked={allowAutomaticRemoval} /> Allow automatic removal</label>
      <p class="hint">Lets unattended cleanup remove what this service owns, once automatic removal is enabled in Settings.</p>
    {/if}
  </form>
  {#snippet foot()}
    <Status text={failure} kind={failure ? 'error' : ''} />
    <button type="button" disabled={busy || testing} onclick={close}>Cancel</button>
    {#if verified}
      <button type="submit" form="add-service" class="primary" disabled={busy}>{choices?.title ?? 'Add service'}</button>
    {:else}
      <button type="button" class="primary" disabled={testing} onclick={test}>{testing ? 'Testing…' : 'Test connection'}</button>
    {/if}
  {/snippet}
</Modal>
