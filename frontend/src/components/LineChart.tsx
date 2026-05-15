import { Group } from "@visx/group";
import { LinePath } from "@visx/shape";
import { scaleLinear } from "@visx/scale";
import { AxisBottom, AxisLeft } from "@visx/axis";

export type Series = {
  label: string;
  color: string;
  points: { x: number; y: number }[];
};

/* Minimal line chart for per-iteration training curves.
 *
 * Decisions:
 *   - Single colour per series, 1.5px stroke (thesis aesthetic).
 *   - Y axis nice'd to 4 ticks — anything more is noise at this size.
 *   - No legend; the caller's section title says which series is which.
 *     If we add a comparison plot (multiple series) we'll inline labels
 *     at the line ends instead of a legend block.
 */
export function LineChart({
  series,
  width = 720,
  height = 220,
  yLabel,
  xLabel,
}: {
  series: Series[];
  width?: number;
  height?: number;
  yLabel?: string;
  xLabel?: string;
}) {
  const margin = { top: 16, right: 24, bottom: 32, left: 56 };
  const innerW = width - margin.left - margin.right;
  const innerH = height - margin.top - margin.bottom;

  // Compute the global x/y extents across all series.
  let xMin = Infinity, xMax = -Infinity, yMin = Infinity, yMax = -Infinity;
  for (const s of series) {
    for (const p of s.points) {
      if (p.x < xMin) xMin = p.x;
      if (p.x > xMax) xMax = p.x;
      if (p.y < yMin) yMin = p.y;
      if (p.y > yMax) yMax = p.y;
    }
  }
  if (!isFinite(xMin)) { xMin = 0; xMax = 1; yMin = 0; yMax = 1; }
  if (xMin === xMax) { xMax = xMin + 1; }
  if (yMin === yMax) { yMax = yMin + 1; }

  const xScale = scaleLinear({ domain: [xMin, xMax], range: [0, innerW] });
  const yScale = scaleLinear({ domain: [yMin, yMax], range: [innerH, 0], nice: true });

  const axisProps = {
    stroke: "var(--border-strong)",
    tickStroke: "var(--border-subtle)",
    tickLabelProps: () => ({
      fontFamily: "var(--font-mono)",
      fontSize: 10,
      fill: "var(--text-tertiary)",
    }),
  } as const;

  return (
    <svg width={width} height={height} role="img" aria-label="line chart">
      <Group left={margin.left} top={margin.top}>
        {series.map((s) => (
          <LinePath
            key={s.label}
            data={s.points}
            x={(d) => xScale(d.x)}
            y={(d) => yScale(d.y)}
            stroke={s.color}
            strokeWidth={1.5}
          />
        ))}
        <AxisBottom top={innerH} scale={xScale} numTicks={6} {...axisProps} />
        <AxisLeft scale={yScale} numTicks={4} {...axisProps} />
        {yLabel && (
          <text
            x={-innerH / 2}
            y={-44}
            transform={`rotate(-90)`}
            textAnchor="middle"
            fontFamily="var(--font-mono)"
            fontSize={10}
            fill="var(--text-tertiary)"
          >
            {yLabel}
          </text>
        )}
        {xLabel && (
          <text
            x={innerW / 2}
            y={innerH + 26}
            textAnchor="middle"
            fontFamily="var(--font-mono)"
            fontSize={10}
            fill="var(--text-tertiary)"
          >
            {xLabel}
          </text>
        )}
      </Group>
    </svg>
  );
}
