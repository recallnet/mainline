package queue

// Manager describes the queue coordination boundary.
type Manager struct {
	Integration IntegrationQueue
	Publish     PublishQueue
}

// IntegrationQueue describes the ordered landing queue.
type IntegrationQueue struct {
	Name string
}

// PublishQueue describes the coalesced publish queue.
type PublishQueue struct {
	Name string
}

// NewManager returns the milestone-zero queue scaffold.
func NewManager() Manager {
	return Manager{
		Integration: IntegrationQueue{Name: "integration"},
		Publish:     PublishQueue{Name: "publish"},
	}
}
