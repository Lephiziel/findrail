package smoke

type processTree interface {
	stop() error
	cleanup()
}
