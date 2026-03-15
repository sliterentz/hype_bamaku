
import { safeParseJson } from '../utils/json';

function runTest(name: string, fn: () => void) {
  try {
    fn();
    console.log(`PASS: ${name}`);
  } catch (e) {
    console.error(`FAIL: ${name}`);
    console.error(e);
    process.exit(1);
  }
}

function expect(actual: any) {
  return {
    toBe: (expected: any) => {
      if (actual !== expected) throw new Error(`Expected ${expected}, got ${actual}`);
    },
    toEqual: (expected: any) => {
      const sActual = JSON.stringify(actual);
      const sExpected = JSON.stringify(expected);
      if (sActual !== sExpected) throw new Error(`Expected ${sExpected}, got ${sActual}`);
    }
  };
}

runTest('parses valid JSON', () => {
  const input = '{"foo":"bar"}';
  const result = safeParseJson(input);
  expect(result.ok).toBe(true);
  expect(result.value).toEqual({ foo: 'bar' });
});

runTest('handles mixed output (text before JSON)', () => {
  const input = `[init] Using Kubernetes version: v1.28.2
[preflight] Running pre-flight checks
{
  "joinWorker": "worker-cmd",
  "joinControlPlane": "cp-cmd",
  "kubeconfigB64": "base64data"
}`;
  const result = safeParseJson(input);
  expect(result.ok).toBe(true);
  expect(result.value).toEqual({
    joinWorker: "worker-cmd",
    joinControlPlane: "cp-cmd",
    kubeconfigB64: "base64data"
  });
});

runTest('fails gracefully on non-JSON', () => {
  const input = '[init] Using Kubernetes version';
  const result = safeParseJson(input);
  expect(result.ok).toBe(false);
});
