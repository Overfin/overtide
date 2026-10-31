<!-- Settings: the password, the API key, and when Overtide may remove
     titles on its own. -->
<script lang="ts">
  import { PageHead, Card, Field, Setting, Status, Busy, Loading, Notice, toast } from '@overfin/ui';
  import { get, put } from '../lib/api';
  import type { Settings } from '../lib/types-services';
  import PasswordForm from '../parts/settings/PasswordForm.svelte';

  let data = $state<Settings | null>(null);
  let failure = $state('');
  let autoMode = $state('disabled');
  let unassociated = $state(false);
  let incomplete = $state(false);
  let copies = $state('first');
  let exclude = $state(false);
  let dryRun = $state(false);
  let seeding = $state<number | string>('');
  let status = $state<{ text: string; kind: '' | 'ok' | 'error' }>({ text: '', kind: '' });
  let password = $state(false);

  function fill(s: Settings) {
    data = s;
    autoMode = s.autoMode;
    unassociated = s.autoRemoveUnassociatedTorrents;
    incomplete = s.autoRemoveIncompleteTorrents;
    copies = s.copiesWithTitles ? 'with-titles' : 'first';
    exclude = s.autoExcludeFromImportLists;
    dryRun = s.dryRun;
    // 0 is off, shown empty.
    seeding = s.privateMinSeedingDays || '';
  }

  async function load() {
    try {
      fill(await get<Settings>('/api/settings'));
    } catch (err) {
      failure = (err as Error).message;
    }
  }

  async function save() {
    status = { text: '', kind: '' };
    // An empty field means 0, off.
    const raw = String(seeding ?? '').trim();
    const days = raw === '' ? 0 : Number(raw);
    if (!Number.isInteger(days)) {
      status = { text: 'Days of seeding for private torrents must be a whole number', kind: 'error' };
      return;
    }
    try {
      fill(await put<Settings>('/api/settings/removal', {
        autoMode,
        autoRemoveUnassociatedTorrents: unassociated,
        dryRun,
        autoExcludeFromImportLists: exclude,
        autoRemoveIncompleteTorrents: incomplete,
        copiesWithTitles: copies === 'with-titles',
        privateMinSeedingDays: days,
      }));
      status = { text: 'Removal settings saved.', kind: 'ok' };
    } catch (err) {
      status = { text: (err as Error).message, kind: 'error' };
    }
  }

  load();
</script>

<PageHead title="Settings" description="Your password, the API key, and when Overtide may remove titles on its own." />

{#if failure}
  <Notice warn>{failure}</Notice>
{:else if !data}
  <Loading />
{:else}
  <Card title="Change password">
    <button type="button" onclick={() => (password = true)}>Update password</button>
  </Card>

  <Card title="API key" description="What Overswell gives to ask Overtide whether a folder has room before it adds a title: add Overtide in Overswell's Services with this key.">
    <Field label="Key"><input class="mono" value={data.apiKey} readonly /></Field>
  </Card>

  <Card title="Automatic removal">
    <Setting label="Automatic removal" hint={'Disabled: the evaluation never runs. Confirm: it evaluates candidates on schedule but never submits anything — review and remove them yourself. Auto: it submits them on its own. Each service also needs "Allow automatic removal" checked in its own settings before anything it owns is touched.'}>
      {#snippet children(ids)}
        <select bind:value={autoMode} aria-labelledby={ids.label} aria-describedby={ids.hint}>
          <option value="disabled">Disabled</option>
          <option value="confirm">Confirm</option>
          <option value="auto">Auto</option>
        </select>
      {/snippet}
    </Setting>
    <Setting label="Also remove torrents with no known owner" hint="Off by default: Overtide has no relationship to judge these safe to delete unattended.">
      {#snippet children(ids)}<input type="checkbox" bind:checked={unassociated} aria-labelledby={ids.label} aria-describedby={ids.hint} />{/snippet}
    </Setting>
    <Setting label="Allow removal of incomplete torrents" hint="Off by default: only controls whether an incomplete download can be a candidate at all. A torrent added in the last 7 days is never picked on its own.">
      {#snippet children(ids)}<input type="checkbox" bind:checked={incomplete} aria-labelledby={ids.label} aria-describedby={ids.hint} />{/snippet}
    </Setting>
    <Setting label="Torrents that are copies of a title" hint={'A torrent whose files were copied to the library, not hardlinked: removing it frees its whole size and the library loses nothing, so by default copies go before any title. "Ordered with their title" places each one by its title\'s Overfin score instead, so seeding goes with the titles that matter least. Torrents that back nothing at all always go first.'}>
      {#snippet children(ids)}
        <select bind:value={copies} aria-labelledby={ids.label} aria-describedby={ids.hint}>
          <option value="first">Removed before titles</option>
          <option value="with-titles">Ordered with their title</option>
        </select>
      {/snippet}
    </Setting>
    <Setting label="Add removed media to import list exclusions" hint={'Off by default: same effect as checking "Add to import list exclusions" on the manual removal overlay, but applied automatically. Prevents a configured import list from re-adding what was just removed.'}>
      {#snippet children(ids)}<input type="checkbox" bind:checked={exclude} aria-labelledby={ids.label} aria-describedby={ids.hint} />{/snippet}
    </Setting>
    <Setting label="Dry run" hint="Simulates every removal, manual or automatic, without deleting anything. Turn this off once you trust what's being selected.">
      {#snippet children(ids)}<input type="checkbox" bind:checked={dryRun} aria-labelledby={ids.label} aria-describedby={ids.hint} />{/snippet}
    </Setting>
    <Setting label="Keep private torrents at least (days)" hint="Off when empty or 0. For private trackers that require a seeding time: a private torrent, and the title it's hardlinked to, stay until it has seeded this long.">
      {#snippet children(ids)}<input type="number" min="0" max="3650" step="1" placeholder="0" bind:value={seeding} aria-labelledby={ids.label} aria-describedby={ids.hint} />{/snippet}
    </Setting>
    {#snippet foot()}
      <Status text={status.text} kind={status.kind} />
      <Busy kind="primary" action={save}>Save</Busy>
    {/snippet}
  </Card>
{/if}

{#if password}
  <PasswordForm saved={() => toast('Password updated.')} close={() => (password = false)} />
{/if}
