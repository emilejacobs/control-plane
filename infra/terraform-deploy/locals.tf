# Shared Postgres connection wiring for every CP service that talks to the DB
# (issue #49). The password is injected straight from the RDS-managed master
# secret (`<arn>:password::` JSON-key reference); everything else is non-secret
# env. cp-api/cp-ingest/audit-mirror/taxonomy-sync build the DSN in-process via
# storage.ResolveDSN. This replaces the hand-synced uknomi/cp/db-dsn secret,
# which went stale on every RDS master-password rotation and silently took the
# control plane down.
locals {
  db_environment = [
    { name = "DB_HOST", value = aws_db_instance.main.address },
    { name = "DB_PORT", value = tostring(aws_db_instance.main.port) },
    { name = "DB_NAME", value = aws_db_instance.main.db_name },
    { name = "DB_USER", value = aws_db_instance.main.username },
    { name = "DB_SSLMODE", value = "require" },
  ]

  db_password_secret = {
    name      = "DB_PASSWORD"
    valueFrom = "${aws_db_instance.main.master_user_secret[0].secret_arn}:password::"
  }
}
