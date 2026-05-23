import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { Button } from "@/components/Button";
import { EmptyState } from "@/components/EmptyState";
import { BarChart } from "@/components/BarChart";
import { ApiError } from "@/api/client";
import { listSimRuns, listStrategies, runSimulator, simRunExportCSVURL, type SimMetrics } from "@/api/simulator";
import { useT } from "@/i18n";
import type { TranslationKey } from "@/i18n/types";

const WINDOW_PRESETS: { labelKey: TranslationKey; days: number }[] = [
  { labelKey: "sim.window.last_7",  days: 7 },
  { labelKey: "sim.window.last_30", days: 30 },
  { labelKey: "sim.window.last_90", days: 90 },
  { labelKey: "sim.window.all",     days: 36500 },
];

export function Simulator() {
  const t = useT();
  const qc = useQueryClient();
  const strategiesQ = useQuery({ queryKey: ["sim-strategies"], queryFn: listStrategies });
  const recent = useQuery({ queryKey: ["sim-runs"], queryFn: () => listSimRuns(20) });

  const [windowDays, setWindowDays] = useState(36500);
  const [runners, setRunners] = useState(2);
  const [selected, setSelected] = useState<string[]>(["fifo", "sjf", "edf", "custom"]);
  const [slaMain, setSlaMain] = useState(1800);
  const [slaFeature, setSlaFeature] = useState(7200);
  const [results, setResults] = useState<SimMetrics[] | null>(null);

  const run = useMutation({
    mutationFn: () => {
      const end = new Date();
      const start = new Date(end.getTime() - windowDays * 86400 * 1000);
      return runSimulator({
        window_start: start.toISOString(),
        window_end: end.toISOString(),
        strategies: selected,
        runners,
        sla_main_sec: slaMain,
        sla_feature_sec: slaFeature,
      });
    },
    onSuccess: (resp) => {
      toast.success(t("sim.toast.done", { jobs: resp.jobs, n: resp.results.length }));
      setResults(resp.results);
      qc.invalidateQueries({ queryKey: ["sim-runs"] });
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error("simulation failed");
    },
  });

  const toggleStrategy = (s: string) =>
    setSelected((cur) => (cur.includes(s) ? cur.filter((x) => x !== s) : [...cur, s]));

  const charts = useMemo(() => {
    if (!results || results.length === 0) return null;
    return {
      makespan:    results.map((r) => ({ label: r.strategy, value: r.makespan_sec })),
      waitMean:    results.map((r) => ({ label: r.strategy, value: r.wait_mean_sec })),
      waitP95:     results.map((r) => ({ label: r.strategy, value: r.wait_p95_sec })),
      slaViols:    results.map((r) => ({ label: r.strategy, value: r.sla_violations })),
    };
  }, [results]);

  return (
    <>
      <PageHeader
        title={t("sim.title")}
        subtitle={t("sim.subtitle")}
        actions={
          <Button variant="primary" loading={run.isPending} disabled={selected.length === 0} onClick={() => run.mutate()}>
            {t("sim.run")}
          </Button>
        }
      />

      <Card style={{ marginBottom: "var(--s-4)" }}>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-6)" }}>
          <div>
            <div className="caps" style={labelStyle}>{t("sim.window")}</div>
            <div style={{ display: "flex", flexWrap: "wrap", gap: "var(--s-2)" }}>
              {WINDOW_PRESETS.map((p) => (
                <button
                  key={p.labelKey}
                  style={pillStyle(p.days === windowDays)}
                  onClick={() => setWindowDays(p.days)}
                >
                  {t(p.labelKey)}
                </button>
              ))}
            </div>

            <div className="caps" style={{ ...labelStyle, marginTop: "var(--s-4)" }}>{t("sim.strategies")}</div>
            <div style={{ display: "flex", flexWrap: "wrap", gap: "var(--s-2)" }}>
              {(strategiesQ.data ?? ["fifo", "sjf", "edf", "custom"]).map((s) => {
                const active = selected.includes(s);
                return (
                  <button key={s} style={pillStyle(active)} onClick={() => toggleStrategy(s)}>
                    <span className="mono">{s}</span>
                  </button>
                );
              })}
            </div>
          </div>

          <div>
            <div className="caps" style={labelStyle}>{t("sim.runners")}</div>
            <input
              type="number"
              min={1}
              max={32}
              value={runners}
              onChange={(e) => setRunners(Math.max(1, Number(e.target.value) || 1))}
              style={numberStyle}
            />

            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-3)", marginTop: "var(--s-4)" }}>
              <div>
                <div className="caps" style={labelStyle}>{t("sim.sla_main")}</div>
                <input
                  type="number"
                  value={slaMain}
                  onChange={(e) => setSlaMain(Number(e.target.value) || 0)}
                  style={numberStyle}
                />
              </div>
              <div>
                <div className="caps" style={labelStyle}>{t("sim.sla_feature")}</div>
                <input
                  type="number"
                  value={slaFeature}
                  onChange={(e) => setSlaFeature(Number(e.target.value) || 0)}
                  style={numberStyle}
                />
              </div>
            </div>
          </div>
        </div>
      </Card>

      {results === null && (
        <EmptyState
          title={t("sim.empty.title")}
          hint={t("sim.empty.hint")}
        />
      )}

      {results && results.length > 0 && (
        <>
          <h2 style={sectionTitleStyle}>{t("sim.results")}</h2>
          <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: "var(--s-3)", marginBottom: "var(--s-6)" }}>
            <ChartCard title={t("sim.metric.makespan")} data={charts!.makespan} />
            <ChartCard title={t("sim.metric.wait_mean")} data={charts!.waitMean} />
            <ChartCard title={t("sim.metric.wait_p95")} data={charts!.waitP95} />
            <ChartCard title={t("sim.metric.sla_viol")} data={charts!.slaViols} />
          </div>

          <Card>
            <h3 style={{ margin: 0, fontSize: "var(--fs-14)", fontWeight: 500 }}>Per-strategy metrics</h3>
            <table style={tableStyle}>
              <thead>
                <tr>
                  <Th>Strategy</Th>
                  <Th>Jobs</Th>
                  <Th>Makespan</Th>
                  <Th>Wait p50</Th>
                  <Th>Wait p95</Th>
                  <Th>Wait mean</Th>
                  <Th>Throughput / min</Th>
                  <Th>SLA viol.</Th>
                </tr>
              </thead>
              <tbody>
                {results.map((r) => (
                  <tr key={r.strategy} style={{ borderTop: "1px solid var(--border-subtle)" }}>
                    <Td mono>{r.strategy}</Td>
                    <Td mono>{r.jobs_count}</Td>
                    <Td mono>{r.makespan_sec.toFixed(0)}</Td>
                    <Td mono>{r.wait_p50_sec.toFixed(0)}</Td>
                    <Td mono>{r.wait_p95_sec.toFixed(0)}</Td>
                    <Td mono>{r.wait_mean_sec.toFixed(1)}</Td>
                    <Td mono>{r.throughput_per_min.toFixed(2)}</Td>
                    <Td mono>{r.sla_violations}</Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>
        </>
      )}

      {recent.data && recent.data.length > 0 && (
        <>
          <h2 style={{ ...sectionTitleStyle, marginTop: "var(--s-8)" }}>{t("sim.recent_runs")}</h2>
          <Card>
            <table style={tableStyle}>
              <thead>
                <tr>
                  <Th>When</Th>
                  <Th>Strategy</Th>
                  <Th>Jobs</Th>
                  <Th>Makespan</Th>
                  <Th>Wait p95</Th>
                  <Th>SLA viol.</Th>
                  <Th>{" "}</Th>
                </tr>
              </thead>
              <tbody>
                {recent.data.map((r) => (
                  <tr key={r.id} style={{ borderTop: "1px solid var(--border-subtle)" }}>
                    <Td mono small>{new Date(r.created_at).toISOString().slice(0, 19)}</Td>
                    <Td mono>{r.strategy}</Td>
                    <Td mono>{r.jobs_count}</Td>
                    <Td mono>{r.makespan_sec?.toFixed(0) ?? "—"}</Td>
                    <Td mono>{r.wait_p95_sec?.toFixed(0) ?? "—"}</Td>
                    <Td mono>{r.sla_violations ?? "—"}</Td>
                    <Td>
                      <a
                        href={simRunExportCSVURL(r.id)}
                        style={{ color: "var(--text-secondary)", textDecoration: "none", fontSize: 11 }}
                      >
                        {t("sim.export_csv")}
                      </a>
                    </Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>
        </>
      )}
    </>
  );
}

function ChartCard({ title, data }: { title: string; data: { label: string; value: number }[] }) {
  return (
    <Card>
      <div className="caps" style={{ color: "var(--text-tertiary)", marginBottom: "var(--s-2)" }}>{title}</div>
      <BarChart data={data} format={(v) => v.toFixed(0)} />
    </Card>
  );
}

const labelStyle: React.CSSProperties = { color: "var(--text-tertiary)", marginBottom: "var(--s-2)" };

const sectionTitleStyle: React.CSSProperties = {
  fontSize: "var(--fs-16)",
  fontWeight: 500,
  margin: "0 0 var(--s-3)",
};

const numberStyle: React.CSSProperties = {
  width: "100%",
  height: 32,
  background: "var(--bg-base)",
  border: "1px solid var(--border-subtle)",
  borderRadius: "var(--r-6)",
  padding: "0 var(--s-3)",
  color: "var(--text-primary)",
  fontFamily: "var(--font-mono)",
  fontSize: "var(--fs-13)",
  outline: "none",
};

function pillStyle(active: boolean): React.CSSProperties {
  return {
    height: 30,
    padding: "0 12px",
    background: active ? "var(--bg-base)" : "transparent",
    color: active ? "var(--text-primary)" : "var(--text-secondary)",
    border: `1px solid ${active ? "var(--border-strong)" : "var(--border-subtle)"}`,
    borderRadius: "var(--r-6)",
    fontSize: "var(--fs-13)",
    cursor: "pointer",
    boxShadow: active ? "inset 0 0 0 1px var(--accent-soft)" : undefined,
  };
}

const tableStyle: React.CSSProperties = {
  width: "100%",
  borderCollapse: "collapse",
  fontSize: "var(--fs-13)",
  marginTop: "var(--s-3)",
};

function Th({ children }: { children: React.ReactNode }) {
  return (
    <th
      className="caps"
      style={{
        textAlign: "left",
        padding: "var(--s-2) var(--s-1)",
        color: "var(--text-tertiary)",
        fontWeight: 500,
      }}
    >
      {children}
    </th>
  );
}

function Td({
  children,
  mono,
  small,
}: {
  children: React.ReactNode;
  mono?: boolean;
  small?: boolean;
}) {
  return (
    <td
      className={mono ? "mono" : undefined}
      style={{
        padding: "var(--s-2) var(--s-1)",
        fontSize: small ? "var(--fs-12)" : undefined,
      }}
    >
      {children}
    </td>
  );
}
