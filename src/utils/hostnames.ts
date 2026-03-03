/**
 * RFC 1123 compliant hostname validation.
 * A hostname can only contain alphanumeric characters and hyphens.
 * It must not start or end with a hyphen.
 * It must be between 1 and 63 characters long.
 */
export function validateHostname(hostname: string): boolean {
  if (hostname.length < 1 || hostname.length > 63) {
    return false;
  }
  const regex = /^(?!-)[A-Za-z0-9-]{1,63}(?<!-)$/;
  return regex.test(hostname);
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

  if (configHostnames && configHostnames[index]) {
    const configValue = configHostnames[index];
    if (validateHostname(configValue)) {
      console.log(`[HOSTNAME] Resolved from configuration file (index ${index}): ${configValue}`);
      return configValue;
    } else {
      console.error(`[HOSTNAME] Invalid hostname in configuration (index ${index}): ${configValue}. Falling back to default.`);
    }
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
