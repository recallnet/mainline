package git

// Engine holds repository-local Git execution context.
type Engine struct {
	RepositoryRoot string
}

// NewEngine returns a Git engine rooted at the provided repository path.
func NewEngine(repositoryRoot string) Engine {
	return Engine{RepositoryRoot: repositoryRoot}
}
