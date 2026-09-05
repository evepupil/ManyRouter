let csrfToken = "";

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
    readonly requestId?: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export function setCsrfToken(value: string): void {
  csrfToken = value;
}

export async function request<T>(
  path: string,
  options: {
    method?: string;
    body?: unknown;
    signal?: AbortSignal;
    idempotencyKey?: string;
  } = {},
): Promise<T> {
  const method = options.method ?? "GET";
  const headers = new Headers({ Accept: "application/json" });
  if (options.body !== undefined)
    headers.set("Content-Type", "application/json");
  if (method !== "GET") {
    headers.set(
      "Idempotency-Key",
      options.idempotencyKey ?? crypto.randomUUID(),
    );
    if (csrfToken) headers.set("X-CSRF-Token", csrfToken);
  }
  let response: Response;
  try {
    response = await fetch(`/api/v1${path}`, {
      method,
      headers,
      credentials: "same-origin",
      body:
        options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError")
      throw error;
    throw new ApiError("无法连接服务，请检查网络后重试", 0, "network_error");
  }
  if (!response.ok) {
    let message = `请求失败（${response.status}）`;
    let code = "request_failed";
    let requestId: string | undefined;
    try {
      const data: unknown = await response.json();
      if (data && typeof data === "object") {
        if ("message" in data && typeof data.message === "string")
          message = data.message;
        if ("code" in data && typeof data.code === "string") code = data.code;
        if ("request_id" in data && typeof data.request_id === "string")
          requestId = data.request_id;
      }
    } catch {
      // The service can return an empty response while restarting.
    }
    if (response.status === 401 && !path.startsWith("/auth/")) {
      window.dispatchEvent(new Event("manyrouter:session-expired"));
    }
    throw new ApiError(message, response.status, code, requestId);
  }
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

export function queryString(
  values: Record<string, string | number | undefined>,
): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const encoded = params.toString();
  return encoded ? `?${encoded}` : "";
}
