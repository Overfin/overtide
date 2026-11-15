<!-- A device's settings window: its name, whether automatic removal looks
     at it, and its thresholds. They apply to the whole physical device:
     every root grouped under it, not one path. Then its root folders: how
     they share its space up to the target, and each one's daily limits. -->
<script lang="ts">
  import { FormModal, Field, Loading, Notice } from '@overfin/ui';
  import { get, put } from '../../lib/api';
  import { identity, type DeviceSettings, type FolderSettings } from '../../lib/types-storage';
  import ShareBar from './ShareBar.svelte';

  let { path, saved, close }: { path: string; saved: () => void; close: () => void } = $props();

  let settings = $state<DeviceSettings | null>(null);
  let failure = $state('');
  let name = $state('');
  let enabled = $state(true);
  let target = $state<number | string>(80);
  let critical = $state<number | string>(90);
  let folders = $state<FolderSettings[]>([]);
  let sharing = $state(false);
  let shares = $state<number[]>([]);

  // Shares when sharing starts: what each folder holds now, so none is
  // over its share at first; equal parts when they hold nothing yet.
  function startShares(): number[] {
    const total = folders.reduce((a, f) => a + f.bytes, 0);
    const out = folders.map((f) => (total > 0 ? Math.round((f.bytes / total) * 100) : Math.floor(100 / folders.length)));
    const gap = 100 - out.reduce((a, b) => a + b, 0);
    out[out.indexOf(Math.max(...out))] += gap;
    return out;
  }
  function toggleSharing() {
    if (sharing) shares = startShares();
  }
  // The space the shares split: the disk's up to the target.
  const shared = $derived(settings ? (settings.usableBytes * (Number(target) || 0)) / 100 : 0);

  $effect(() => {
    get<DeviceSettings>(`/api/storage/device?path=${encodeURIComponent(path)}`).then(
      (s) => {
        settings = s;
        name = s.name;
        enabled = s.automaticRemovalEnabled;
        target = Math.round(s.targetUsagePercent);
        critical = Math.round(s.criticalUsagePercent);
        folders = s.folders;
        sharing = s.folders.some((f) => f.sharePercent > 0);
        shares = sharing ? s.folders.map((f) => f.sharePercent) : [];
      },
      (err) => (failure = err.message),
    );
  });

  async function save() {
    // An empty or not-a-number field is refused here, as the server did.
    const t = Number(target), c = Number(critical);
    if (String(target).trim() === '' || !Number.isFinite(t)) throw new Error('target_usage_percent must be a number');
    if (String(critical).trim() === '' || !Number.isFinite(c)) throw new Error('critical_usage_percent must be a number');
    const sent = folders.map((f, i) => {
      const titles = Number(f.titlesPerDay), upgrades = Number(f.upgradesPerDay);
      if (!Number.isInteger(titles) || titles < 1) throw new Error(`Titles a day in ${f.path} must be 1 or more`);
      if (!Number.isInteger(upgrades) || upgrades < 0) throw new Error(`Upgrades a day in ${f.path} must be 0 or more`);
      return { path: f.path, sharePercent: sharing ? shares[i] : 0, titlesPerDay: titles, upgradesPerDay: upgrades };
    });
    await put('/api/storage/device', { path, name, targetUsagePercent: t, criticalUsagePercent: c, automaticRemovalEnabled: enabled, folders: sent });
    saved();
  }
</script>

<FormModal title="Device settings" description={settings ? identity(settings) : ''} {save} {close}>
  {#if failure}
    <Notice warn>{failure}</Notice>
  {:else if !settings}
    <Loading />
  {:else}
    <p class="muted">These thresholds apply to the whole physical device identified above, including every root path grouped under it — not to one path specifically.</p>
    <Field label="Name"><input bind:value={name} placeholder={settings.rootLabels.length ? settings.rootLabels.join(', ') : settings.path} /></Field>
    <label class="inline"><input type="checkbox" bind:checked={enabled} /> Enable on this device</label>
    {#if enabled}
      <Field label="Target %"><input type="number" min="1" max="99" step="1" bind:value={target} /></Field>
    {/if}
    <Field label="Critical %" hint="Removal steers toward the target. Past critical, adding adds nothing more there until removal catches up. Must be above the target.">
      <input type="number" min="2" max="99" step="1" bind:value={critical} />
    </Field>
    {#if folders.length}
      <h3>Root folders</h3>
      {#if folders.length > 1}
        <label class="inline"><input type="checkbox" bind:checked={sharing} onchange={toggleSharing} /> Share this disk between its root folders</label>
        {#if sharing}
          <p class="muted">Each root folder gets a part of the disk's space up to the target. One over its part gets nothing added and no upgrades. When the disk needs room, removal takes from the folders over their part, one title at a time, the furthest over first.</p>
          <ShareBar {folders} bind:shares capacity={shared} />
        {/if}
      {/if}
      <table class="folder-limits">
        <thead><tr><th scope="col">Root folder</th><th scope="col">Titles a day</th><th scope="col">Upgrades a day</th></tr></thead>
        <tbody>
          {#each folders as folder (folder.path)}
            <tr>
              <th scope="row">{folder.path}</th>
              <td><input type="number" min="1" max="500" bind:value={folder.titlesPerDay} aria-label="Titles a day in {folder.path}" /></td>
              <td><input type="number" min="0" max="500" bind:value={folder.upgradesPerDay} aria-label="Upgrades a day in {folder.path}" /></td>
            </tr>
          {/each}
        </tbody>
      </table>
      <p class="muted">Titles a day: how many titles may start downloading there in 24 hours, a series counting once per season; past it, they wait. Upgrades a day: how many searches for a better copy may go for what's there in 24 hours; 0 is none.</p>
    {/if}
  {/if}
</FormModal>
