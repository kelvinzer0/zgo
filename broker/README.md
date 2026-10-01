# ZGo Build Broker (Cloudflare Worker)

Thin edge broker for coordinating ZGo remote builds, single-flight deduplication, rate-limiting, and Server-Sent Events (SSE) status streaming.

## Features

- **Single-Flight Deduplication**: Multiple users requesting the same cache key attach to a single in-flight build.
- **Server-Sent Events (SSE)**: Streams real-time build progress to the CLI (`queued` -> `building` -> `completed`).
- **Strict Allowlist**: Validates package paths and denies dangerous flags (`-toolexec`, `-exec`, etc.).
- **Rate Limiting**: Protects builder runner minutes from abuse.
- **GitHub Webhook Integration**: Receives `workflow_run` events and relays updates to active SSE clients.

## Deployment

1. Install Wrangler:
   ```bash
   npm install -g wrangler
   ```

2. Create KV namespace (optional, for persistent state across edges):
   ```bash
   wrangler kv:namespace create BUILD_STATE_KV
   ```
   Update `wrangler.jsonc` with the returned KV ID.

3. Set Secrets:
   ```bash
   wrangler secret put GITHUB_PAT      # Personal access token with repo permissions
   wrangler secret put WEBHOOK_SECRET  # Webhook secret configured in GitHub repo
   ```

4. Deploy:
   ```bash
   wrangler deploy
   ```

## Webhook Configuration

In your GitHub Builder repository (`zgo-cli/builder`), add a webhook:
- **Payload URL**: `https://<your-worker>.workers.dev/webhook/github`
- **Content type**: `application/json`
- **Secret**: The `WEBHOOK_SECRET` set above
- **Events**: Select individual events -> Check `Workflow runs`
