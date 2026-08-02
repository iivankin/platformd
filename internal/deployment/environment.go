package deployment

type EnvironmentKind string

const (
	EnvironmentProduction EnvironmentKind = "production"
	EnvironmentPreview    EnvironmentKind = "preview"
)

// EnvironmentContext keeps ephemeral deployment metadata out of the durable
// service snapshot while giving build and runtime resolution one contract.
type EnvironmentContext struct {
	DeploymentID      string
	Kind              EnvironmentKind
	PreviewURL        string
	PullRequestNumber int
	SourceRevision    string
	CommitMessage     string
}
