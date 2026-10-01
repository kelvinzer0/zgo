import { CanonicalRequest, BuildState, Env } from './types';
import { validateBuildRequest } from './allowlist';
import { checkRateLimit } from './ratelimit';

// In-memory active build state map for single-flight deduplication
const activeBuilds = new Map<string, BuildState>();
// Active SSE controller listeners by key
const sseListeners = new Map<string, Set<ReadableStreamDefaultController>>();

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    const clientIP = request.headers.get('CF-Connecting-IP') || '127.0.0.1';

    // CORS preflight
    if (request.method === 'OPTIONS') {
      return new Response(null, {
        headers: {
          'Access-Control-Allow-Origin': '*',
          'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
          'Access-Control-Allow-Headers': 'Content-Type, Authorization',
        },
      });
    }

    // Health check
    if (url.pathname === '/' || url.pathname === '/health') {
      return jsonResponse({
        service: 'zgo-broker',
        version: '2.0.0',
        builder_repo: env.BUILDER_REPO || 'kelvinzer0/zgo',
        status: 'healthy',
      });
    }

    // Rate Limiting
    const limitCheck = checkRateLimit(clientIP, parseInt(env.RATE_LIMIT_PER_MINUTE || '20', 10));
    if (!limitCheck.allowed && url.pathname.startsWith('/build')) {
      return jsonResponse({ error: 'Rate limit exceeded. Try again in 1 minute.' }, 429);
    }

    // POST /build — Single-flight remote build dispatch
    if (request.method === 'POST' && url.pathname === '/build') {
      let body: CanonicalRequest;
      try {
        body = await request.json();
      } catch {
        return jsonResponse({ error: 'Invalid JSON request body' }, 400);
      }

      // Validate allowlist
      const validation = validateBuildRequest(body);
      if (!validation.valid) {
        return jsonResponse({ error: validation.error }, 400);
      }

      // Compute cache key using Web Crypto SHA-256
      const key = await computeKeyFromCanonical(body);

      // Single-flight check: If build is already active, return existing state
      const existing = await getBuildState(key, env);
      if (existing && (existing.status === 'queued' || existing.status === 'building' || existing.status === 'publishing')) {
        return jsonResponse({
          key,
          status: existing.status,
          message: 'Build already in progress (single-flight attached)',
          run_url: existing.run_url,
        }, 200);
      }

      // Check if already completed and published in GitHub Releases
      const builderRepo = env.BUILDER_REPO || 'kelvinzer0/zgo';
      const releaseTag = `b-${key}`;
      const releaseCheck = await fetch(`https://github.com/${builderRepo}/releases/download/${releaseTag}/metadata.json`, {
        method: 'HEAD',
      });
      if (releaseCheck.status === 200) {
        return jsonResponse({
          key,
          status: 'completed',
          message: 'Release already exists in global cache',
        }, 200);
      }

      // Dispatch new GitHub Actions workflow
      const initialState: BuildState = {
        key,
        status: 'queued',
        package: body.package,
        version: body.version,
        target: `${body.goos}/${body.goarch}`,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      };

      await saveBuildState(key, initialState, env);

      // Dispatch workflow via GitHub REST API if PAT is configured
      if (env.GITHUB_PAT) {
        try {
          const dispatchRes = await fetch(
            `https://api.github.com/repos/${builderRepo}/actions/workflows/builder.yml/dispatches`,
            {
              method: 'POST',
              headers: {
                Authorization: `Bearer ${env.GITHUB_PAT}`,
                Accept: 'application/vnd.github+json',
                'User-Agent': 'zgo-broker',
                'X-GitHub-Api-Version': '2022-11-28',
              },
              body: JSON.stringify({
                ref: 'main',
                inputs: {
                  key,
                  package: body.package,
                  version: body.version,
                  goos: body.goos,
                  goarch: body.goarch,
                  cgo_enabled: body.cgo_enabled ? '1' : '0',
                  goflags: (body.goflags || []).join(' '),
                  toolchain: body.toolchain || 'auto',
                },
              }),
            }
          );

          if (!dispatchRes.ok) {
            const errText = await dispatchRes.text();
            initialState.status = 'failed';
            initialState.error = `GitHub dispatch error: ${errText}`;
            await saveBuildState(key, initialState, env);
            return jsonResponse({ error: initialState.error }, 502);
          }
        } catch (e: any) {
          initialState.status = 'failed';
          initialState.error = `Failed to trigger builder workflow: ${e.message}`;
          await saveBuildState(key, initialState, env);
          return jsonResponse({ error: initialState.error }, 500);
        }
      }

      return jsonResponse({
        key,
        status: 'queued',
        message: 'Build requested successfully',
      }, 202);
    }

    // GET /build/:key/status — SSE Stream or polling JSON
    const statusMatch = url.pathname.match(/^\/build\/([a-f0-9]{64})\/status$/);
    if (request.method === 'GET' && statusMatch) {
      const key = statusMatch[1];
      const isStream = url.searchParams.get('stream') === 'true' || request.headers.get('Accept') === 'text/event-stream';

      const state = await getBuildState(key, env) || {
        key,
        status: 'queued',
        package: '',
        version: '',
        target: '',
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      };

      if (!isStream) {
        return jsonResponse(state);
      }

      // Return SSE Stream
      const textEncoder = new TextEncoder();
      let listenerSet = sseListeners.get(key);
      if (!listenerSet) {
        listenerSet = new Set();
        sseListeners.set(key, listenerSet);
      }

      const stream = new ReadableStream({
        start(controller) {
          listenerSet!.add(controller);
          // Send initial state immediately
          controller.enqueue(textEncoder.encode(`data: ${JSON.stringify(state)}\n\n`));

          // If already completed or failed, close stream
          if (state.status === 'completed' || state.status === 'failed') {
            controller.enqueue(textEncoder.encode('data: [DONE]\n\n'));
            controller.close();
            listenerSet!.delete(controller);
          }
        },
        cancel(controller) {
          listenerSet?.delete(controller);
        },
      });

      return new Response(stream, {
        headers: {
          'Content-Type': 'text/event-stream',
          'Cache-Control': 'no-cache, no-transform',
          Connection: 'keep-alive',
          'Access-Control-Allow-Origin': '*',
        },
      });
    }

    // POST /webhook/github — Receives workflow_run events from GitHub
    if (request.method === 'POST' && url.pathname === '/webhook/github') {
      const event = request.headers.get('x-github-event');
      if (event !== 'workflow_run') {
        return new Response('Ignored', { status: 200 });
      }

      let payload: any;
      try {
        payload = await request.json();
      } catch {
        return new Response('Invalid payload', { status: 400 });
      }

      const run = payload.workflow_run;
      if (!run || !run.name) {
        return new Response('No run data', { status: 200 });
      }

      // Format of run-name is "zgo <key>"
      const match = run.name.match(/^zgo\s+([a-f0-9]{64})$/);
      if (!match) {
        return new Response('Not a zgo run', { status: 200 });
      }

      const key = match[1];
      const existing = await getBuildState(key, env) || {
        key,
        status: 'queued',
        package: '',
        version: '',
        target: '',
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      };

      existing.run_id = run.id;
      existing.run_url = run.html_url;
      existing.updated_at = new Date().toISOString();

      if (run.status === 'in_progress') {
        existing.status = 'building';
        existing.message = 'GitHub Actions runner compiling binary...';
      } else if (run.status === 'completed') {
        if (run.conclusion === 'success') {
          existing.status = 'completed';
          existing.message = 'Binary built and release b-' + key.slice(0, 12) + ' published!';
        } else {
          existing.status = 'failed';
          existing.error = `Build workflow finished with status: ${run.conclusion}`;
        }
      }

      await saveBuildState(key, existing, env);
      broadcastSSE(key, existing);

      return jsonResponse({ ok: true });
    }

    return jsonResponse({ error: 'Not Found' }, 404);
  },
};

