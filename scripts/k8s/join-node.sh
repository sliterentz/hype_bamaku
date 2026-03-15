#!/usr/bin/env bash
set -euo pipefail

JOIN_COMMAND="${JOIN_COMMAND:?JOIN_COMMAND wajib}"

if systemctl is-active --quiet kubelet && sudo test -f /etc/kubernetes/kubelet.conf; then
  echo "node already joined; skipping" >&2
  exit 0
fi

sudo ${JOIN_COMMAND}
