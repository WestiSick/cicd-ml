import { Link } from "react-router-dom";

export function NotFound() {
  return (
    <div
      style={{
        textAlign: "center",
        padding: "var(--s-16) 0",
      }}
    >
      <div
        className="mono caps"
        style={{ color: "var(--text-tertiary)" }}
      >
        404
      </div>
      <h1
        style={{
          fontSize: "var(--fs-28)",
          fontWeight: 500,
          margin: "var(--s-2) 0 var(--s-3)",
        }}
      >
        Page not found.
      </h1>
      <Link
        to="/dashboard"
        style={{
          color: "var(--text-primary)",
          borderBottom: "1px solid var(--accent)",
          paddingBottom: 2,
        }}
      >
        Back to dashboard
      </Link>
    </div>
  );
}
