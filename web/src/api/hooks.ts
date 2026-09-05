import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { request, queryString } from "./client";
import type { ListResponse } from "./types";
import { useRef } from "react";
import {
  confirmRequestAttempt,
  prepareRequestAttempt,
  type ActionRequest,
  type RequestAttempt,
} from "./request-attempt";

export function useList<T>(
  resource: string,
  params: Record<string, string | number | undefined> = {},
  options: { enabled?: boolean; poll?: boolean } = {},
) {
  const filters = { limit: 20, ...params };
  return useQuery({
    queryKey: ["ops", resource, filters],
    queryFn: ({ signal }) =>
      request<ListResponse<T>>(`/ops/${resource}${queryString(filters)}`, {
        signal,
      }),
    enabled: options.enabled ?? true,
    refetchInterval: options.poll ? 5000 : false,
  });
}

export function useAction(onSuccess?: () => void) {
  const client = useQueryClient();
  const pendingAttempt = useRef<RequestAttempt | null>(null);
  return useMutation({
    mutationFn: async (action: ActionRequest) => {
      const attempt = prepareRequestAttempt(
        pendingAttempt.current,
        action,
        () => crypto.randomUUID(),
      );
      pendingAttempt.current = attempt;
      const result = await request<unknown>(`/ops/${action.path}`, {
        method: action.method ?? "POST",
        body: action.body,
        idempotencyKey: attempt.key,
      });
      pendingAttempt.current = confirmRequestAttempt(
        pendingAttempt.current,
        attempt.key,
      );
      return result;
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["ops"] });
      onSuccess?.();
    },
  });
}
