/**
 * RFC 1123 compliant hostname validation.
 * A hostname can only contain alphanumeric characters and hyphens.
 * It must not start or end with a hyphen.
 * It must be between 1 and 63 characters long.
 */
export function validateHostname(hostname: string): boolean {
  if (!hostname || hostname.length < 1 || hostname.length > 63) {
    return false;
  }
  const regex = /^(?!-)[A-Za-z0-9-]{1,63}(?<!-)$/;
  return regex.test(hostname);
}

/**
 * Validates that a list of hostnames contains no duplicates.
 * Throws an error if a duplicate is found.
 */
export function validateUniqueHostnames(hostnames: string[]): void {
  const seen = new Set<string>();
  const duplicates = new Set<string>();

  for (const hostname of hostnames) {
    if (seen.has(hostname)) {
      duplicates.add(hostname);
    }
    seen.add(hostname);
  }

  if (duplicates.size > 0) {
    throw new Error(`[HOSTNAME] Duplicate hostnames detected: ${Array.from(duplicates).join(', ')}`);
  }
}

/**
 * Precedence rules:
 * 1. Environment variable: NODE_HOSTNAME_<TYPE>_<INDEX> (e.g., NODE_HOSTNAME_CP_0, NODE_HOSTNAME_WORKER_1)
 * 2. Config list: nodes.controlPlaneHostnames[index] or nodes.workerHostnames[index]
 * 3. Default: <stack>-<type>-<index+1>
 */
export function resolveNodeHostname(
  nodeType: 'cp' | 'worker',
  index: number,
  stack: string,
  configHostnames?: string[]
): string {
  const envVarName = `NODE_HOSTNAME_${nodeType.toUpperCase()}_${index}`;
  const envValue = process.env[envVarName];

  if (envValue) {
    if (validateHostname(envValue)) {
      console.log(`[HOSTNAME] Resolved from environment variable ${envVarName}: ${envValue}`);
      return envValue;
    } else {
      console.error(`[HOSTNAME] Invalid hostname in environment variable ${envVarName}: ${envValue}. Falling back to config.`);
    }
  }

  if (configHostnames && Array.isArray(configHostnames)) {
    // Check bounds
    if (index >= 0 && index < configHostnames.length) {
      const configValue = configHostnames[index];
      if (validateHostname(configValue)) {
        console.log(`[HOSTNAME] Resolved from configuration file (index ${index}): ${configValue}`);
        return configValue;
      } else {
        console.error(`[HOSTNAME] Invalid hostname in configuration (index ${index}): ${configValue}. Falling back to default.`);
      }
    } else {
      // Index out of bounds - log warning if expected to be there
      // This is expected if config list is shorter than node count, but useful to log
      if (configHostnames.length > 0) {
        console.warn(`[HOSTNAME] Configuration list shorter than node index ${index} (length: ${configHostnames.length}). Falling back to default.`);
      }
    }
  } else if (configHostnames) {
     console.warn(`[HOSTNAME] Config hostnames provided but not an array: ${typeof configHostnames}`);
  }

  // Default fallback: stack-type-index+1 (index+1 to match the common 1-based numbering)
  const defaultValue = `${stack}-${nodeType}-${index + 1}`;
  if (!validateHostname(defaultValue)) {
    // Should not happen for standard stacks/types/indices, but for safety:
    throw new Error(`[HOSTNAME] Generated default hostname is invalid: ${defaultValue}`);
  }

  console.log(`[HOSTNAME] Using default fallback: ${defaultValue}`);
  return defaultValue;
}
