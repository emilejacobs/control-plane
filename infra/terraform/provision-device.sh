#!/bin/bash
# provision-device.sh — wrap `terraform apply` + extract outputs into a workspace
# directory that mirrors the layout the agent expects. Run from this directory.
#
# Usage:
#   ./provision-device.sh apply  dev-pi-emile
#   ./provision-device.sh destroy dev-pi-emile
#
# After `apply`, outputs land in ./out/<device_id>/:
#   cert.pem   private.key   ca.pem   agent.json
# scp these to the device per docs/runbooks/phase-0-agent-install.md.
set -euo pipefail

cd "$(dirname "$0")"

action="${1:-}"
device_id="${2:-}"

if [[ -z "$action" || -z "$device_id" ]]; then
  echo "usage: $0 {apply|destroy} <device_id>" >&2
  exit 2
fi

case "$action" in
  apply)
    terraform init -upgrade >/dev/null
    terraform apply -auto-approve -var "device_id=$device_id"

    out_dir="out/$device_id"
    mkdir -p "$out_dir"
    chmod 700 "$out_dir"

    terraform output -raw cert_pem    > "$out_dir/cert.pem"
    terraform output -raw private_key > "$out_dir/private.key"
    chmod 600 "$out_dir/private.key"

    curl -sS -o "$out_dir/ca.pem" https://www.amazontrust.com/repository/AmazonRootCA1.pem

    broker_url=$(terraform output -raw broker_url)
    cat > "$out_dir/agent.json" <<JSON
{
  "device_id":          "$device_id",
  "version":            "0.1.0",
  "broker_url":         "$broker_url",
  "client_id":          "$device_id",
  "cert_path":          "/etc/uknomi/certs/device.crt",
  "key_path":           "/etc/uknomi/certs/device.key",
  "ca_cert_path":       "/etc/uknomi/certs/ca.crt",
  "telemetry_interval": "10s"
}
JSON
    echo
    echo "✓ provisioned. files in $out_dir/:"
    ls -la "$out_dir"
    ;;

  destroy)
    terraform destroy -auto-approve -var "device_id=$device_id"
    rm -rf "out/$device_id"
    echo "✓ destroyed $device_id"
    ;;

  *)
    echo "unknown action: $action" >&2
    exit 2
    ;;
esac
