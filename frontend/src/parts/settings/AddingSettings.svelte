<!-- The Settings page's adding section: how titles are added, and where
     they go. -->
<script lang="ts">
  import { Card, Setting, Status, Busy, Notice, Empty, Loading, RowActions, ask, toast } from '@overfin/ui';
  import { get, put, del } from '../../lib/api';
  import type { AddingSettings, Destination } from '../../lib/types-adding';
  import { short } from '../../lib/words-adding';
  import DestinationForm from '../adding/DestinationForm.svelte';
  import '../../styles/adding.css';

  let data = $state<AddingSettings | null>(null);
  let failure = $state('');
  let mode = $state('off');
  let manage = $state('own');
  let titlesPerDay = $state(10);
  let upgradesPerDay = $state(0);
  let searches = $state(3);
  let eventsUrl = $state('');
  let status = $state<{ text: string; kind: '' | 'ok' | 'error' }>({ text: '', kind: '' });
  // editing: the destination whose form is open; null for a new one.
  let editing = $state<number | null | undefined>(undefined);

  async function load() {
    try {
      data = await get<AddingSettings>('/api/settings/adding');
      ({ mode, manage, titlesPerDay, upgradesPerDay, searches, eventsUrl } = data);
      failure = '';
    } catch (err) {
      failure = (err as Error).message;
    }
  }

  async function save() {
    status = { text: '', kind: '' };
    try {
      await put('/api/settings/adding', { mode, manage, searches: Number(searches), titlesPerDay: Number(titlesPerDay), upgradesPerDay: Number(upgradesPerDay), eventsUrl: eventsUrl.trim() });
      status = { text: 'Saved.', kind: 'ok' };
      await load();
    } catch (err) {
      status = { text: (err as Error).message, kind: 'error' };
    }
  }

  async function remove(d: Destination) {
    if (!(await ask(`Remove the destination for ${d.for}?`, { title: 'Remove this destination?', yes: 'Remove', danger: true }))) return;
    try {
      await del(`/api/destinations/${d.index}`);
      toast('Destination removed.');
    } catch (err) {
      toast((err as Error).message, true);
    }
    await load();
  }

  load();
</script>

