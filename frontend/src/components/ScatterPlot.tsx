import { Group } from "@visx/group";
import { scaleLinear } from "@visx/scale";
import { AxisBottom, AxisLeft } from "@visx/axis";

/* Predicted-vs-actual scatter.
 *
 * Visual choices:
 *   - Log-log scale by default. CI durations span 5s → 3000s; linear axes
 *     hide all the action near the origin under a single dot cloud.
 *   - Diagonal y=x reference line. A perfect model has every point on it.
 *   - Tiny semi-transparent dots — with 500+ points overlap is the
 *     dominant readability concern.
 *
 * `points` are pre-fetched and passed in; the parent handles loading
 * states and empty-data fallbacks.
 */
export type ScatterPoint = { x: number; y: number };

export function ScatterPlot({
  points,
  width = 480,
  height = 360,
  xLabel = "actual (sec)",
  yLabel = "predicted (sec)",
}: {
  points: ScatterPoint[];
  width?: number;
  height?: number;
  xLabel?: string;
  yLabel?: string;
}) {
  const margin = { top: 16, right: 24, bottom: 36, left: 56 };
  const innerW = width - margin.left - margin.right;
  const innerH = height - margin.top - margin.bottom;

  let lo = Infinity, hi = -Infinity;
  for (const p of points) {
    if (p.x > 0 && p.x < lo) lo = p.x;
    if (p.y > 0 && p.y < lo) lo = p.y;
    if (p.x > hi) hi = p.x;
    if (p.y > hi) hi = p.y;
  }
  if (!isFinite(lo)) { lo = 1; hi = 100; }
  if (lo <= 0) lo = 1;
  if (hi <= lo) hi = lo * 10;

  // Log scale base 10 — clamp via Math.log10 since visx's scaleLog
  // pre-Bezier 5 was finicky with negative values. Custom log mapping
  // gives us full control over edge cases.
  const logLo = Math.log10(lo);
  const logHi = Math.log10(hi);

  const xScale = scaleLinear({ domain: [logLo, logHi], range: [0, innerW] });
  const yScale = scaleLinear({ domain: [logLo, logHi], range: [innerH, 0] });

  // 4-5 nice powers-of-10 ticks across the range.
  const tickPows: number[] = [];
  for (let p = Math.ceil(logLo); p <= Math.floor(logHi); p++) tickPows.push(p);

  return (
    <svg width={width} height={height} role="img" aria-label="predicted vs actual scatter">
      <Group left={margin.left} top={margin.top}>
        {/* Reference y=x diagonal */}
        <line
          x1={xScale(logLo)}
          y1={yScale(logLo)}
          x2={xScale(logHi)}
          y2={yScale(logHi)}
          stroke="var(--border-strong)"
          strokeDasharray="4 4"
          strokeWidth={1}
        />
        {/* Points */}
        {points.map((p, i) => {
          if (p.x <= 0 || p.y <= 0) return null;
          return (
            <circle
              key={i}
              cx={xScale(Math.log10(p.x))}
              cy={yScale(Math.log10(p.y))}
              r={2}
              fill="var(--accent)"
              fillOpacity={0.5}
            />
          );
        })}
        <AxisBottom
          top={innerH}
          scale={xScale}
          tickValues={tickPows}
          tickFormat={(v) => formatPow(Number(v))}
          stroke="var(--border-strong)"
          tickStroke="var(--border-subtle)"
          tickLabelProps={() => ({
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            fill: "var(--text-tertiary)",
            textAnchor: "middle",
            dy: "0.5em",
          })}
        />
        <AxisLeft
          scale={yScale}
          tickValues={tickPows}
          tickFormat={(v) => formatPow(Number(v))}
          stroke="var(--border-strong)"
          tickStroke="var(--border-subtle)"
          numTicks={4}
          tickLabelProps={() => ({
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            fill: "var(--text-tertiary)",
            textAnchor: "end",
            dx: "-0.3em",
            dy: "0.3em",
          })}
        />
        <text
          x={innerW / 2}
          y={innerH + 28}
          textAnchor="middle"
          fontFamily="var(--font-mono)"
          fontSize={10}
          fill="var(--text-tertiary)"
        >
          {xLabel}
        </text>
        <text
          x={-innerH / 2}
          y={-44}
          transform="rotate(-90)"
          textAnchor="middle"
          fontFamily="var(--font-mono)"
          fontSize={10}
          fill="var(--text-tertiary)"
        >
          {yLabel}
        </text>
      </Group>
    </svg>
  );
}

function formatPow(p: number): string {
  // 10^p as a compact label. 10⁰=1, 10¹=10, 10²=100, 10³=1k, 10⁴=10k.
  const v = Math.pow(10, p);
  if (v >= 1000) return `${(v / 1000).toFixed(0)}k`;
  return v.toFixed(0);
}
