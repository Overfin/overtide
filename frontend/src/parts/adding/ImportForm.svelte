<!-- Says which episode each waiting file of a download is. For each file:
     how long it lasts, and what its number means in Sonarr's order, TMDB's
     and TMDB's others, with titles in the server's language, air dates and
     lengths, so the right one can be told. Each starts on what its number
     says; "Leave waiting" leaves it out. -->
<script lang="ts">
  import { FormModal, Notice, Loading } from '@overfin/ui';
  import { get, post } from '../../lib/api';
  import type { EpisodeOption, ImportOptions, Meaning, WaitingImport } from '../../lib/types-adding';

  let { item, saved, close }: { item: WaitingImport; saved: () => void; close: () => void } = $props();

  let options = $state<ImportOptions | null>(null);
  let failure = $state('');
  let chosen = $state<Record<string, string>>({});

  const address = $derived(`/api/adding/imports/${encodeURIComponent(item.serviceId)}/${encodeURIComponent(item.downloadId)}`);
  const fileName = (path: string) => path.slice(path.lastIndexOf('/') + 1);
  const pad = (n: number) => String(n).padStart(2, '0');
  const number = (season: number, episode: number) => `S${pad(season)}E${pad(episode)}`;

  /** length is a file's length as people read it. */
  function length(seconds?: number): string {
    if (!seconds) return '';
    const m = Math.floor(seconds / 60);
    return `${m} min ${pad(seconds % 60)} s`;
  }

  /** fits: TMDB's runtime is within 2 minutes of the file's length. */
  const fits = (seconds?: number, runtime?: number) => !!seconds && !!runtime && Math.abs(seconds / 60 - runtime) <= 2;

  function named(m: Meaning | EpisodeOption): string {
    return [m.title, m.translated].filter(Boolean).join(' · ');
  }

  function label(e: EpisodeOption): string {
    return [number(e.season, e.episode), named(e), e.airDate, e.runtime ? `${e.runtime} min` : ''].filter(Boolean).join(' · ');
  }

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

<FormModal title="Choose episodes" wide saveLabel="Import" {save} {close}>
  {#if failure}
    <Notice warn>{failure}</Notice>
  {:else if !options}
    <Loading />
  {:else}
    <p class="muted">
      Each file starts on what its number says in Sonarr's order, which may be wrong. Under each file: what that number means
      in each order, and TMDB's length of the episode, marked when it fits the file's. Files on "Leave waiting" stay as they are.
    </p>
    {#each item.files as file (file.path)}
      {@const facts = options.files[file.path]}
      <section class="file">
        <div class="file-head">
          <span class="filename">{fileName(file.path)}</span>
          {#if facts?.seconds}<span class="muted">{length(facts.seconds)}</span>{/if}
        </div>
        <div class="muted why">{file.why}</div>
        {#if facts?.meanings.length}
          <div class="table-wrap">
            <table>
              <thead><tr><th>Order</th><th>Number</th><th>Episode</th><th>Aired</th><th>Length</th></tr></thead>
              <tbody>
                {#each facts.meanings as m (m.order)}
                  <tr>
                    <td>{m.order}</td>
                    <td>{number(m.season, m.episode)}</td>
                    {#if m.missing}
                      <td class="muted" colspan="3">None: this order has no such episode.</td>
                    {:else}
                      <td>{named(m)}</td>
                      <td class="muted">{m.airDate ?? ''}</td>
                      <td class:fit={fits(facts.seconds, m.runtime)}>{m.runtime ? `${m.runtime} min` : ''}{fits(facts.seconds, m.runtime) ? ' ✓' : ''}</td>
                    {/if}
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
        <label class="pick">
          <span class="visually-hidden">Episode for {fileName(file.path)}</span>
          <select bind:value={chosen[file.path]}>
            <option value="">Leave waiting</option>
            {#each options.episodes as e (e.id)}
              <option value={String(e.id)}>{label(e)}</option>
            {/each}
          </select>
        </label>
      </section>
    {/each}
  {/if}
</FormModal>

<style>
  .file {
    border-top: 1px solid var(--line, rgba(128, 128, 128, 0.25));
    padding: 0.75rem 0;
    display: grid;
    gap: 0.4rem;
  }
  .file-head {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 1rem;
    justify-content: space-between;
  }
  .filename {
    font-weight: 600;
    overflow-wrap: anywhere;
  }
  .why {
    font-size: 0.9em;
  }
  .fit {
    font-weight: 600;
  }
  .pick select {
    width: 100%;
  }
</style>
