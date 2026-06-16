output "device_id" {
  description = "Thing name."
  value       = aws_iot_thing.device.name
}

output "broker_url" {
  description = "MQTT-over-WSS broker URL for the agent config."
  value       = "tls://${data.aws_iot_endpoint.ats.endpoint_address}:8883"
}

output "cert_pem" {
  description = "Device certificate PEM. Write to /etc/uknomi/certs/device.crt on the device."
  value       = aws_iot_certificate.device.certificate_pem
  sensitive   = true
}

output "private_key" {
  description = "Device private key PEM. Write to /etc/uknomi/certs/device.key (mode 0600). State holds this — see README on protecting state."
  value       = aws_iot_certificate.device.private_key
  sensitive   = true
}

output "bootstrap_key_secret_arn" {
  description = "ARN of the bootstrap-key secret. cp-api reads it via CP_BOOTSTRAP_SECRET_ID (default name uknomi/cp/bootstrap-key)."
  value       = aws_secretsmanager_secret.bootstrap_key.arn
}
