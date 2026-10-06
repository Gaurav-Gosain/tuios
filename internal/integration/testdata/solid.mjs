// The Node plugin drivers supply the reactive host boundary. flush() stands
// for Solid rerunning effects when the driver's route changes; real-client
// smoke tests use OpenCode's Solid runtime instead.
const effects = new Set();
let owner;
export function createRoot(fn) {
  const previous = owner;
  const owned = [];
  owner = owned;
  try { return fn(() => owned.forEach((effect) => effects.delete(effect))); }
  finally { owner = previous; }
}
export function createEffect(fn) {
  effects.add(fn);
  owner?.push(fn);
  fn();
}
export function flush() { for (const effect of effects) effect(); }
