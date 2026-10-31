<!-- A device's settings window: its name, whether automatic removal looks
     at it, and its thresholds. They apply to the whole physical device:
     every root grouped under it, not one path. -->
<script lang="ts">
  import { FormModal, Field, Loading, Notice } from '@overfin/ui';
  import { get, put } from '../../lib/api';
  import { identity, type DeviceSettings } from '../../lib/types-storage';

  let { path, saved, close }: { path: string; saved: () => void; close: () => void } = $props();

  let settings = $state<DeviceSettings | null>(null);
  let failure = $state('');
  let name = $state('');
  let enabled = $state(true);
  let target = $state<number | string>(80);
  let critical = $state<number | string>(90);

  $effect(() => {
    get<DeviceSettings>(`/api/storage/device?path=${encodeURIComponent(path)}`).then(
      (s) => {
        settings = s;
        name = s.name;
        enabled = s.automaticRemovalEnabled;
        target = Math.round(s.targetUsagePercent);
        critical = Math.round(s.criticalUsagePercent);
      },
      (err) => (failure = err.message),
    );
  });

  async function save() {
    // An empty or not-a-number field is refused here, as the server did.
    const t = Number(target), c = Number(critical);
    if (String(target).trim() === '' || !Number.isFinite(t)) throw new Error('target_usage_percent must be a number');
    if (String(critical).trim() === '' || !Number.isFinite(c)) throw new Error('critical_usage_percent must be a number');
    await put('/api/storage/device', { path, name, targetUsagePercent: t, criticalUsagePercent: c, automaticRemovalEnabled: enabled });
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
    <Field label="Critical %" hint="Overtide steers toward the target: it adds first, then removal makes room. Past critical, nothing more is added until removal catches up. Must be above the target.">
      <input type="number" min="2" max="99" step="1" bind:value={critical} />
    </Field>
  {/if}
</FormModal>
