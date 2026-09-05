interface SelectableSite {
  id: string;
  status: string;
}

export function resolveDeploymentSelection(
  sites: readonly SelectableSite[],
  selected: readonly string[] | null,
  initialSiteID: string,
): string[] {
  const enabled = new Set(
    sites.filter((site) => site.status === "enabled").map((site) => site.id),
  );
  return [...new Set(selected ?? [initialSiteID])].filter((id) =>
    enabled.has(id),
  );
}
