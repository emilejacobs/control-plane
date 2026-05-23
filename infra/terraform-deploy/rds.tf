# RDS Postgres — the CP's source of truth. Single-AZ for Wave 0 with a
# variable to flip to multi-AZ before the ship gate. RDS manages the master
# password in its own Secrets Manager secret (rotated by AWS, encrypted with
# our KMS key); cp-api consumes a constructed DSN secret the operator
# populates once after first apply.

resource "aws_db_subnet_group" "main" {
  name       = "uknomi-cp"
  subnet_ids = aws_subnet.private[*].id
  tags       = { Name = "uknomi-cp" }
}

resource "aws_db_parameter_group" "main" {
  name        = "uknomi-cp-postgres16"
  family      = "postgres16"
  description = "uknomi-cp Postgres 16 parameters: TLS-only, log DDL + slow queries."

  parameter {
    name  = "rds.force_ssl"
    value = "1"
  }

  parameter {
    name  = "log_statement"
    value = "ddl"
  }

  parameter {
    name  = "log_min_duration_statement"
    value = "1000"
  }

  tags = { Name = "uknomi-cp-postgres16" }
}

# RDS export log group, created before the instance so retention is set
# from day one rather than RDS defaulting to never-expire.
resource "aws_cloudwatch_log_group" "rds_postgresql" {
  name              = "/aws/rds/instance/uknomi-cp/postgresql"
  retention_in_days = var.db_log_retention_days
  tags              = { Name = "uknomi-cp-postgresql" }
}

resource "aws_db_instance" "main" {
  identifier = "uknomi-cp"

  engine                = "postgres"
  engine_version        = var.db_engine_version
  instance_class        = var.db_instance_class
  allocated_storage     = var.db_allocated_storage
  max_allocated_storage = var.db_max_allocated_storage
  storage_type          = "gp3"
  storage_encrypted     = true
  kms_key_id            = aws_kms_key.main.arn

  db_name  = "uknomi_cp"
  username = "uknomi_admin"

  # RDS owns the master password lifecycle: it generates the value, stores
  # it in a Secrets Manager secret encrypted with our KMS key, and rotates
  # on demand. cp-api never reads this secret directly — see uknomi/cp/db-dsn
  # in secrets.tf.
  manage_master_user_password   = true
  master_user_secret_kms_key_id = aws_kms_key.main.arn

  multi_az               = var.db_multi_az
  db_subnet_group_name   = aws_db_subnet_group.main.name
  parameter_group_name   = aws_db_parameter_group.main.name
  vpc_security_group_ids = [aws_security_group.rds.id]
  publicly_accessible    = false

  backup_retention_period = var.db_backup_retention_days
  backup_window           = "07:00-08:00"
  maintenance_window      = "sun:08:00-sun:09:00"

  deletion_protection       = true
  skip_final_snapshot       = false
  final_snapshot_identifier = "uknomi-cp-final-snapshot"

  performance_insights_enabled          = true
  performance_insights_kms_key_id       = aws_kms_key.main.arn
  performance_insights_retention_period = 7

  enabled_cloudwatch_logs_exports = ["postgresql"]

  tags = { Name = "uknomi-cp" }

  depends_on = [aws_cloudwatch_log_group.rds_postgresql]
}
