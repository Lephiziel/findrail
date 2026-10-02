# Payment retry notes

This is a fictional document for the Findrail demo.

When a webhook arrives twice, use an idempotency key to avoid applying the
same payment event twice. Keep the event ID before retrying the work.

The Go example describes the retry delay. The PDF runbook describes the
idempotency policy on page 2.

Demo marker: amber
