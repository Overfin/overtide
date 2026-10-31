// The panel's pages, by address: /, /library, /library/<…>, /torrents,
// /torrents/<hash>, /unmanaged, /storage, /services, /tasks, /history,
// /settings. A click on one of the panel's own links changes the page
// without reloading it; the query string is the page's own (filters).
export const route = $state({ path: location.pathname, search: location.search, hash: location.hash });

export function go(url: string, replace = false) {
  const next = new URL(url, location.href);
  if (next.pathname === route.path && next.search === route.search && next.hash === route.hash) return;
  if (replace) history.replaceState(null, '', next);
  else history.pushState(null, '', next);
  const samePage = next.pathname === route.path;
  route.path = next.pathname;
  route.search = next.search;
  route.hash = next.hash;
  if (!samePage) window.scrollTo(0, 0);
}

/** params are the address's query, for a page's filters. */
export const params = () => new URLSearchParams(route.search);

addEventListener('popstate', () => {
  route.path = location.pathname;
  route.search = location.search;
  route.hash = location.hash;
});

addEventListener('click', (event) => {
  if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  const link = (event.target as Element).closest?.('a[href]') as HTMLAnchorElement | null;
  if (!link || link.target || link.hasAttribute('download') || link.origin !== location.origin) return;
  if (link.pathname.startsWith('/api/') || link.pathname.startsWith('/ui/')) return;
  event.preventDefault();
  go(link.pathname + link.search + link.hash);
});
