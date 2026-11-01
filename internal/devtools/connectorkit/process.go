package connectorkit

type processTree interface {
	stop() error
	cleanup()
}
