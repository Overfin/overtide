<!-- The torrents whose files are gone from disk, which qBittorrent still
     lists: removed from qBittorrent only, files kept. -->
<script lang="ts">
  import { Modal, Status, Loading, ask, toast } from '@overfin/ui';
  import { get, post } from '../../lib/api';
  import { message } from '../../lib/format';

  let { close }: { close: () => void } = $props();

  let data = $state<{ torrents: { name: string; status: string }[]; dryRun: boolean } | null>(null);
  let failure = $state('');
  let busy = $state(false);

  get<{ torrents: { name: string; status: string }[]; dryRun: boolean }>('/api/torrents-missing').then(
    (d) => (data = d),
    (err) => (failure = message(err)),
  );

  const many = (n: number) => `${n} torrent${n !== 1 ? 's' : ''}`;

  async function remove() {
    const d = data!;
    const n = d.torrents.length;
    const sure = await ask(
      d.dryRun ? `Simulate removing ${many(n)} from qBittorrent? Dry run is on: nothing is sent to qBittorrent.` : `Remove ${many(n)} from qBittorrent? Their files are not deleted.`,
      { title: 'Remove from qBittorrent', yes: d.dryRun ? 'Simulate' : 'Remove', danger: !d.dryRun },
    );
    if (!sure) return;
    busy = true;
    failure = '';
    try {
      await post('/api/torrents-missing');
      toast(d.dryRun ? 'Simulated.' : 'Removed from qBittorrent.');
      close();
    } catch (err) {
      failure = message(err);
    } finally {
      busy = false;
    }
  }
</script>

<Modal title="Torrents whose files are gone" description={data ? `${many(data.torrents.length)} still listed by qBittorrent` : ''} {busy} {close}>
  {#if !data}
    {#if failure}<p class="error">{failure}</p>{:else}<Loading />{/if}
  {:else}
    <p>These torrents are removed from qBittorrent only. <b>No file is deleted</b>: qBittorrent is told to keep files, and each torrent's files are checked again on disk first. A torrent you kept, one with a seeding obligation, or one whose disk looks empty (not mounted?) is left alone.</p>
    {#if data.dryRun}<p class="dry">Dry run is on: nothing is sent to qBittorrent.</p>{/if}
    <ul class="list">
      {#each data.torrents as t, i (i)}<li><div>{t.name} <span class="muted">· {t.status}</span></div></li>{:else}<li class="muted">None now.</li>{/each}
    </ul>
  {/if}
  {#snippet foot()}
    <Status text={data ? failure : ''} kind={failure ? 'error' : ''} />
    <button type="button" disabled={busy} onclick={close}>Cancel</button>
    {#if data?.torrents.length}
      <button type="button" class={data.dryRun ? 'primary' : 'danger'} disabled={busy} onclick={remove}>{data.dryRun ? 'Simulate' : `Remove ${data.torrents.length} from qBittorrent`}</button>
    {/if}
  {/snippet}
</Modal>
