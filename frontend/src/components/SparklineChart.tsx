/* SparklineChart — tiny inline area chart for the /dashboard 24h load.
 *
 * Conventional sparkline rules: no axes, no legend, no grid. Just the
 * shape. A single horizontal baseline at zero is implicit through the
 * fill area. Tooltip on hover shows the exact value per bucket.
 *
 * The chart is intentionally trivial — when we need rich axes, we use
 * LineChart from @visx; sparklines should never grow beyond their
 * one-cell role.
 */
export type SparkPoint = { label: string; value: number };

export function SparklineChart({
  points,
  width = 220,
  height = 36,
  format = (v: number) => v.toLocaleString(),
}: {
  points: SparkPoint[];
  width?: number;
  height?: number;
  format?: (v: number) => string;
}) {
  if (points.length === 0) {
    return (
      <span className="mono" style={{ color: "var(--text-tertiary)", fontSize: 11 }}>
        no data
      </span>
    );
  }
  const max = Math.max(1, ...points.map((p) => p.value));
  const stepX = points.length > 1 ? width / (points.length - 1) : width;
  const path = points
    .map((p, i) => {
      const x = i * stepX;
      const y = height - (p.value / max) * (height - 2) - 1;
      return `${i === 0 ? "M" : "L"} ${x.toFixed(1)} ${y.toFixed(1)}`;
    })
    .join(" ");
  // Closed path for fill below the line. Add the bottom-right and
  // bottom-left corners so the polygon wraps.
  const area = `${path} L ${(points.length - 1) * stepX} ${height} L 0 ${height} Z`;

  return (
    <svg width={width} height={height} role="img" aria-label="24h load sparkline">
      <path d={area} fill="var(--accent-soft)" />
      <path d={path} stroke="var(--accent)" strokeWidth={1.5} fill="none" />
      {/* Invisible hover targets — one per bucket. SVG title is the
          native tooltip; no JS needed. */}
      {points.map((p, i) => (
        <rect
          key={i}
          x={i * stepX - stepX / 2}
          y={0}
          width={stepX}
          height={height}
          fill="transparent"
        >
          <title>{`${p.label}: ${format(p.value)}`}</title>
        </rect>
      ))}
    </svg>
  );
}
