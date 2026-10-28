// The app's own confirmation dialog, used instead of the browser's
// window.confirm: same look as every other modal, built here in the
// browser (no server round trip), and stacked above whatever modal is
// already open — it never touches #modal-root, so cancelling leaves the
// dialog underneath exactly as it was.

export interface ConfirmOptions {
  title: string;
  message: string;
  confirmLabel: string;
  danger: boolean;
  opener: HTMLElement | null;
}

let open = false;

// Resolves true when the confirm button is pressed, false on Cancel,
// Escape or a click on the backdrop. Only one confirmation shows at a time:
// a second request while one is open is refused (false).
export function confirmDialog(options: ConfirmOptions): Promise<boolean> {
  if (open) return Promise.resolve(false);
  open = true;

  const overlay = document.createElement("div");
  overlay.className = "modal-overlay confirm-overlay";
  const dialog = document.createElement("main");
  dialog.className = "modal-dialog confirm-dialog";
  dialog.setAttribute("role", "alertdialog");
  dialog.setAttribute("aria-modal", "true");
  dialog.setAttribute("aria-labelledby", "confirm-dialog-title");
  dialog.setAttribute("aria-describedby", "confirm-dialog-message");
  dialog.tabIndex = -1;

  const top = document.createElement("div");
  top.className = "top";
  const title = document.createElement("h2");
  title.id = "confirm-dialog-title";
  title.textContent = options.title;
  top.append(title);

  const message = document.createElement("p");
  message.id = "confirm-dialog-message";
  message.textContent = options.message;

  const actions = document.createElement("div");
  actions.className = "actions";
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.textContent = "Cancel";
  const confirm = document.createElement("button");
  confirm.type = "button";
  confirm.textContent = options.confirmLabel;
  if (options.danger) confirm.className = "danger";
  actions.append(cancel, confirm);

  dialog.append(top, message, actions);
  overlay.append(dialog);

  const hadModalOpen = document.body.classList.contains("modal-open");
  document.body.classList.add("modal-open");
  document.body.append(overlay);
  // Cancel gets the focus: Enter or Space on a dialog that just appeared
  // must not carry out a removal by accident.
  cancel.focus();

  return new Promise(resolve => {
    const finish = (confirmed: boolean) => {
      overlay.remove();
      if (!hadModalOpen) document.body.classList.remove("modal-open");
      open = false;
      if (options.opener && document.contains(options.opener)) options.opener.focus();
      resolve(confirmed);
    };
    cancel.addEventListener("click", () => finish(false));
    confirm.addEventListener("click", () => finish(true));
    overlay.addEventListener("click", event => {
      if (event.target === overlay) finish(false);
    });
    // On the dialog itself, and stopped there: a modal underneath that
    // closes on Escape (removal.ts listens on document) must stay open.
    dialog.addEventListener("keydown", event => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        finish(false);
        return;
      }
      if (event.key === "Tab") {
        // Keep the focus inside the dialog: two buttons, so Tab and
        // Shift+Tab just move between them.
        event.preventDefault();
        (document.activeElement === cancel ? confirm : cancel).focus();
      }
    });
  });
}
