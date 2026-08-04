package deployment

type EnvironmentKind string

const (
	EnvironmentProduction EnvironmentKind = "production"
	EnvironmentPreview    EnvironmentKind = "preview"
)

// EnvironmentContext keeps ephemeral deployment metadata out of the durable
// service snapshot while giving production and preview runtimes one contract.
type EnvironmentContext struct {
	DeploymentID   string
	Kind           EnvironmentKind
	PreviewURL     string
	SourceRevision string
	CommitMessage  string
}
