<!-- Making the admin account, the first time, or signing in. -->
<script lang="ts">
  import { Single, Card, Field, Status } from '@overfin/ui';
  import { post } from '../lib/api';

  let { setup, done }: { setup: boolean; done: () => void } = $props();

  let username = $state('');
  let password = $state('');
  let confirm = $state('');
  let busy = $state(false);
  let failure = $state('');

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    failure = '';
    try {
      if (setup) await post('/api/setup', { username, password, confirm });
      else await post('/api/login', { username, password });
      done();
    } catch (err) {
      failure = (err as Error).message;
    } finally {
      busy = false;
    }
  }
</script>

<Single name="Overtide" logo="/logo.svg">
  <Card title={setup ? 'Welcome to Overtide' : 'Sign in'} description={setup ? 'Create the admin account used to sign in. There is only ever one — no invitations, no roles.' : 'Sign in to continue.'}>
    <form id="sign-in" class="modal-form" onsubmit={submit}>
      <Field label="Username"><input bind:value={username} autocomplete="username" required /></Field>
      <Field label="Password"><input type="password" bind:value={password} autocomplete={setup ? 'new-password' : 'current-password'} minlength={setup ? 8 : undefined} required /></Field>
      {#if setup}
        <Field label="Confirm password"><input type="password" bind:value={confirm} autocomplete="new-password" minlength="8" required /></Field>
      {/if}
    </form>
    {#snippet foot()}
      <Status text={failure} kind={failure ? 'error' : ''} />
      <button type="submit" form="sign-in" class="primary" disabled={busy}>{setup ? 'Create account' : 'Sign in'}</button>
    {/snippet}
  </Card>
</Single>
