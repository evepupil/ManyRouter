export interface ActionRequest {
  path: string;
  method?: string;
  body?: unknown;
}
export interface RequestAttempt {
  signature: string;
  key: string;
}

export function prepareRequestAttempt(
  previous: RequestAttempt | null,
  action: ActionRequest,
  newKey: () => string,
): RequestAttempt {
  const signature = JSON.stringify({
    path: action.path,
    method: (action.method ?? "POST").toUpperCase(),
    body: action.body,
  });
  return previous?.signature === signature
    ? previous
    : { signature, key: newKey() };
}

export function confirmRequestAttempt(
  current: RequestAttempt | null,
  confirmedKey: string,
): RequestAttempt | null {
  return current?.key === confirmedKey ? null : current;
}
