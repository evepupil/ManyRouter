export interface ScoreWindowEvidence {
  id: string;
  window: string;
  attempts: number;
  successes: number;
  recoveryMillis: number;
  complete: boolean;
  ttftP50?: number;
  ttftP95?: number;
}

export function scoreWindowEvidence(value: unknown): ScoreWindowEvidence[] {
  if (!isRecord(value) || !Array.isArray(value.windows)) return [];
  return value.windows.flatMap((entry): ScoreWindowEvidence[] => {
    if (!isRecord(entry) || typeof entry.window !== "string") return [];
    return [
      {
        id: entry.window,
        window: entry.window,
        attempts: finiteNumber(entry.sla_attempts),
        successes: finiteNumber(entry.successes),
        recoveryMillis: finiteNumber(entry.recovery_ms),
        complete: entry.complete === true,
        ttftP50: metricValue(entry.latency, "ttft_p50_ms"),
        ttftP95: metricValue(entry.latency, "ttft_p95_ms"),
      },
    ];
  });
}

function metricValue(value: unknown, metric: string): number | undefined {
  if (!isRecord(value) || !isRecord(value.score)) return undefined;
  const components = value.score.components;
  if (!Array.isArray(components)) return undefined;
  for (const component of components) {
    if (
      isRecord(component) &&
      component.metric === metric &&
      typeof component.raw_value === "number" &&
      Number.isFinite(component.raw_value)
    ) {
      return component.raw_value;
    }
  }
  return undefined;
}

function finiteNumber(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
