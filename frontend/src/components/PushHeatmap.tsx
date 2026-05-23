import { useLocale, useT } from "@/i18n";
import type { PushRecommendations, PushRecCell } from "@/api/repos";
import { formatDuration } from "@/lib/format";

/* PushHeatmap — "when to push" per-repo 24×7 grid.
 *
 * Each cell is one (hour, day-of-week) bucket; colour is a DIVERGING
 * scale (green ↔ neutral ↔ red) on `total_delta_pct` — signed % vs
 * the repo's overall mean end-to-end time. Negative = faster than mean
 * = good to push then; positive = avoid this slot.
 *
 * Cells with sample_count below confidenceThreshold are rendered with
 * a muted alpha and dashed border to surface low-confidence buckets
 * (otherwise a single 1s job at 04:00 Sunday looks like the optimal
 * push time). Cells with no data at all are omitted from the API and
 * draw as a flat inset placeholder.
 *
 * Day labels are produced via Intl.DateTimeFormat in the active locale,
 * so the Russian build shows "пн вт ср …" automatically without an
 * i18n string for each abbreviation.
 *
 * Used on /datasets/:id below the existing per-repo statistics blocks.
 */
type Props = {
  data: PushRecommendations;
  cellSize?: number;
  confidenceThreshold?: number;
};

const HOURS = Array.from({ length: 24 }, (_, i) => i);
const DAYS  = Array.from({ length: 7  }, (_, i) => i); // 0 = Mon

export function PushHeatmap({ data, cellSize = 26, confidenceThreshold = 3 }: Props) {
  const t = useT();
  const { locale } = useLocale();

  // Sparse → dense lookup keyed by `dow|hour`.
  const lookup = new Map<string, PushRecCell>();
  for (const c of data.cells) lookup.set(`${c.dow}|${c.hour}`, c);

  const labelWidth = 36;
  const tickHeight = 18;
  const gridWidth  = HOURS.length * cellSize;
  const gridHeight = DAYS.length * cellSize;
  const width  = labelWidth + gridWidth + 4;
  const height = tickHeight + gridHeight + 4;

  // Localised short day names — `weekday: "short"` produces "Mon", "пн"
  // depending on locale. We pin the date to a known Monday and walk
  // forward so the labels match our Mon=0..Sun=6 indexing.
  const dayLabels = useDayLabels(locale);

  // Best / worst callouts come from the API but the format helper lives
  // here so the rendering stays self-contained.
  const best = data.best;
  const worst = data.worst;

  return (
    <div>
      <Caption data={data} t={t} dayLabels={dayLabels} best={best} worst={worst} />

      <div style={{ overflowX: "auto", marginTop: "var(--s-3)" }}>
        <svg width={width} height={height} role="img" aria-label="push recommendations heatmap">
          {/* Hour ticks across the top */}
          <g>
            {HOURS.map((h) => (
              <text
                key={h}
                x={labelWidth + h * cellSize + cellSize / 2}
                y={tickHeight - 6}
                textAnchor="middle"
                fontFamily="var(--font-mono)"
                fontSize={10}
                fill="var(--text-tertiary)"
              >
                {h.toString().padStart(2, "0")}
              </text>
            ))}
          </g>

          {/* Rows: one per day-of-week */}
          {DAYS.map((d) => (
            <g key={d} transform={`translate(0, ${tickHeight + d * cellSize})`}>
              <text
                x={labelWidth - 6}
                y={cellSize / 2 + 4}
                textAnchor="end"
                fontFamily="var(--font-mono)"
                fontSize={11}
                fill="var(--text-secondary)"
              >
                {dayLabels[d]}
              </text>
              {HOURS.map((h) => {
                const cell = lookup.get(`${d}|${h}`);
                const x = labelWidth + h * cellSize;
                if (!cell) {
                  return (
                    <rect
                      key={h}
                      x={x + 1} y={1}
                      width={cellSize - 2} height={cellSize - 2}
                      fill="var(--bg-inset)" rx={2}
                    >
                      <title>{t("datasets.pushrec.tooltip_empty")}</title>
                    </rect>
                  );
                }
                const lowConf = cell.sample_count < confidenceThreshold;
                const fill = divergingColour(cell.total_delta_pct, lowConf);
                return (
                  <rect
                    key={h}
                    x={x + 1} y={1}
                    width={cellSize - 2} height={cellSize - 2}
                    fill={fill}
                    stroke={lowConf ? "var(--border-subtle)" : "transparent"}
                    strokeDasharray={lowConf ? "2 2" : undefined}
                    rx={2}
                  >
                    <title>
                      {t("datasets.pushrec.tooltip", {
                        day: dayLabels[d],
                        hour: h.toString().padStart(2, "0"),
                        n: cell.sample_count,
                        total: formatDuration(cell.mean_total_sec),
                        wait: formatDuration(cell.mean_wait_sec),
                        delta: formatSignedPct(cell.total_delta_pct),
                      })}
                    </title>
                  </rect>
                );
              })}
            </g>
          ))}
        </svg>
      </div>

      <Legend t={t} />
    </div>
  );
}

