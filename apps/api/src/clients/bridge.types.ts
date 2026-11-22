export interface Bridge {
  createCall(to: string): Promise<{ callId: string }>;
}