{#if failure}<Notice warn>{failure}</Notice>{/if}

{#if !data}
  <Loading />
{:else}
<Card title="Adding">
  <Setting label="Adding titles" hint="Dry run only reads: nothing is added, searched for, tagged or unmonitored.">
    {#snippet children(ids)}
      <select bind:value={mode} aria-labelledby={ids.label} aria-describedby={ids.hint}>
        <option value="off">Off</option>
        <option value="dry_run">Dry run</option>
        <option value="on">On</option>
      </select>
    {/snippet}
  </Setting>
  <Setting label="Overtide manages" hint="Only what it adds: titles already there, or added by you, by lists or by other request apps, are left to Radarr and Sonarr. Everything: Radarr and Sonarr no longer fetch or upgrade anything on their own. Overtide fetches what they would have, at its own pace: monitored films and episodes without their files, and the new episodes of the series they followed. It keeps how each title was monitored before it took it: switching back gives every title it took back to Radarr and Sonarr exactly as it was.">
    {#snippet children(ids)}
      <select bind:value={manage} aria-labelledby={ids.label} aria-describedby={ids.hint}>
        <option value="own">Only what it adds</option>
        <option value="all">Everything in Radarr and Sonarr</option>
      </select>
    {/snippet}
  </Setting>
  {#if manage === 'all'}
    <Setting label="Overtide's address for Radarr and Sonarr" hint={'Where Radarr and Sonarr tell Overtide each title they add, so it takes it at once (a webhook named Overtide in their Connect settings, which Overtide makes). Left empty, Overtide finds it: it tries the address you opened this page at, its own name and the apps\' machine, and keeps the one a test reaches it at. Set it when they reach Overtide some other way, like http://overtide:8088.'}>
      {#snippet children(ids)}<input type="url" placeholder="Found by Overtide" bind:value={eventsUrl} aria-labelledby={ids.label} aria-describedby={ids.hint} />{/snippet}
    </Setting>
    {#if data.eventsProblem}<Notice warn>{data.eventsProblem}</Notice>{/if}
  {/if}
  <Setting label="Titles a day" hint="How many titles may start downloading in 24 hours, whatever brought them: wishes first, then older incomplete seasons, then the rest. Past it, they wait, for months if need be. A series counts once per season, and one with more seasons than that still goes, alone in its day. Episodes of a season airing don't wait and don't count; a season just finished doesn't wait but counts. 10 by default.">
    {#snippet children(ids)}<input type="number" min="1" max="500" bind:value={titlesPerDay} aria-labelledby={ids.label} aria-describedby={ids.hint} />{/snippet}
  </Setting>
  <Setting label="Upgrades a day" hint="How many searches for a better copy may go in 24 hours, for what's below its quality profile's cutoff, the most valued first. A season is one search, a season still airing waits, and a title is searched again after 30 days at the earliest. A season that came episode by episode as it aired, or without its pack, gets a whole-season pack here: one of a lower quality than its best episodes is taken (one release for the season), never one below its worst. They use the searches left after everything else. Custom format scores aren't looked at. 0, the default, is no upgrades.">
    {#snippet children(ids)}<input type="number" min="0" max="500" bind:value={upgradesPerDay} aria-labelledby={ids.label} aria-describedby={ids.hint} />{/snippet}
  </Setting>
  <Setting label="Searches every 15 minutes" hint="At most. With Prowlarr connected, searches also stop short of each tracker's query limit. 3 by default.">
    {#snippet children(ids)}<input type="number" min="1" max="60" bind:value={searches} aria-labelledby={ids.label} aria-describedby={ids.hint} />{/snippet}
  </Setting>

  {#if data.manage === 'all' && data.summary}
    {@const s = data.summary}
    <Notice warn={s.dryRun}>
      {#if s.dryRun}Dry run, {short(s.at)}, what Overtide would do:{:else}Last pass, {short(s.at)}:{/if}
      {s.tagged} titles tagged, {s.unmonitored} films unmonitored, {s.seriesTaken} series taken, {s.filmsQueued} films without their file put in line. {s.episodesWanted} missing episodes wanted, {s.seasons} seasons to replace with a whole-season pack, {s.following} series followed for new episodes, {s.monitored} airing episodes monitored.
      {#if s.error}<div class="error">{s.error}</div>{/if}
    </Notice>
  {/if}

  <details class="how">
    <summary>How adding works</summary>
    <p>Every hour, Overtide asks Overfin what to add: the titles your libraries lack, in the order Overfin gives them (its most valuable first, from what people here watch and like, and what's out in your country). What people wished for in Overfin goes first, in the turns Overfin gives (each account in turn), then the films taken from Radarr with "Everything"; Overtide tells Overfin what became of each wish.</p>
    <p>Every 15 minutes it tries the titles whose turn it is, one search at a time, at most the searches set above (a series' seasons each counting). A title goes in Radarr or Sonarr once (their root folder and quality profile), unmonitored and tagged "overtide-added" ("overtide-requested" for a wish), and adding never deletes it: removal does, by its own settings. A try monitors it (for a series, the seasons Overfin suggests) and searches for it; if nothing started, it's unmonitored again and tried later: after 1 day, then 2, 4, 8, 16, then every 30. Two days before its release, a title is monitored without a search, so Radarr's or Sonarr's RSS catches the release. Once its download is confirmed, a title is unmonitored, so Radarr and Sonarr don't upgrade it on their own; if the download stops before its import, the title goes back in line.</p>
    <p>Searches go in this order: what was added in Radarr or Sonarr while Overtide manages everything (by you, by your import lists or by another app: you want it now), episodes airing (a season just finished gets its last episodes alone, its pack later as an upgrade), wishes, older incomplete seasons, then the other titles, then upgrades. A series comes a season at a time, the lowest first, each season counting as a title: one Overfin suggested goes first among its suggestions until its last season is grabbed (or one fails), and an incomplete series' missing seasons come the same way; a wish keeps its seasons together. At most the titles a day set above start downloading in 24 hours; past them, everything but airing episodes waits until the oldest download is a day old. Single episodes don't count as titles; a season mostly missing does. At most 200 suggestions wait. One leaves after 5 tries that found nothing, or 60 days in the list. A title that left the libraries, or the list unfinished, isn't taken again for 6 months.</p>
    <p>Whatever it manages, Overtide follows the airing seasons it has files of: each episode is monitored from 2 days before it airs until its download starts, so Sonarr's RSS gets it, and searched for from 2 days after it aired, every day for 14 days, then less and less often. 7 days after a season's last episode aired, a season with missing episodes is replaced by a whole-season pack. A season with some of its files that lacks aired episodes gets the same, in its turn, unless it lost a file in the last 6 months other than to an upgrade: that one was removed, not missed. A season with no file is one nobody wants. A season still short after a whole-season pack came isn't searched again: the Adding page says it needs a look. A pack's files Sonarr thought no upgrade replace the older ones anyway, even of better quality, so a season comes from one release. A finished download whose episodes all have their files is taken off Sonarr's queue, and stays in the download client, unless it has files matching none of Sonarr's episodes: its season then needs a look.</p>
    <p>A search that found nothing counts as a try only when every tracker answered (with Prowlarr connected).</p>
    <p>Tags in Radarr and Sonarr say where each title Overtide manages came from: "overtide-added" (from Overfin's suggestions), "overtide-requested" (wished for in Overfin), "overtide-found" (taken while Overtide manages everything). Tag a title "overtide-ignore" there and Overtide gives it back as it was before it took it, takes off its other tags (but "overtide-keep"), and never takes, unmonitors or searches for it again. Import lists are yours: Overtide doesn't read them.</p>
  </details>

  {#snippet foot()}
    <Status text={status.text} kind={status.kind} />
    <Busy kind="primary" action={save}>Save</Busy>
  {/snippet}
</Card>

<Card title="Where titles go" description={'A title goes to the first destination of one of Overfin\'s categories it fits, else to its kind\'s "every title". With none for its kind, it goes to the first Radarr or Sonarr, when that one has a single root folder and quality profile.'}>
  <table>
    <thead><tr><th>For</th><th>Service</th><th class="hide-narrow">Root folder</th><th class="hide-narrow">Quality profile</th><th><span class="visually-hidden">Actions</span></th></tr></thead>
    <tbody>
      {#each data.destinations as d (d.index)}
        <tr>
          <td><span class="name">{d.for}</span></td>
          <td>{d.service}</td>
          <td class="muted mono hide-narrow">{d.rootFolder}</td>
          <td class="hide-narrow">{d.profileName}</td>
          <RowActions name={d.for} edit={() => (editing = d.index)} remove={() => remove(d)} />
        </tr>
      {:else}
        <tr><td colspan="5"><Empty>No destination yet.</Empty></td></tr>
      {/each}
    </tbody>
  </table>
  {#snippet foot()}
    <span class="grow"></span>
    <button type="button" onclick={() => (editing = null)}>Add a destination</button>
  {/snippet}
</Card>
{/if}

{#if editing !== undefined}
  <DestinationForm index={editing} saved={() => { toast('Saved.'); load(); }} close={() => (editing = undefined)} />
{/if}
