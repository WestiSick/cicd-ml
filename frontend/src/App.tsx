import { Routes, Route, Navigate } from "react-router-dom";

import { AppShell } from "@/components/AppShell";
import { Setup } from "@/pages/Setup";
import { Dashboard } from "@/pages/Dashboard";
import { Datasets } from "@/pages/Datasets";
import { DatasetDetail } from "@/pages/DatasetDetail";
import { Experiments } from "@/pages/Experiments";
import { TrainingDetail } from "@/pages/TrainingDetail";
import { Simulator } from "@/pages/Simulator";
import { Admin } from "@/pages/Admin";
import { NotFound } from "@/pages/NotFound";
import { useBootstrapStatus } from "@/hooks/useBootstrapStatus";

export function App() {
  // The bootstrap gate: if the system has never been initialised we route
  // every request to /setup so the onboarding flow is unavoidable. Once the
  // backend flips `bootstrap_done=true`, the gate releases and AppShell loads.
  const { data, isLoading } = useBootstrapStatus();

  if (isLoading) {
    return <FullPageBoot />;
  }

  if (!data?.bootstrap_done) {
    return (
      <Routes>
        <Route path="/setup" element={<Setup />} />
        <Route path="*" element={<Navigate to="/setup" replace />} />
      </Routes>
    );
  }

  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route path="/" element={<Navigate to="/dashboard" replace />} />
        <Route path="/dashboard" element={<Dashboard />} />
        <Route path="/datasets" element={<Datasets />} />
        <Route path="/datasets/:id" element={<DatasetDetail />} />
        <Route path="/experiments" element={<Experiments />} />
        <Route path="/experiments/jobs/:id" element={<TrainingDetail />} />
        <Route path="/simulator" element={<Simulator />} />
        <Route path="/admin" element={<Admin />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}

function FullPageBoot() {
  return (
    <div
      style={{
        display: "grid",
        placeItems: "center",
        height: "100vh",
        fontFamily: "var(--font-mono)",
        color: "var(--text-tertiary)",
        fontSize: 13,
        letterSpacing: "0.04em",
        textTransform: "uppercase",
      }}
    >
      connecting…
    </div>
  );
}
