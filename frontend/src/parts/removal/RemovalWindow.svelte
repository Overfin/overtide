<!-- The removal window: what removing a library title, a torrent or
     unmanaged files would do, what may be chosen, and its consequences,
     recalculated here as the choice changes (selection.ts). Removing goes
     through the server's checks and its scheduler; in dry run it's only
     simulated. -->
<script lang="ts">
  import { Modal, Badge, Notice, Loading, toast } from '@overfin/ui';
  import { get, post } from '../../lib/api';
  import { bytes, message } from '../../lib/format';
  import { applyUpdates } from '../../lib/live.svelte';
  import type { RemovalRequest } from '../../lib/removal.svelte';
  import type { GroupFile, RemovalView } from '../../lib/types-removal';
  import { actionKey, consequences, initialActions, physicalState, physicalActions, selectedActions, splitAction } from './selection';
  import { indeterminate } from './indeterminate';
  import '../../styles/removal.css';

  let { request, close }: { request: RemovalRequest; close: () => void } = $props();

  let view = $state<RemovalView | null>(null);
  let problem = $state('');
  // picked: the actions chosen (a field and its value each, see selection.ts).
  let picked = $state(new Set<string>());
  let excludeMovies = $state(false);
  let excludeSeries = $state(false);
  let busy = $state(false);
  let failure = $state('');

  const titles: Record<string, string> = { media: 'Remove media', torrent: 'Remove torrent', unmanaged: 'Remove files' };
  const title = $derived(titles[view?.kind ?? request.kind] ?? 'Remove files');

  $effect(() => {
    const query = new URLSearchParams();
    for (const [name, value] of Object.entries(request.params)) {
      for (const v of Array.isArray(value) ? value : [value]) query.append(name, v);
    }
    get<RemovalView>(`/api/removal/${request.kind}?${query}`).then(
      (v) => {
        view = v;
        picked = initialActions(v.selection);
      },
      (err) => (problem = message(err)),
    );
  });

  const actions = $derived(view ? selectedActions(view.selection, picked) : new Set<string>());
  const result = $derived(view ? consequences(view.selection, actions) : null);

  // A pick is one checkbox: a physical file (a media's own, which stands
  // for every action on it), or a field and its value.
  type Pick = { physicalKey: string; initial: boolean } | { name: string; value: string };

  function isOn(pick: Pick): boolean {
    if ('physicalKey' in pick) return view ? (physicalState(view.selection, pick.physicalKey, actions)?.checked ?? pick.initial) : false;
    return actions.has(actionKey(pick.name, pick.value));
  }

  function isSome(pick: Pick): boolean {
    if ('physicalKey' in pick) return view ? (physicalState(view.selection, pick.physicalKey, actions)?.indeterminate ?? false) : false;
    return false;
  }

  function set(picks: Pick[], on: boolean) {
    const next = new Set(picked);
    for (const pick of picks) {
      const keys = 'physicalKey' in pick ? (view ? physicalActions(view.selection, pick.physicalKey) : []) : [actionKey(pick.name, pick.value)];
      for (const key of keys) {
        if (on) next.add(key);
        else next.delete(key);
      }
    }
    picked = next;
  }

  // A group's checkbox: all of its picks, some, or none.
  const allOn = (picks: Pick[]) => picks.length > 0 && picks.every(isOn);
  const someOn = (picks: Pick[]) => picks.some((p) => isOn(p) || isSome(p)) && !allOn(picks);

  const ownPicks = (files: GroupFile[]): Pick[] => files.map((f) => ({ physicalKey: f.physicalKey, initial: f.selected }));
  const relatedPicks = (files: GroupFile[]): Pick[] => files.map((f) => ({ name: 'managed_file', value: f.key }));
  const unmanagedPicks = $derived<Pick[]>((view?.relatedUnmanaged ?? []).filter((p) => p.selectable).map((p) => ({ name: 'unmanaged_path', value: p.path })));

  const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;

  async function submit() {
    if (!view || busy || actions.size === 0) return;
    busy = true;
    failure = '';
    const body: Record<string, unknown> = { kind: view.kind, operationToken: view.operationToken };
    if (view.kind === 'media') Object.assign(body, { mediaType: view.mediaType, mediaId: view.mediaId, serviceId: view.mediaServiceId });
    if (view.kind === 'torrent') Object.assign(body, { hash: view.hash, serviceId: view.torrentServiceId });
    // The actions, as the request's fields (see removalRequest in Go).
    const fields: Record<string, string> = { managed_file: 'managedFiles', torrent: 'torrents', path: 'paths', unmanaged_path: 'unmanagedPaths' };
    const lists: Record<string, string[]> = {};
    for (const key of actions) {
      const [name, value] = splitAction(key);
      if (name === 'target') body.target = true;
      else if (fields[name]) (lists[fields[name]] ??= []).push(value);
    }
    Object.assign(body, lists);
    if (excludeMovies) body.excludeMovies = true;
    if (excludeSeries) body.excludeSeries = true;
    try {
      await post('/api/removal/execute', body);
      const label = view.label || 'Removal';
      close();
      toast(`${label} queued for removal.`);
      applyUpdates();
    } catch (err) {
      busy = false;
      failure = message(err);
    }
  }
