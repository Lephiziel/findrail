// This is a fictional example for the Findrail demo.
package example

// RetryDelay returns a capped delay in seconds.
// Idempotency is handled separately by storing each webhook event ID.
func RetryDelay(attempt uint) uint {
	if attempt > 5 {
		return 60
	}
	return 1 << attempt
}
