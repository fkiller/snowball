"use client";

import { AuthBoundary } from "./auth-boundary";
import { VoiceConsole } from "./voice-console";

export function SnowballApp() {
  return (
    <AuthBoundary>
      <VoiceConsole />
    </AuthBoundary>
  );
}
