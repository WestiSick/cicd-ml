/* Horizontal bar chart — feature names on Y, importance on X.
 *
 * Used by /experiments/jobs/:id to render top-K features. Horizontal
 * layout is the only sane choice when labels are long ("workflow_name=
 * Push on main" doesn't fit under a vertical bar).
 *
 * Bars share a single colour (--accent) — the importance value is the
 * variable, the category is fixed-rank. Multi-colour bars would imply
 * categorical buckets that don't exist here.
 */
export function HBarChart({
  items,
  width = 480,
  rowHeight = 18,
  labelWidth = 240,
  valueFormat = (v) => v.toFixed(3),
}: {
  items: { name: string; value: number }[];
  width?: number;
  rowHeight?: number;
  labelWidth?: number;
  valueFormat?: (v: number) => string;
}) {
  const height = items.length * rowHeight + 16;
  const barAreaWidth = width - labelWidth - 56; // 56 for value text on right
  const maxVal = Math.max(0, ...items.map((d) => d.value));
  const scale = maxVal > 0 ? barAreaWidth / maxVal : 0;

  return (
    <svg width={width} height={height} role="img" aria-label="horizontal bar chart">
      {items.map((d, i) => {
        const y = 8 + i * rowHeight;
        const barW = Math.max(1, d.value * scale);
        return (
          <g key={`${d.name}-${i}`}>
            <text
              x={labelWidth - 8}
              y={y + rowHeight / 2 + 3}
              textAnchor="end"
              fontFamily="var(--font-mono)"
              fontSize={10}
              fill="var(--text-secondary)"
            >
              {truncate(d.name, 38)}
            </text>
            <rect
              x={labelWidth}
              y={y + 3}
              width={barW}
              height={rowHeight - 6}
              fill="var(--accent)"
              opacity={0.85}
              rx={2}
            />
            <text
              x={labelWidth + barW + 6}
              y={y + rowHeight / 2 + 3}
              fontFamily="var(--font-mono)"
              fontSize={10}
              fill="var(--text-tertiary)"
            >
              {valueFormat(d.value)}
            </text>
          </g>
        );
      })}
    </svg>
  );
}

function truncate(s: string, n: number): string {
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}
