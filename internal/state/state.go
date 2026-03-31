package state

// Store describes the durable state boundary.
type Store struct {
	Path string
}

// NewStore returns the milestone-zero state scaffold.
func NewStore(path string) Store {
	return Store{Path: path}
}