/* Caption — sample count, window, and best/worst callouts. */
function Caption({
  data,
  t,
  dayLabels,
  best,
  worst,
}: {
  data: PushRecommendations;
  t: ReturnType<typeof useT>;
  dayLabels: string[];
  best: PushRecommendations["best"];
  worst: PushRecommendations["worst"];
}) {
  return (
    <div
      style={{
        display: "flex",
        flexWrap: "wrap",
        alignItems: "center",
        gap: "var(--s-4)",
        fontSize: "var(--fs-12)",
        color: "var(--text-tertiary)",
        fontFamily: "var(--font-mono)",
      }}
    >
      <span>
        {t("datasets.pushrec.window", {
          days: data.days,
          n: data.overall.sample_count.toLocaleString(),
        })}
      </span>
      <span>
        {t("datasets.pushrec.mean_total", {
          v: formatDuration(data.overall.mean_total_sec),
        })}
      </span>
      <span>tz: {data.tz}</span>
      {best && (
        <span style={{ color: "var(--ok)" }}>
          {t("datasets.pushrec.best", {
            day: dayLabels[best.dow],
            hour: best.hour.toString().padStart(2, "0"),
            delta: formatSignedPct(best.total_delta_pct),
          })}
        </span>
      )}
      {worst && (
        <span style={{ color: "var(--err)" }}>
          {t("datasets.pushrec.worst", {
            day: dayLabels[worst.dow],
            hour: worst.hour.toString().padStart(2, "0"),
            delta: formatSignedPct(worst.total_delta_pct),
          })}
        </span>
      )}
    </div>
  );
}

/* Diverging colour scale. Saturation is clipped to [-60%, +60%] —
 * anything past that is "really fast" / "really slow" but visually
 * indistinguishable. The accent palette is ok-green ↔ neutral ↔ err-red
 * matching the rest of the system.
 */
function divergingColour(delta: number, lowConf: boolean): string {
  const SATURATION_CLIP = 60;
  const norm = Math.max(-1, Math.min(1, delta / SATURATION_CLIP));
  const baseAlpha = lowConf ? 0.18 : 0.85;
  if (Math.abs(norm) < 0.05) {
    return `rgba(155, 161, 166, ${lowConf ? 0.08 : 0.15})`;
  }
  if (norm < 0) {
    // green for faster-than-mean
    const a = lowConf ? baseAlpha : 0.2 + (-norm) * 0.65;
    return `rgba(74, 222, 128, ${a})`;
  }
  // red for slower-than-mean
  const a = lowConf ? baseAlpha : 0.2 + norm * 0.65;
  return `rgba(248, 113, 113, ${a})`;
}

/* Legend strip — fixed examples on the diverging scale. */
function Legend({ t }: { t: (k: any, v?: any) => string }) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: "var(--s-2)",
        marginTop: "var(--s-3)",
        fontFamily: "var(--font-mono)",
        fontSize: "var(--fs-12)",
        color: "var(--text-tertiary)",
      }}
    >
      <span>{t("datasets.pushrec.legend.faster")}</span>
      <Swatch delta={-50} />
      <Swatch delta={-25} />
      <Swatch delta={0} />
      <Swatch delta={25} />
      <Swatch delta={50} />
      <span>{t("datasets.pushrec.legend.slower")}</span>
    </div>
  );
}

function Swatch({ delta }: { delta: number }) {
  return (
    <span
      style={{
        display: "inline-block",
        width: 14,
        height: 14,
        borderRadius: 2,
        background: divergingColour(delta, false),
      }}
      title={formatSignedPct(delta)}
    />
  );
}

function formatSignedPct(v: number): string {
  if (!Number.isFinite(v)) return "—";
  const sign = v >= 0 ? "+" : "";
  return `${sign}${v.toFixed(0)}%`;
}

/* Cache day labels per locale — building seven Date instances per
 * render is cheap but unnecessary. */
function useDayLabels(locale: string): string[] {
  // Memoise via module-level cache — useMemo would be equivalent but
  // the value only depends on locale string so a manual map is
  // simpler and saves the dep-array.
  if (!dayLabelCache.has(locale)) {
    const fmt = new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", {
      weekday: "short",
    });
    // 2024-01-01 was a Monday — use it as the index-0 anchor.
    const monday = new Date(Date.UTC(2024, 0, 1));
    const labels = DAYS.map((i) => {
      const d = new Date(monday.getTime() + i * 86_400_000);
      return fmt.format(d).replace(/[.,]/g, "");
    });
    dayLabelCache.set(locale, labels);
  }
  return dayLabelCache.get(locale)!;
}
const dayLabelCache = new Map<string, string[]>();
