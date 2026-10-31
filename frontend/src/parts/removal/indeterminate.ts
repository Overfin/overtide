// A checkbox's "some of them" state, which HTML has no attribute for.
export function indeterminate(node: HTMLInputElement, value: boolean) {
  node.indeterminate = value;
  return {
    update(next: boolean) {
      node.indeterminate = next;
    },
  };
}