function jsonResponse(data: any, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      'Content-Type': 'application/json',
      'Access-Control-Allow-Origin': '*',
    },
  });
}

async function getBuildState(key: string, env: Env): Promise<BuildState | null> {
  if (activeBuilds.has(key)) {
    return activeBuilds.get(key)!;
  }
  if (env.BUILD_STATE_KV) {
    const val = await env.BUILD_STATE_KV.get(`build:${key}`, 'json');
    if (val) return val as BuildState;
  }
  return null;
}

async function saveBuildState(key: string, state: BuildState, env: Env): Promise<void> {
  activeBuilds.set(key, state);
  if (env.BUILD_STATE_KV) {
    await env.BUILD_STATE_KV.put(`build:${key}`, JSON.stringify(state), {
      expirationTtl: 86400, // 24 hours
    });
  }
}

function broadcastSSE(key: string, state: BuildState) {
  const listeners = sseListeners.get(key);
  if (!listeners) return;

  const textEncoder = new TextEncoder();
  const chunk = textEncoder.encode(`data: ${JSON.stringify(state)}\n\n`);

  for (const controller of listeners) {
    try {
      controller.enqueue(chunk);
      if (state.status === 'completed' || state.status === 'failed') {
        controller.enqueue(textEncoder.encode('data: [DONE]\n\n'));
        controller.close();
        listeners.delete(controller);
      }
    } catch {
      listeners.delete(controller);
    }
  }
}

async function computeKeyFromCanonical(req: CanonicalRequest): Promise<string> {
  const copy: any = {
    builder_schema: 2,
    module: (req.module || '').trim(),
    version: (req.version || '').trim(),
    package: (req.package || '').trim(),
    goos: (req.goos || '').toLowerCase().trim(),
    goarch: (req.goarch || '').toLowerCase().trim(),
    cgo_enabled: Boolean(req.cgo_enabled),
    goflags: [...(req.goflags || [])].sort(),
  };

  if (req.commit) copy.commit = req.commit.trim();
  if (req.goarm) copy.goarm = req.goarm.trim();
  if (req.goamd64) copy.goamd64 = req.goamd64.trim();
  if (req.go386) copy.go386 = req.go386.trim();
  if (req.gomips) copy.gomips = req.gomips.trim();
  if (req.gomips64) copy.gomips64 = req.gomips64.trim();
  if (req.goppc64) copy.goppc64 = req.goppc64.trim();
  if (req.toolchain) copy.toolchain = req.toolchain.trim();

  const jsonStr = JSON.stringify(copy);
  const encoder = new TextEncoder();
  const data = encoder.encode(jsonStr);
  const hashBuf = await crypto.subtle.digest('SHA-256', data);
  const hashArr = Array.from(new Uint8Array(hashBuf));
  return hashArr.map(b => b.toString(16).padStart(2, '0')).join('');
}
