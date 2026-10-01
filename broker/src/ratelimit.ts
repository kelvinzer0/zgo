// In-memory rate limiting map for edge instances
const memoryRateMap = new Map<string, { count: number; resetAt: number }>();

export function checkRateLimit(ip: string, limitPerMinute: number = 20): { allowed: boolean; remaining: number } {
  const now = Date.now();
  const entry = memoryRateMap.get(ip);

  if (!entry || now > entry.resetAt) {
    memoryRateMap.set(ip, { count: 1, resetAt: now + 60_000 });
    return { allowed: true, remaining: limitPerMinute - 1 };
  }

  if (entry.count >= limitPerMinute) {
    return { allowed: false, remaining: 0 };
  }

  entry.count++;
  return { allowed: true, remaining: limitPerMinute - entry.count };
}
