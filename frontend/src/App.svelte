<!-- Overtide's panel: setting up, signing in, then its pages, with the live
     updates, the operations in progress and the removal window. -->
<script lang="ts">
  import { Shell, Dialogs, Toasts, Loading, Notice, toast } from '@overfin/ui';
  import type { NavGroup } from '@overfin/ui';
  import { get, post, onSignedOut } from './lib/api';
  import { live, startLive, applyUpdates } from './lib/live.svelte';
  import { route } from './lib/router.svelte';
  import { removal, closeRemoval } from './lib/removal.svelte';
  import SignIn from './pages/SignIn.svelte';
  import Home from './pages/Home.svelte';
  import Library from './pages/Library.svelte';
  import Media from './pages/Media.svelte';
  import Torrents from './pages/Torrents.svelte';
  import Torrent from './pages/Torrent.svelte';
  import Unmanaged from './pages/Unmanaged.svelte';
  import Storage from './pages/Storage.svelte';
  import Services from './pages/Services.svelte';
  import Tasks from './pages/Tasks.svelte';
  import History from './pages/History.svelte';
  import Settings from './pages/Settings.svelte';
  import RemovalWindow from './parts/removal/RemovalWindow.svelte';

  interface Session {
    needsSetup: boolean;
    signedIn: boolean;
    username?: string;
  }

  const icons: Record<string, string[]> = {
    home: ['M3 11l9-7 9 7', 'M5 10v10h14V10', 'M10 20v-6h4v6'],
    library: ['M4 5h16v14H4z', 'M4 9h16', 'M9 5v14'],
    torrents: ['M12 4v11', 'M7 10l5 5 5-5', 'M5 20h14'],
    unmanaged: ['M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z', 'M12 11v3', 'M12 16.5h.01'],
    storage: ['M4 5h16v6H4z', 'M4 13h16v6H4z', 'M8 8h.01', 'M8 16h.01'],
    services: ['M9 7V3', 'M15 7V3', 'M7 7h10v4a5 5 0 0 1-10 0z', 'M12 16v5'],
    tasks: ['M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z', 'M12 7v5l3 2'],
    history: ['M4 6h16', 'M4 12h16', 'M4 18h10'],
    settings: ['M4 6h10', 'M18 6h2', 'M4 12h4', 'M12 12h8', 'M4 18h12', 'M16 4v4', 'M10 10v4', 'M18 16v4'],
  };
  const nav: NavGroup[] = [
    { items: [{ href: '/', label: 'Home', icon: icons.home }] },
    { label: 'Media', items: [
      { href: '/library', label: 'Library', icon: icons.library },
      { href: '/torrents', label: 'Torrents', icon: icons.torrents },
      { href: '/unmanaged', label: 'Unmanaged files', icon: icons.unmanaged },
    ] },
    { label: 'Server', items: [
      { href: '/storage', label: 'Storage', icon: icons.storage },
      { href: '/services', label: 'Services', icon: icons.services },
      { href: '/tasks', label: 'Tasks', icon: icons.tasks },
      { href: '/history', label: 'History', icon: icons.history },
      { href: '/settings', label: 'Settings', icon: icons.settings },
    ] },
  ];

  let session = $state<Session | null>(null);
  let problem = $state('');

  // The page an address shows, and the menu entry that stays lit for it.
  const page = $derived.by(() => {
    const p = route.path;
    if (p === '/' || p === '') return { show: Home, active: '/' };
    if (p === '/library') return { show: Library, active: '/library' };
    if (p.startsWith('/library/') || p.startsWith('/media/')) return { show: Media, active: '/library' };
    if (p === '/torrents') return { show: Torrents, active: '/torrents' };
    if (p.startsWith('/torrents/')) return { show: Torrent, active: '/torrents' };
    if (p === '/unmanaged') return { show: Unmanaged, active: '/unmanaged' };
    if (p === '/storage') return { show: Storage, active: '/storage' };
    if (p === '/services') return { show: Services, active: '/services' };
    if (p === '/tasks') return { show: Tasks, active: '/tasks' };
    if (p === '/history') return { show: History, active: '/history' };
    if (p === '/settings') return { show: Settings, active: '/settings' };
    return { show: Home, active: '/' };
  });
  const Page = $derived(page.show);

  async function load() {
    try {
      session = await get<Session>('/api/session');
      problem = '';
      if (session.signedIn) startLive();
    } catch (err) {
      problem = (err as Error).message;
    }
  }

  onSignedOut(() => {
    if (session?.signedIn) toast('Signed out: sign in again.', true);
    session = { needsSetup: false, signedIn: false };
  });

  async function signOut() {
    try {
      await post('/api/logout');
    } finally {
      session = { needsSetup: false, signedIn: false };
    }
  }

  // A new page starts with nothing pending.
  $effect(() => {
    void route.path;
    live.updates = false;
  });

  load();
</script>

{#if problem}
  <main class="single"><Notice warn>{problem}</Notice><button type="button" onclick={load}>Try again</button></main>
{:else if !session}
  <main class="single"><Loading /></main>
{:else if !session.signedIn}
  <SignIn setup={session.needsSetup} done={load} />
{:else}
  <Shell name="Overtide" subtitle="Storage" logo="/logo.svg" {nav} active={page.active} who={session.username} {signOut} wide>
    {#snippet extra()}
      {#if live.pendingOperations}
        <a class="operations" href="/tasks">{live.pendingOperations === 1 ? '1 operation in progress' : `${live.pendingOperations} operations in progress`}</a>
      {/if}
      {#each live.notices as n (n.id)}
        <a class="failure" href={`/history#operation-${n.id}`}>{n.label || 'Removal'}: {n.message || n.status}</a>
      {/each}
    {/snippet}
    {#if live.updates}
      <div class="updates-bar">
        <Notice>This page has changed since you opened it.</Notice>
        <button type="button" class="primary" onclick={applyUpdates}>Updates available</button>
      </div>
    {/if}
    {#key route.path}<Page />{/key}
  </Shell>
  {#if removal.request}
    <RemovalWindow request={removal.request} close={closeRemoval} />
  {/if}
{/if}

<Dialogs />
<Toasts />
