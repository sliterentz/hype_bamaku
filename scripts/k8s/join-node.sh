#!/usr/bin/env bash
set -euo pipefail

JOIN_COMMAND="${JOIN_COMMAND:?JOIN_COMMAND wajib}"

sudo ${JOIN_COMMAND}
