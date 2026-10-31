// Overtide's API, as the panel asks it: JSON both ways. A failure throws
// an Error whose message is what the server said.

/** signedOut is called when the session ended while the panel was open. */
let signedOut = () => {};

export function onSignedOut(callback: () => void) {
  signedOut = callback;
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, headers: {} as Record<string, string>, cache: 'no-store' };
  if (body !== undefined) {
    (init.headers as Record<string, string>)['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body);
  }
  const response = await fetch(path, init);
  const answer = await response.json().catch(() => ({}));
  if (!response.ok) {
    // A wrong password at sign-in is 401 too, but not a session ending.
    if (response.status === 401 && path !== '/api/login') signedOut();
    throw new Error(answer.error || `Overtide answered ${response.status}.`);
  }
  return answer as T;
}

export const get = <T>(path: string) => call<T>('GET', path);
export const post = <T = unknown>(path: string, body: unknown = {}) => call<T>('POST', path, body);
export const put = <T = unknown>(path: string, body: unknown) => call<T>('PUT', path, body);
export const del = <T = unknown>(path: string) => call<T>('DELETE', path);
