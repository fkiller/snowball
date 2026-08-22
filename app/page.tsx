import type { Metadata } from "next";
import { SnowballApp } from "./snowball-app";

export const metadata: Metadata = {
  title: "Snowball",
  description: "A private, room-scale bridge to ChatGPT Voice.",
};

export default function Home() {
  return <SnowballApp />;
}
