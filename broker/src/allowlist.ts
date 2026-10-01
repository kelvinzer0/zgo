const FORBIDDEN_FLAGS = [
  '-toolexec',
  '-exec',
  '-modfile',
  '-overlay',
  '-mod=vendor',
  '-pkgdir',
  '-extldflags',
];

const DENYLIST_PACKAGES = [
  'malicious/repo',
  'exploit/',
];

export function validateBuildRequest(req: { package: string; goflags: string[] }): { valid: boolean; error?: string } {
  if (!req.package || typeof req.package !== 'string') {
    return { valid: false, error: 'package path is required' };
  }

  // Check denylist
  for (const denied of DENYLIST_PACKAGES) {
    if (req.package.includes(denied)) {
      return { valid: false, error: `package ${req.package} is blocked on public broker` };
    }
  }

  if (Array.isArray(req.goflags)) {
    for (const flag of req.goflags) {
      if (typeof flag !== 'string') continue;

      for (const forbidden of FORBIDDEN_FLAGS) {
        if (flag === forbidden || flag.startsWith(forbidden + '=')) {
          return { valid: false, error: `ERROR: unsupported GOFLAGS: ${forbidden}` };
        }
      }

      // Check for local file paths
      if (flag.startsWith('/') || flag.startsWith('./') || flag.startsWith('../') || flag.includes(':\\')) {
        return { valid: false, error: `ERROR: local filesystem paths are forbidden in GOFLAGS: ${flag}` };
      }
    }
  }

  return { valid: true };
}
