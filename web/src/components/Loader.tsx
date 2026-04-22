// Loader is the shared pending-state placeholder — used by LazyPanel
// while a dynamic-imported panel resolves, and re-usable by panels for
// their initial fetch. Kept deliberately minimal to avoid CLS: one line
// of text, no spinner, no reserved height box.
export function Loader() {
  return <p class="statnive-loading">loading…</p>;
}
