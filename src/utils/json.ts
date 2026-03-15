
export interface ParseResult<T = any> {
  ok: boolean;
  value?: T;
  error?: string;
}

export function safeParseJson<T = any>(s: string): ParseResult<T> {
  const t = s?.trim();
  if (!t) return { ok: false, error: 'Empty input' };
  
  // Try to parse the whole string first
  try {
    return { ok: true, value: JSON.parse(t) };
  } catch (e) {
    // If it fails, try to find the last JSON object
    // This is a heuristic for mixed output where the JSON is at the end
    // Look for the last closing brace and the corresponding opening brace
    const lastBrace = t.lastIndexOf('}');
    // Find the first opening brace
    const firstBrace = t.indexOf('{');
    
    if (lastBrace !== -1 && firstBrace !== -1 && firstBrace < lastBrace) {
        const candidate = t.substring(firstBrace, lastBrace + 1);
        try {
            return { ok: true, value: JSON.parse(candidate) };
        } catch (e2) {
            // Still failed
        }
    }

    return { ok: false, error: String(e) };
  }
}
