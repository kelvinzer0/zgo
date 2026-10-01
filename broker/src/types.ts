export interface CanonicalRequest {
  builder_schema: number;
  module: string;
  version: string;
  commit?: string;
  package: string;
  goos: string;
  goarch: string;
  goarm?: string;
  goamd64?: string;
  go386?: string;
  gomips?: string;
  gomips64?: string;
  goppc64?: string;
  cgo_enabled: boolean;
  goflags: string[];
  toolchain?: string;
}

export interface BuildState {
  key: string;
  status: 'queued' | 'building' | 'publishing' | 'completed' | 'failed';
  package: string;
  version: string;
  target: string;
  run_id?: number;
  run_url?: string;
  error?: string;
  message?: string;
  created_at: string;
  updated_at: string;
}

export interface Env {
  BUILDER_REPO?: string;
  GITHUB_PAT?: string;
  WEBHOOK_SECRET?: string;
  RATE_LIMIT_PER_MINUTE?: string;
  MAX_DAILY_BUILDS_PER_IP?: string;
  BUILD_STATE_KV?: KVNamespace;
}
