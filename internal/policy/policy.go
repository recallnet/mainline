package policy

// Config contains repository and workflow policy defaults.
type Config struct {
	Repo        RepoConfig
	Integration IntegrationConfig
	Publish     PublishConfig
}

// RepoConfig holds repository-level settings.
type RepoConfig struct {
	ProtectedBranch string
	RemoteName      string
}

// IntegrationConfig holds integration policy defaults.
type IntegrationConfig struct {
	Strategy   string
	SyncPolicy string
}

// PublishConfig holds publish policy defaults.
type PublishConfig struct {
	Mode      string
	Coalesced bool
}

// DefaultConfig returns the default milestone-zero policy scaffold.
func DefaultConfig() Config {
	return Config{
		Repo: RepoConfig{
			ProtectedBranch: "main",
			RemoteName:      "origin",
		},
		Integration: IntegrationConfig{
			Strategy:   "rebase-then-ff",
			SyncPolicy: "sync-before-integrate",
		},
		Publish: PublishConfig{
			Mode:      "manual",
			Coalesced: true,
		},
	}
}
