package policy

// Config contains repository and workflow policy defaults.
type Config struct {
	Repo        RepoConfig
	Integration IntegrationConfig
	Publish     PublishConfig
	Checks      ChecksConfig
}

// RepoConfig holds repository-level settings.
type RepoConfig struct {
	ProtectedBranch      string
	RemoteName           string
	MainWorktree         string
	WorktreeLayoutPolicy string
	WorktreeRootPrefix   string
	HookPolicy           string
}

// IntegrationConfig holds integration policy defaults.
type IntegrationConfig struct {
	Strategy            string
	SyncPolicy          string
	DirtyWorktreePolicy string
	MaxQueueDepth       int
}

// PublishConfig holds publish policy defaults.
type PublishConfig struct {
	Mode              string
	Coalesced         bool
	InterruptInflight bool
}

// ChecksConfig holds configured shell checks and timeout policy.
type ChecksConfig struct {
	PreIntegrate   []string
	PrePublish     []string
	CommandTimeout string
}

// DefaultConfig returns the default milestone-zero policy scaffold.
func DefaultConfig() Config {
	return Config{
		Repo: RepoConfig{
			ProtectedBranch:      "main",
			RemoteName:           "origin",
			MainWorktree:         "",
			WorktreeLayoutPolicy: "any",
			WorktreeRootPrefix:   "",
			HookPolicy:           "inherit",
		},
		Integration: IntegrationConfig{
			Strategy:            "rebase-then-ff",
			SyncPolicy:          "sync-before-integrate",
			DirtyWorktreePolicy: "reject",
			MaxQueueDepth:       0,
		},
		Publish: PublishConfig{
			Mode:              "manual",
			Coalesced:         true,
			InterruptInflight: false,
		},
		Checks: ChecksConfig{
			PreIntegrate:   []string{},
			PrePublish:     []string{},
			CommandTimeout: "5m",
		},
	}
}
