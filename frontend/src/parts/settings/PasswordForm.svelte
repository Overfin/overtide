<!-- Changing the admin password: every other browser is signed out, this
     one stays signed in. -->
<script lang="ts">
  import { FormModal, Field } from '@overfin/ui';
  import { put } from '../../lib/api';

  let { saved, close }: { saved: () => void; close: () => void } = $props();

  let current = $state('');
  let fresh = $state('');
  let confirm = $state('');

  async function save() {
    await put('/api/settings/password', { current, new: fresh, confirm });
    saved();
  }
</script>

<FormModal title="Change password" saveLabel="Update password" {save} {close}>
  <Field label="Current password"><input type="password" bind:value={current} required autocomplete="current-password" /></Field>
  <Field label="New password"><input type="password" bind:value={fresh} required minlength="8" autocomplete="new-password" /></Field>
  <Field label="Confirm new password"><input type="password" bind:value={confirm} required minlength="8" autocomplete="new-password" /></Field>
</FormModal>