</script>

<Modal {title} description={view?.label ?? ''} size="wide" {busy} {close}>
  {#if problem}
    <p class="bad error">{problem}</p>
  {:else if !view || !result}
    <Loading />
  {:else}
    {@const v = view}
    {#if v.dryRun}
      <p><Badge kind="warn">DRY RUN</Badge></p>
      <Notice warn>Dry run — nothing will be removed.</Notice>
    {/if}

    <div class="removal-summary">
      <div class="muted">Storage reclaimed</div>
      <div class="big">{bytes(result.reclaimed)}</div>
      <div class="muted">{result.selectedFiles ? `${plural(result.selectedFiles, 'file')} selected · ${bytes(result.selectedBytes)}` : ''}</div>
    </div>
    {#if result.guidance}
      <Notice warn>
        <p><strong>{result.guidance.title}</strong></p>
        <p>{result.guidance.action}</p>
      </Notice>
    {/if}
    {#each v.warnings as w, i (i)}<p class="error">{w}</p>{/each}

    {#if v.kind === 'media' && v.managedGroups.length}
      <section class="removal-section">
        <h3>{v.managedSectionLabel}</h3>
        {#each v.managedGroups as group, g (g)}
          {@const picks = ownPicks(group.files)}
          {#if group.complete && group.label !== 'Files' && group.files.length > 1}
            <details class="removal-group">
              <summary><label class="inline"><input type="checkbox" checked={allOn(picks)} use:indeterminate={someOn(picks)} onchange={(e) => set(picks, e.currentTarget.checked)} /> {group.label} · {group.files.length} episodes · {group.size}</label></summary>
              {#each group.files as f, i (i)}{@render ownFile(f)}{/each}
            </details>
          {:else}
            <div class="removal-group">
              {#if group.label !== 'Files'}
                <label class="inline group-label"><input type="checkbox" checked={allOn(picks)} use:indeterminate={someOn(picks)} onchange={(e) => set(picks, e.currentTarget.checked)} /> {group.label}{group.totalFiles > 0 ? ` · ${group.files.length}/${group.totalFiles} episodes` : ''} · {group.size}</label>
              {/if}
              {#each group.files as f, i (i)}{@render ownFile(f)}{/each}
            </div>
          {/if}
        {/each}
      </section>
    {:else if v.kind === 'torrent' && v.torrentTarget}
      <section class="removal-section">
        <h3>Torrent</h3>
        <label class="inline"><input type="checkbox" checked={isOn({ name: 'target', value: '1' })} onchange={(e) => set([{ name: 'target', value: '1' }], e.currentTarget.checked)} /> <strong>{v.label}</strong></label>
      </section>
    {/if}

    {#if v.relatedManaged.length && v.kind !== 'torrent'}
      <section class="removal-section">
        <h3>Library files</h3>
        {#each v.relatedManaged as m, mi (mi)}
          {@const all = m.groups.flatMap((gr) => relatedPicks(gr.files))}
          <div class="removal-media">
            <label class="inline group-label"><input type="checkbox" checked={allOn(all)} use:indeterminate={someOn(all)} onchange={(e) => set(all, e.currentTarget.checked)} /> <strong>{m.title}{m.year ? ` (${m.year})` : ''}</strong> <span class="muted">· {plural(m.fileCount, 'file')}</span></label>
            {#each m.groups as group, g (g)}
              {@const picks = relatedPicks(group.files)}
              <div class="removal-group">
                {#if group.label !== 'Files'}
                  <label class="inline group-label"><input type="checkbox" checked={allOn(picks)} use:indeterminate={someOn(picks)} onchange={(e) => set(picks, e.currentTarget.checked)} /> {group.label} · {plural(group.files.length, 'file')}</label>
                {/if}
                {#each group.files as f, i (i)}
                  <label class="inline removal-file"><input type="checkbox" checked={isOn({ name: 'managed_file', value: f.key })} onchange={(e) => set([{ name: 'managed_file', value: f.key }], e.currentTarget.checked)} /><span><strong class="managed-name" title={f.filename}>{f.filename}</strong> <span class="muted">{f.label ? `${f.label} · ` : ''}{f.size}</span></span></label>
                {/each}
              </div>
            {/each}
          </div>
        {/each}
      </section>
    {/if}

    {#if v.relatedGroups.length}
      <section class="removal-section">
        <h3>{v.kind === 'media' ? `Media torrents · ${v.relatedTorrentCount}` : 'Related torrents'}</h3>
        {#if v.kind === 'media'}<p class="muted">{v.selectedTorrentCount} selected for removal · {v.preservedTorrentCount} preserved. Every related torrent may be selected; only the one proven to physically back this media is pre-selected.</p>{/if}
        {#each v.relatedGroups as group, g (g)}
          <div class="removal-group">
            <h4>{group.label}</h4>
            {#each group.torrents as t (t.hash)}
              <div class="removal-row">
                {#if t.selectable}
                  <label class="inline"><input type="checkbox" checked={isOn({ name: 'torrent', value: t.hash })} onchange={(e) => set([{ name: 'torrent', value: t.hash }], e.currentTarget.checked)} /> <strong>{t.name}</strong></label>
                {:else}
                  <strong>{t.name}</strong>
                {/if}
                <div class="muted">{plural(t.fileCount, 'file')} · {t.size} · {t.meta}</div>
              </div>
            {/each}
          </div>
        {/each}
      </section>
    {/if}

    {#if v.relatedUnmanaged.length && v.kind === 'unmanaged'}
      <section class="removal-section">
        <h3>Related unmanaged files</h3>
        <label class="inline"><input type="checkbox" checked={allOn(unmanagedPicks)} use:indeterminate={someOn(unmanagedPicks)} onchange={(e) => set(unmanagedPicks, e.currentTarget.checked)} /> Select all</label>
        {#each v.relatedUnmanaged as p (p.path)}
          <label class="inline removal-file"><input type="checkbox" checked={isOn({ name: 'unmanaged_path', value: p.path })} onchange={(e) => set([{ name: 'unmanaged_path', value: p.path }], e.currentTarget.checked)} /> <strong class="mono" title={p.path}>{p.short}</strong> <span class="muted">· {p.size}</span></label>
        {/each}
      </section>
    {/if}

    {#if v.relatedUnmanaged.length && v.kind === 'torrent'}
      <section class="removal-section">
        <h3>Preserved related library paths</h3>
        <p class="muted">These paths share physical files with the torrent. Removing the torrent does not remove them.</p>
        {#each v.relatedUnmanaged as p (p.path)}
          <div class="removal-row"><strong class="mono" title={p.path}>{p.short}</strong> <span class="muted">· {p.size} · preserved</span></div>
        {/each}
      </section>
    {/if}

    {#if v.relatedManaged.length && v.kind === 'torrent'}
      <section class="removal-section">
        <h3>Hardlinked library files</h3>
        <p class="error">This torrent shares physical data with these library files. Removing only the torrent will not free this space — the library file must also be removed, from its own service, to reclaim it.</p>
        {#each v.relatedManaged as m, mi (mi)}
          <div class="removal-row"><a href={`/library/${m.type}/${m.sourceId}?service_id=${encodeURIComponent(m.serviceId)}`} onclick={close}><strong>{m.title}{m.year ? ` (${m.year})` : ''}</strong></a> <span class="muted">· {plural(m.fileCount, 'file')} hardlinked</span></div>
        {/each}
      </section>
    {/if}

    <details class="removal-storage">
      <summary>Files and storage ({plural(v.fileGroups.length, 'physical file')} · {plural(v.pathCount, 'path')})</summary>
      {#each v.fileGroups as file, i (i)}
        <div class="removal-row">
          {#each file.paths as p, j (j)}<div class="mono file-path" title={p.path}><strong>{p.label}:</strong> {p.short}</div>{/each}
          <div class="muted">
            {#if file.exists}{file.size}{#if file.identityKnown && file.links > 1}{' · '}<span class="hardlink">hardlinked{#if file.missingLinks > 0}{' · '}<span class="error">{plural(file.missingLinks, 'hardlink')} missing</span>{:else}{' · '}{file.pathCount} paths to the same physical file{/if}</span>{/if}{:else}missing{/if}{#if file.error}{' · '}{file.error}{/if}
          </div>
        </div>
      {:else}
        <p class="muted">No files.</p>
      {/each}
    </details>

    {#if v.canUnmonitorMovies || v.canUnmonitorEpisodes || v.canExcludeMovies || v.canExcludeSeries}
      <section class="removal-section">
        <h3>After removal</h3>
        {#if v.canUnmonitorMovies || v.canUnmonitorEpisodes}<p class="muted">The titles stay in Radarr and Sonarr, unmonitored (a series, its removed episodes), tagged "overtide".</p>{/if}
        {#if v.canExcludeMovies}<label class="inline"><input type="checkbox" bind:checked={excludeMovies} /> Add affected movies to import list exclusions</label>{/if}
        {#if v.canExcludeSeries}<label class="inline"><input type="checkbox" bind:checked={excludeSeries} /> Add affected series to import list exclusions</label>{/if}
      </section>
    {/if}

    {#if failure}<p class="error operation-error" role="alert">{failure}</p>{/if}
  {/if}

  {#snippet foot()}
    <span class="grow"></span>
    {#if problem}
      <button type="button" onclick={close}>Close</button>
    {:else}
      <button type="button" disabled={busy} onclick={close}>Cancel</button>
      {#if view}
        <button type="button" class={view.dryRun ? 'primary' : 'danger'} disabled={actions.size === 0 || busy} onclick={submit}>
          {busy ? (view.dryRun ? 'Simulating…' : 'Queueing…') : view.dryRun ? 'Simulate' : 'Remove selected'}
        </button>
      {/if}
    {/if}
  {/snippet}
</Modal>

{#snippet ownFile(f: GroupFile)}
  {@const state = view ? physicalState(view.selection, f.physicalKey, actions) : null}
  <label class="inline removal-file">
    <input type="checkbox" checked={state ? state.checked : f.selected} use:indeterminate={state?.indeterminate ?? false} onchange={(e) => set([{ physicalKey: f.physicalKey, initial: f.selected }], e.currentTarget.checked)} />
    <span><strong class="managed-name" title={f.filename}>{f.filename}</strong> <span class="muted">{f.label && f.label !== f.filename ? `${f.label} · ` : ''}{f.size}</span></span>
  </label>
{/snippet}
