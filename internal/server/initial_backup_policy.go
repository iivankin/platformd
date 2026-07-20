package server

import "github.com/iivankin/platformd/internal/state"

type initialBackupPolicyRequest struct {
	TargetID       string `json:"targetId"`
	Enabled        bool   `json:"enabled"`
	Cron           string `json:"cron"`
	RetentionCount int    `json:"retentionCount"`
}

func (request initialBackupPolicyRequest) statePolicy() state.InitialBackupPolicy {
	return state.InitialBackupPolicy{
		TargetID: request.TargetID, Enabled: request.Enabled,
		Cron: request.Cron, RetentionCount: request.RetentionCount,
	}
}
