# UknomiAgentPolicy — the corrected shape that actually works with both the
# device agent and the agent-cli developer harness. See Phase 0 issue 10 for
# why the runbook's original policy didn't work with the CLI.
#
# - Connect is scoped to client/*: AWS IoT substitutes the Connection.Thing
#   variable only when client_id == thing name. The CLI uses a random
#   agent-cli-<hex> client_id, so the substituted-variant denied it. The cert
#   principal still gates everything below — only the session-label
#   restriction is loosened.
# - Subscribe is symmetric with Publish: the device subscribes to /cmd, the
#   CLI subscribes to /cmd-result, telemetry observers subscribe to /telemetry.
#
# Phase 1 task: split into a tight device-side policy and a broader operator
# policy bound to a separate "controller" cert (Phase 0 issue 10 path B).
resource "aws_iot_policy" "agent" {
  name = "UknomiAgentPolicy"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = "iot:Connect"
        Resource = "arn:aws:iot:*:*:client/*"
      },
      {
        Effect = "Allow"
        Action = ["iot:Publish", "iot:Receive"]
        Resource = [
          "arn:aws:iot:*:*:topic/devices/$${iot:Connection.Thing.ThingName}/cmd",
          "arn:aws:iot:*:*:topic/devices/$${iot:Connection.Thing.ThingName}/cmd-result",
          "arn:aws:iot:*:*:topic/devices/$${iot:Connection.Thing.ThingName}/telemetry",
          # Phase 2 service-status reports: the agent's
          # ServiceStatusPublisher emits a typed servicestatus.Report on
          # this topic every 5 minutes. AWS IoT silently drops publishes
          # to disallowed topics (broker-layer PUBACKs still succeed),
          # so omitting this line was the cause of "agent looks healthy,
          # cp-ingest sees nothing" in the Wave 0 bench upgrade.
          "arn:aws:iot:*:*:topic/devices/$${iot:Connection.Thing.ThingName}/service-status",
          # Phase 2 issue #19 fleet health probes: the agent's
          # ProbePublisher emits a healthprobes.Report on this topic every
          # 5 minutes. Same silent-drop trap as service-status above —
          # omitting it made the agent look healthy (heartbeats on
          # /telemetry still flow) while cp-ingest saw zero probe reports.
          "arn:aws:iot:*:*:topic/devices/$${iot:Connection.Thing.ThingName}/health-probes",
        ]
      },
      {
        Effect = "Allow"
        Action = "iot:Subscribe"
        Resource = [
          "arn:aws:iot:*:*:topicfilter/devices/$${iot:Connection.Thing.ThingName}/cmd",
          "arn:aws:iot:*:*:topicfilter/devices/$${iot:Connection.Thing.ThingName}/cmd-result",
          "arn:aws:iot:*:*:topicfilter/devices/$${iot:Connection.Thing.ThingName}/telemetry",
        ]
      },
    ]
  })
}
