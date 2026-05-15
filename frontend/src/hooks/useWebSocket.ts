import { useEffect, useRef, useState } from "react";

const WS_BASE: string = import.meta.env.VITE_WS_BASE || "ws://localhost:8080";

export type WSMessage = {
  type: string;
  data?: unknown;
};

/* Thin wrapper around the native WebSocket with auto-reconnect.
 *
 * Decisions:
 *   - Backoff caps at 30s; the reconnect attempt itself is a useful
 *     liveness signal for the health dot in the topbar.
 *   - We do NOT replay missed messages. The backend's source of truth
 *     is Postgres — on reconnect the page can re-fetch via REST and
 *     stream forward from "now".
 *   - The hook returns *latest* message rather than a queue; consumers
 *     pair this with their own state (e.g. a Map of bg_jobs) and
 *     update on every event.
 */
export function useWebSocket(path: string, onMessage?: (m: WSMessage) => void) {
  const [connected, setConnected] = useState(false);
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;

  useEffect(() => {
    let socket: WebSocket | null = null;
    let stopped = false;
    let retries = 0;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;

    function connect() {
      if (stopped) return;
      socket = new WebSocket(WS_BASE + path);
      socket.addEventListener("open", () => {
        retries = 0;
        setConnected(true);
      });
      socket.addEventListener("close", () => {
        setConnected(false);
        if (stopped) return;
        const delay = Math.min(30_000, 500 * 2 ** retries++);
        reconnectTimer = setTimeout(connect, delay);
      });
      socket.addEventListener("message", (e) => {
        try {
          const msg = JSON.parse(e.data) as WSMessage;
          onMessageRef.current?.(msg);
        } catch {
          // Non-JSON frames are skipped silently.
        }
      });
    }

    connect();
    return () => {
      stopped = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      socket?.close();
    };
  }, [path]);

  return { connected };
}
