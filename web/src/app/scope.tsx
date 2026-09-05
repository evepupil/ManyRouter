import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { useList } from "../api/hooks";
import type { Site } from "../api/types";
import { resolveScopeState, type ScopeState } from "./scope-state";

interface SiteScope {
  siteId: string;
  setSiteId: (id: string) => void;
  sites: Site[];
  site?: Site;
  state: ScopeState;
  error: Error | null;
  retry: () => void;
  fetching: boolean;
}
const ScopeContext = createContext<SiteScope | null>(null);

export function ScopeProvider({ children }: { children: ReactNode }) {
  const [siteId, setSiteId] = useState("");
  const query = useList<Site>("sites", { limit: 100 });
  const sites = query.data?.items ?? [];
  useEffect(() => {
    if (!siteId && sites[0]) setSiteId(sites[0].id);
    if (siteId && query.isSuccess && !sites.some((site) => site.id === siteId))
      setSiteId("");
  }, [siteId, sites, query.isSuccess]);
  return (
    <ScopeContext.Provider
      value={{
        siteId,
        setSiteId,
        sites,
        site: sites.find((site) => site.id === siteId),
        state: resolveScopeState(
          query.isPending,
          query.isError,
          sites.length,
          siteId,
        ),
        error: query.error,
        retry: () => {
          void query.refetch();
        },
        fetching: query.isFetching,
      }}
    >
      {children}
    </ScopeContext.Provider>
  );
}

export function useScope(): SiteScope {
  const scope = useContext(ScopeContext);
  if (!scope) throw new Error("站点范围不可用");
  return scope;
}
