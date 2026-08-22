"use client";

import { AuthBoundary } from "../auth-boundary";
import { AdminConsole } from "./admin-console";

export function AdminApp() {
  return (
    <AuthBoundary>
      <AdminConsole />
    </AuthBoundary>
  );
}
