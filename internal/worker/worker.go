package worker

// Registry groups worker definitions.
type Registry struct {
	Integration Worker
	Publish     Worker
}

// Worker identifies a worker type.
type Worker struct {
	Name string
}

// NewRegistry returns the milestone-zero worker scaffold.
func NewRegistry() Registry {
	return Registry{
		Integration: Worker{Name: "integration"},
		Publish:     Worker{Name: "publish"},
	}
}
