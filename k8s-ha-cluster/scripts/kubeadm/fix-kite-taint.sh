
#!/bin/bash
set -Eeuo pipefail

echo "🔧 Fixing Kite Pod Scheduling Issue"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# 1. Check current node taints
echo "📋 Current Node Taints:"
kubectl get nodes -o custom-columns=NAME:.metadata.name,TAINTS:.spec.taints

# 2. Patch deployment to add tolerations
echo ""
echo "🔄 Adding tolerations to Kite deployment..."

kubectl patch deployment kite -n kube-system --type=json -p='[
  {
    "op": "add",
    "path": "/spec/template/spec/tolerations",
    "value": [
      {
        "key": "node-role.kubernetes.io/control-plane",
        "operator": "Exists",
        "effect": "NoSchedule"
      },
      {
        "key": "node-role.kubernetes.io/master",
        "operator": "Exists",
        "effect": "NoSchedule"
      }
    ]
  }
]'

# 3. Wait for rollout
echo ""
echo "⏳ Waiting for pod to reschedule..."
kubectl rollout status deployment/kite -n kube-system --timeout=120s

# 4. Verify
echo ""
echo "✅ Verification:"
kubectl get pods -n kube-system -l "app.kubernetes.io/name=kite" -o wide

echo ""
echo "📋 Pod Events:"
kubectl describe pod -n kube-system -l "app.kubernetes.io/name=kite" | grep -A 10 "Events:"

echo ""
echo "✅ Fix applied successfully!"