# Route 53 hosted zone + ACM cert for the Control Plane.
#
# control.uknomi.com is a new sub-zone. The parent uknomi.com lives at the
# registrar (not in this AWS account), so making control.uknomi.com
# publicly resolvable requires a one-time NS delegation:
#
#   terraform apply -target=aws_route53_zone.control
#   terraform output control_zone_nameservers
#   # at the uknomi.com registrar: create 4 NS records for `control`
#   # pointing at the four AWS nameservers above
#
# After the delegation propagates, a full `terraform apply` completes — the
# ACM cert's DNS validation hangs (default 75 min timeout) until then.

resource "aws_route53_zone" "control" {
  name = "control.uknomi.com"
  tags = { Name = "control.uknomi.com" }
}

# Cert for the apex (dashboard) + the api host. ALB host-based routing
# picks the target group from the SNI host.
resource "aws_acm_certificate" "control" {
  domain_name               = "control.uknomi.com"
  subject_alternative_names = ["api.control.uknomi.com"]
  validation_method         = "DNS"

  lifecycle {
    create_before_destroy = true
  }

  tags = { Name = "control.uknomi.com" }
}

resource "aws_route53_record" "control_validation" {
  for_each = {
    for dvo in aws_acm_certificate.control.domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      record = dvo.resource_record_value
      type   = dvo.resource_record_type
    }
  }

  allow_overwrite = true
  zone_id         = aws_route53_zone.control.zone_id
  name            = each.value.name
  type            = each.value.type
  records         = [each.value.record]
  ttl             = 60
}

resource "aws_acm_certificate_validation" "control" {
  certificate_arn         = aws_acm_certificate.control.arn
  validation_record_fqdns = [for r in aws_route53_record.control_validation : r.fqdn]
}

# A-alias records pointing each hostname at the ALB. The ALB itself lives
# in alb.tf; these records depend on it implicitly via the alias target.

resource "aws_route53_record" "dashboard" {
  zone_id = aws_route53_zone.control.zone_id
  name    = "control.uknomi.com"
  type    = "A"

  alias {
    name                   = aws_lb.main.dns_name
    zone_id                = aws_lb.main.zone_id
    evaluate_target_health = true
  }
}

resource "aws_route53_record" "api" {
  zone_id = aws_route53_zone.control.zone_id
  name    = "api.control.uknomi.com"
  type    = "A"

  alias {
    name                   = aws_lb.main.dns_name
    zone_id                = aws_lb.main.zone_id
    evaluate_target_health = true
  }
}
