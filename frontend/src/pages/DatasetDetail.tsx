import { useParams } from "react-router-dom";

import { PageHeader } from "@/components/PageHeader";
import { EmptyState } from "@/components/EmptyState";

export function DatasetDetail() {
  const { id } = useParams();
  return (
    <>
      <PageHeader
        title={`Dataset #${id}`}
        subtitle="Statistics, workflows and feature preview for this repository."
      />
      <EmptyState
        title="Detail view coming online with the collector."
        hint="Once the repo has been synced, distributions and per-workflow stats appear here."
      />
    </>
  );
}
