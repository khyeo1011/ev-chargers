#!/usr/bin/env bash
# Deletes everything aws-setup.sh created. Safe to run more than once.
set -uo pipefail

export AWS_REGION="${AWS_REGION:-us-west-2}"
export AWS_PAGER=""

PREFIX="ev-chargers"
APP_USER="$PREFIX-app"
RULE="$PREFIX-created"

echo "IAM user (keys and inline policy must go first)"
for key in $(aws iam list-access-keys --user-name "$APP_USER" \
    --query 'AccessKeyMetadata[].AccessKeyId' --output text 2>/dev/null); do
  aws iam delete-access-key --user-name "$APP_USER" --access-key-id "$key"
done
aws iam delete-user-policy --user-name "$APP_USER" --policy-name "$PREFIX-least-privilege" 2>/dev/null
aws iam delete-user --user-name "$APP_USER" 2>/dev/null

echo "EventBridge rule (targets must go first)"
aws events remove-targets --rule "$RULE" --ids sqs >/dev/null 2>&1
aws events delete-rule --name "$RULE" 2>/dev/null

echo "SQS queues"
for q in "$PREFIX-events" "$PREFIX-events-dlq"; do
  url=$(aws sqs get-queue-url --queue-name "$q" --query QueueUrl --output text 2>/dev/null) \
    && aws sqs delete-queue --queue-url "$url"
done

echo "DynamoDB table"
aws dynamodb delete-table --table-name "$PREFIX" >/dev/null 2>&1

rm -f "$(dirname "$0")/../.env"
echo "Teardown complete."
