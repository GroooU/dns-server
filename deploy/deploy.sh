#!/usr/bin/env bash
# Deploy dns-forwarder to Yandex Cloud VM.
#
# Required env vars:
#   REGISTRY_ID   — Yandex Container Registry ID (from terraform output registry_id)
#   VM_IP         — VM external IP (from terraform output vm_external_ip)
#   SSH_KEY_PATH  — path to SSH private key (default: ~/.ssh/id_rsa)
#
# Optional:
#   IMAGE_TAG     — Docker image tag (default: git short SHA)

set -euo pipefail

REGISTRY_ID="${REGISTRY_ID:?set REGISTRY_ID to the Container Registry ID}"
VM_IP="${VM_IP:?set VM_IP to the VM external IP}"
SSH_KEY_PATH="${SSH_KEY_PATH:-$HOME/.ssh/id_rsa}"
IMAGE_TAG="${IMAGE_TAG:-$(git rev-parse --short HEAD)}"

FULL_IMAGE="cr.yandex/${REGISTRY_ID}/dns-forwarder:${IMAGE_TAG}"
REPO_ROOT="$(git rev-parse --show-toplevel)"

echo "==> Building ${FULL_IMAGE}"
docker build -t "${FULL_IMAGE}" -f "${REPO_ROOT}/Dockerfile" "${REPO_ROOT}"

echo "==> Authenticating with Yandex Container Registry"
docker login --username iam --password "$(yc iam create-token)" cr.yandex

echo "==> Pushing image"
docker push "${FULL_IMAGE}"

echo "==> Uploading config.yaml to VM"
scp -i "${SSH_KEY_PATH}" -o StrictHostKeyChecking=no \
    "${REPO_ROOT}/config.yaml" \
    "${REPO_ROOT}/deploy/docker-compose.yml" \
    "ubuntu@${VM_IP}:/app/"

echo "==> Pulling image and restarting container on ${VM_IP}"
ssh -i "${SSH_KEY_PATH}" -o StrictHostKeyChecking=no "ubuntu@${VM_IP}" \
    "IMAGE_TAG=${IMAGE_TAG} REGISTRY_ID=${REGISTRY_ID} \
     docker compose -f /app/docker-compose.yml pull && \
     docker compose -f /app/docker-compose.yml up -d --remove-orphans"

echo "==> Waiting for health check..."
sleep 5
if curl -sf "http://${VM_IP}:8053/health" > /dev/null; then
    echo "==> Deploy successful. DNS forwarder is healthy."
else
    echo "==> Health check failed — check logs:" >&2
    ssh -i "${SSH_KEY_PATH}" "ubuntu@${VM_IP}" "docker logs dns-forwarder 2>&1 | tail -30" >&2
    exit 1
fi

echo ""
echo "    DNS:     ${VM_IP}:53"
echo "    Metrics: http://${VM_IP}:8053/metrics"
echo "    Health:  http://${VM_IP}:8053/health"
