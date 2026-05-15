"""Canonical error envelope, matched 1:1 with the Go api-gateway.

The frontend reads `error.user_action` and shows it verbatim in a toast.
Never put stack traces or internal codes in `message` — those belong in
logs. `code` is machine-readable; the UI uses it for retry policy.
"""
from __future__ import annotations

from typing import Any


class APIError(Exception):
    def __init__(
        self,
        status: int,
        code: str,
        message: str,
        user_action: str = "",
        details: dict[str, Any] | None = None,
    ) -> None:
        self.status = status
        self.code = code
        self.message = message
        self.user_action = user_action
        self.details = details or {}

    def body(self) -> dict[str, Any]:
        out: dict[str, Any] = {"code": self.code, "message": self.message}
        if self.user_action:
            out["user_action"] = self.user_action
        if self.details:
            out["details"] = self.details
        return out
