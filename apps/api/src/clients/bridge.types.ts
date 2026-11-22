export interface CallRequest {
  to: string;
  provider: string;
  providerAgentId: string;
}

export interface Bridge {
  createCall(request: CallRequest): Promise<{ callId: string }>;
}
