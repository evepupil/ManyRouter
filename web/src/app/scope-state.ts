export type ScopeState = "loading" | "error" | "empty" | "selection" | "ready";

export function resolveScopeState(
  pending: boolean,
  failed: boolean,
  siteCount: number,
  selected: string,
): ScopeState {
  if (failed) return "error";
  if (pending) return "loading";
  if (siteCount === 0) return "empty";
  return selected ? "ready" : "selection";
}
