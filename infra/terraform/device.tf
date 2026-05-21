resource "aws_iot_thing" "device" {
  name = var.device_id
}

resource "aws_iot_certificate" "device" {
  active = true
}

resource "aws_iot_policy_attachment" "device" {
  policy = aws_iot_policy.agent.name
  target = aws_iot_certificate.device.arn
}

resource "aws_iot_thing_principal_attachment" "device" {
  thing     = aws_iot_thing.device.name
  principal = aws_iot_certificate.device.arn
}
