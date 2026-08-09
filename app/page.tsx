import type { Metadata } from "next";
import { VoiceConsole } from "./voice-console";

export const metadata: Metadata = {
  title: "Snowball",
  description: "A private, room-scale bridge to ChatGPT Voice.",
};

export default function Home() {
  return <VoiceConsole />;
}
