import type { Metadata } from "next";
import { AdminApp } from "./admin-app";

export const metadata: Metadata = {
  title: "Admin · Snowball-Voice-Gate",
  description: "Snowball gateway settings and security controls.",
};

export default function AdminPage() {
  return <AdminApp />;
}
