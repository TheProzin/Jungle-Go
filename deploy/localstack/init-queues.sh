#!/bin/sh
set -eu

ACCOUNT_ID="000000000000"

awslocal sqs create-queue \
  --queue-name wager-transactions-dlq.fifo \
  --attributes FifoQueue=true

DLQ_URL="http://localhost:4566/${ACCOUNT_ID}/wager-transactions-dlq.fifo"
DLQ_ARN=$(awslocal sqs get-queue-attributes \
  --queue-url "$DLQ_URL" \
  --attribute-names QueueArn \
  --query 'Attributes.QueueArn' \
  --output text)

awslocal sqs create-queue \
  --queue-name wager-transactions.fifo \
  --attributes FifoQueue=true,ContentBasedDeduplication=false

TX_URL="http://localhost:4566/${ACCOUNT_ID}/wager-transactions.fifo"
TX_ATTRIBUTES=$(printf '{"VisibilityTimeout":"60","MessageRetentionPeriod":"345600","RedrivePolicy":"{\\"deadLetterTargetArn\\":\\"%s\\",\\"maxReceiveCount\\":\\"5\\"}"}' "$DLQ_ARN")

awslocal sqs set-queue-attributes \
  --queue-url "$TX_URL" \
  --attributes "$TX_ATTRIBUTES"

awslocal sqs create-queue \
  --queue-name wager-events.fifo \
  --attributes FifoQueue=true,ContentBasedDeduplication=false

cat > /tmp/wager-consumer-policy.json <<'JSON'
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "sqs:ReceiveMessage",
        "sqs:DeleteMessage",
        "sqs:ChangeMessageVisibility",
        "sqs:GetQueueAttributes"
      ],
      "Resource": "arn:aws:sqs:us-east-1:000000000000:wager-transactions.fifo"
    },
    {
      "Effect": "Allow",
      "Action": "sqs:SendMessage",
      "Resource": [
        "arn:aws:sqs:us-east-1:000000000000:wager-events.fifo",
        "arn:aws:sqs:us-east-1:000000000000:wager-transactions-dlq.fifo"
      ]
    }
  ]
}
JSON

cat > /tmp/wager-producer-policy.json <<'JSON'
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "sqs:SendMessage",
      "Resource": "arn:aws:sqs:us-east-1:000000000000:wager-transactions.fifo"
    }
  ]
}
JSON

awslocal iam create-user --user-name wager-consumer || true
awslocal iam put-user-policy \
  --user-name wager-consumer \
  --policy-name WagerConsumerPolicy \
  --policy-document file:///tmp/wager-consumer-policy.json

awslocal iam create-user --user-name wager-producer || true
awslocal iam put-user-policy \
  --user-name wager-producer \
  --policy-name WagerProducerPolicy \
  --policy-document file:///tmp/wager-producer-policy.json
