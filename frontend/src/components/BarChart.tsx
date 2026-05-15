import { Group } from "@visx/group";
import { Bar } from "@visx/shape";
import { scaleBand, scaleLinear } from "@visx/scale";
import { AxisBottom, AxisLeft } from "@visx/axis";

/* Compact horizontal-ish bar chart for "metric by strategy" comparisons.
 *
 * Visual decisions:
 *   - One bar per strategy, fixed-width track. No animations.
 *   - Mono ticks (var(--font-mono)) for numeric scales — same convention
 *     as the rest of the data tables.
 *   - One colour (--accent) for every bar. The point of these charts is
 *     comparison, not categorisation; rainbow colours would imply
 *     categories that don't exist.
 *   - No gridlines. The axis line is enough; gridlines clutter at this
 *     size (small comparison charts, not data exploration).
 */
export function BarChart({
  data,
  format,
  width = 320,
  height = 180,
}: {
  data: { label: string; value: number }[];
  format?: (v: number) => string;
  width?: number;
  height?: number;
}) {
  const margin = { top: 12, right: 8, bottom: 28, left: 56 };
  const innerWidth = width - margin.left - margin.right;
  const innerHeight = height - margin.top - margin.bottom;

  const maxValue = Math.max(0, ...data.map((d) => d.value));
  const xScale = scaleBand({
    domain: data.map((d) => d.label),
    range: [0, innerWidth],
    padding: 0.3,
  });
  const yScale = scaleLinear({
    domain: [0, maxValue || 1],
    range: [innerHeight, 0],
    nice: true,
  });

  const fmt = format ?? ((v: number) => v.toFixed(0));

  const axisStyle = {
    stroke: "var(--border-strong)",
    tickStroke: "var(--border-subtle)",
    fontFamily: "var(--font-mono)",
    fontSize: 10,
    fill: "var(--text-tertiary)",
  };

  return (
    <svg width={width} height={height} role="img" aria-label="bar chart">
      <Group left={margin.left} top={margin.top}>
        {data.map((d) => {
          const x = xScale(d.label) ?? 0;
          const y = yScale(d.value);
          const barH = innerHeight - y;
          return (
            <g key={d.label}>
              <Bar
                x={x}
                y={y}
                width={xScale.bandwidth()}
                height={Math.max(2, barH)}
                fill="var(--accent)"
                rx={2}
              />
              <text
                x={x + xScale.bandwidth() / 2}
                y={y - 4}
                textAnchor="middle"
                fontSize={10}
                fontFamily="var(--font-mono)"
                fill="var(--text-secondary)"
              >
                {fmt(d.value)}
              </text>
            </g>
          );
        })}
        <AxisBottom
          top={innerHeight}
          scale={xScale}
          stroke={axisStyle.stroke}
          tickStroke={axisStyle.tickStroke}
          tickLabelProps={() => ({
            fontFamily: axisStyle.fontFamily,
            fontSize: axisStyle.fontSize,
            fill: axisStyle.fill,
            textAnchor: "middle",
            dy: "0.5em",
          })}
        />
        <AxisLeft
          scale={yScale}
          stroke={axisStyle.stroke}
          tickStroke={axisStyle.tickStroke}
          numTicks={4}
          tickFormat={(v) => fmt(v as number)}
          tickLabelProps={() => ({
            fontFamily: axisStyle.fontFamily,
            fontSize: axisStyle.fontSize,
            fill: axisStyle.fill,
            textAnchor: "end",
            dx: "-0.3em",
            dy: "0.3em",
          })}
        />
      </Group>
    </svg>
  );
}
