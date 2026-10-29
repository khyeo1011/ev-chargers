#!/usr/bin/env bash
# Creates everything the app needs in AWS and writes the settings to .env:
#   1. DynamoDB table   ev-chargers           (on-demand billing)
#   2. SQS queues       ev-chargers-events + ev-chargers-events-dlq
#   3. EventBridge rule ev-chargers-created   (default bus -> SQS)
#   4. Queue policy     letting that rule send to the queue
#   5. IAM user         ev-chargers-app with a least-privilege policy
#
# Cost at practice scale is effectively $0 (all within free tier / pennies).
# Run scripts/aws-teardown.sh when you're done.
#
# Needs: AWS CLI v2 logged in as an admin-ish user, and jq.
set -euo pipefail

command -v aws >/dev/null || { echo "Install the AWS CLI v2 first"; exit 1; }
command -v jq  >/dev/null || { echo "Install jq first (apt install jq / brew install jq)"; exit 1; }

export AWS_REGION="${AWS_REGION:-us-west-2}"
export AWS_PAGER=""

PREFIX="ev-chargers"
TABLE="$PREFIX"
QUEUE="$PREFIX-events"
DLQ="$PREFIX-events-dlq"
RULE="$PREFIX-created"
APP_USER="$PREFIX-app"
ENV_FILE="$(dirname "$0")/../.env"

ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
echo "Account $ACCOUNT_ID, region $AWS_REGION"

echo "1/5 DynamoDB table '$TABLE'"
aws dynamodb create-table \
  --table-name "$TABLE" \
  --attribute-definitions AttributeName=id,AttributeType=S \
  --key-schema AttributeName=id,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST >/dev/null
aws dynamodb wait table-exists --table-name "$TABLE"

echo "2/5 SQS queues (main + dead-letter)"
DLQ_URL=$(aws sqs create-queue --queue-name "$DLQ" --query QueueUrl --output text)
DLQ_ARN=$(aws sqs get-queue-attributes --queue-url "$DLQ_URL" \
  --attribute-names QueueArn --query Attributes.QueueArn --output text)

# After 3 failed receives a message moves to the DLQ instead of looping forever.
REDRIVE=$(jq -nc --arg arn "$DLQ_ARN" '{deadLetterTargetArn: $arn, maxReceiveCount: "3"}')
QUEUE_URL=$(aws sqs create-queue --queue-name "$QUEUE" \
  --attributes "$(jq -nc --arg r "$REDRIVE" '{RedrivePolicy: $r, VisibilityTimeout: "30"}')" \
  --query QueueUrl --output text)
QUEUE_ARN=$(aws sqs get-queue-attributes --queue-url "$QUEUE_URL" \
  --attribute-names QueueArn --query Attributes.QueueArn --output text)

echo "3/5 EventBridge rule '$RULE' -> SQS"
RULE_ARN=$(aws events put-rule --name "$RULE" \
  --event-pattern '{"source":["ev.chargers"],"detail-type":["ChargerCreated"]}' \
  --query RuleArn --output text)
aws events put-targets --rule "$RULE" --targets "Id=sqs,Arn=$QUEUE_ARN" >/dev/null

echo "4/5 Queue policy (only this rule may send to the queue)"
QUEUE_POLICY=$(jq -nc --arg q "$QUEUE_ARN" --arg r "$RULE_ARN" '{
  Version: "2012-10-17",
  Statement: [{
    Effect: "Allow",
    Principal: {Service: "events.amazonaws.com"},
    Action: "sqs:SendMessage",
    Resource: $q,
    Condition: {ArnEquals: {"aws:SourceArn": $r}}
  }]}')
aws sqs set-queue-attributes --queue-url "$QUEUE_URL" \
  --attributes "$(jq -nc --arg p "$QUEUE_POLICY" '{Policy: $p}')"

echo "5/5 IAM user '$APP_USER' with least-privilege policy"
APP_POLICY=$(jq -nc \
  --arg table "arn:aws:dynamodb:$AWS_REGION:$ACCOUNT_ID:table/$TABLE" \
  --arg bus   "arn:aws:events:$AWS_REGION:$ACCOUNT_ID:event-bus/default" \
  --arg queue "$QUEUE_ARN" '{
  Version: "2012-10-17",
  Statement: [
    {Sid: "ChargersTable", Effect: "Allow",
     Action: ["dynamodb:PutItem", "dynamodb:GetItem", "dynamodb:Scan"], Resource: $table},
    {Sid: "PublishEvents", Effect: "Allow",
     Action: "events:PutEvents", Resource: $bus},
    {Sid: "ConsumeQueue", Effect: "Allow",
     Action: ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"], Resource: $queue}
  ]}')
aws iam create-user --user-name "$APP_USER" >/dev/null
aws iam put-user-policy --user-name "$APP_USER" \
  --policy-name "$PREFIX-least-privilege" --policy-document "$APP_POLICY"
KEY_JSON=$(aws iam create-access-key --user-name "$APP_USER")

umask 077
cat > "$ENV_FILE" <<EOF
AWS_REGION=$AWS_REGION
AWS_ACCESS_KEY_ID=$(jq -r .AccessKey.AccessKeyId <<<"$KEY_JSON")
AWS_SECRET_ACCESS_KEY=$(jq -r .AccessKey.SecretAccessKey <<<"$KEY_JSON")
TABLE_NAME=$TABLE
EVENT_BUS_NAME=default
QUEUE_URL=$QUEUE_URL
DLQ_URL=$DLQ_URL
EOF

echo
echo "Done. Settings and the app's access key are in .env (git-ignored)."
echo "IAM can take ~10 seconds to recognize a new key before the first call works."
