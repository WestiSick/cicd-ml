/* HeatmapChart — repo × day data-coverage grid.
 *
 * Each cell is one (repo, day) bucket; colour saturation maps to count
 * of jobs ingested that day. Shows the user where data is dense vs
 * sparse — "dyrki v datasete" in plan §«Карта временного покрытия».
 *
 * Visual decisions:
 *   - Single accent hue (amber) for the heat scale rather than a
 *     rainbow. Rainbow heatmaps trip on colourblindness and read as
 *     "categorical" when the data is really continuous.
 *   - Log-scaled saturation so a repo with 200 jobs on a busy day
 *     doesn't wash out every other repo's 5-job day.
 *   - Per-row label on the left, day axis on bottom. Days collapse to
 *     month markers every 14 days; full date in the tooltip.
 *   - Cells are 12px square — small enough to fit 90 days × 8 repos in
 *     the available card width, big enough to mouse-hover individually.
 */
export type HeatmapInput = {
  days: string[];
  repos: Array<{ id: number; slug: string }>;
  cells: Array<{ repo_id: number; day: string; count: number }>;
};

export function HeatmapChart({ data, cellSize = 12 }: { data: HeatmapInput; cellSize?: number }) {
  const labelWidth = 160;
  const tickHeight = 18;
  const width = labelWidth + data.days.length * cellSize + 8;
  const height = data.repos.length * cellSize + tickHeight + 16;

  // Lookup by (repo_id, day) → count. Linear scan of cells is fine —
  // even at 90 days × 20 repos we're under 2000 entries.
  const lookup = new Map<string, number>();
  let maxCount = 0;
  for (const c of data.cells) {
    const k = `${c.repo_id}|${c.day}`;
    lookup.set(k, c.count);
    if (c.count > maxCount) maxCount = c.count;
  }
  // log scale base — +1 so count=1 doesn't map to 0/0.
  const logMax = Math.log10(maxCount + 1) || 1;

  function colour(count: number): string {
    if (count <= 0) return "var(--bg-inset)";
    const intensity = Math.log10(count + 1) / logMax; // 0..1
    // Map intensity to amber alpha via rgba — simpler than building a
    // proper interpolated palette.
    return `rgba(242, 201, 76, ${0.15 + intensity * 0.85})`;
  }

  return (
    <svg width={width} height={height} role="img" aria-label="data coverage heatmap">
      {data.repos.map((r, ri) => (
        <g key={r.id} transform={`translate(0, ${ri * cellSize})`}>
          <text
            x={labelWidth - 8} y={cellSize - 3}
            textAnchor="end"
            fontFamily="var(--font-mono)" fontSize={11}
            fill="var(--text-secondary)"
          >
            {r.slug.length > 22 ? r.slug.slice(0, 20) + "…" : r.slug}
          </text>
          {data.days.map((day, di) => {
            const count = lookup.get(`${r.id}|${day}`) ?? 0;
            return (
              <rect
                key={di}
                x={labelWidth + di * cellSize}
                y={1}
                width={cellSize - 1}
                height={cellSize - 1}
                fill={colour(count)}
                rx={1}
              >
                <title>{`${r.slug} · ${day} · ${count} job${count === 1 ? "" : "s"}`}</title>
              </rect>
            );
          })}
        </g>
      ))}
      {/* Day axis — every 14 days. */}
      <g transform={`translate(0, ${data.repos.length * cellSize + 4})`}>
        {data.days.map((day, di) =>
          di % 14 === 0 ? (
            <text
              key={di}
              x={labelWidth + di * cellSize + cellSize / 2}
              y={tickHeight}
              textAnchor="middle"
              fontFamily="var(--font-mono)" fontSize={10}
              fill="var(--text-tertiary)"
            >
              {day.slice(5)}
            </text>
          ) : null,
        )}
      </g>
      {/* Legend strip in the bottom-right */}
      <g transform={`translate(${labelWidth}, ${height - 10})`}>
        <text x={0} y={9} fontFamily="var(--font-mono)" fontSize={10} fill="var(--text-tertiary)">
          0
        </text>
        {[0.2, 0.4, 0.6, 0.8, 1.0].map((t, i) => (
          <rect
            key={i}
            x={20 + i * 14}
            y={0}
            width={12}
            height={10}
            fill={`rgba(242, 201, 76, ${0.15 + t * 0.85})`}
            rx={1}
          />
        ))}
        <text
          x={20 + 5 * 14 + 4} y={9}
          fontFamily="var(--font-mono)" fontSize={10}
          fill="var(--text-tertiary)"
        >
          {maxCount.toLocaleString()}
        </text>
      </g>
    </svg>
  );
}
