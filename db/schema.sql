-- Agents
CREATE TABLE agents (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  name text NOT NULL,
  provider text NOT NULL CHECK (provider IN ('cartesia')),
  provider_agent_id text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (provider, provider_agent_id)
);
