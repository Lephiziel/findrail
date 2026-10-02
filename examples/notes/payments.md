# Payments integration

Webhook deliveries may be duplicated or arrive out of order. Store an event ID
before applying a payment transition, and test retries with an idempotency key.

The provider's API is the source of truth for reconciliation.
