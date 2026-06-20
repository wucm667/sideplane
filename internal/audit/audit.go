// Package audit defines stable audit action and actor labels.
package audit

const (
	// ActorOperator is used for operator-initiated API actions.
	ActorOperator = "operator"
	// ActorSidecar is used for sidecar-authenticated callbacks.
	ActorSidecar = "sidecar"
	// ActorNode is used for node enrollment actions.
	ActorNode = "node"

	// ActionEnrollmentTokenCreate records one-time enrollment token creation.
	ActionEnrollmentTokenCreate = "enrollment.token.create"
	// ActionOperatorTokenCreate records named operator token creation.
	ActionOperatorTokenCreate = "operator.token.create"
	// ActionOperatorTokenList records named operator token metadata listing.
	ActionOperatorTokenList = "operator.token.list"
	// ActionOperatorTokenRevoke records named operator token revocation.
	ActionOperatorTokenRevoke = "operator.token.revoke"
	// ActionNodeEnroll records successful node enrollment.
	ActionNodeEnroll = "node.enroll"
	// ActionNodeDelete records operator removal of a node from inventory.
	ActionNodeDelete = "node.delete"
	// ActionNodeLabelsUpdate records operator updates to node labels.
	ActionNodeLabelsUpdate = "node.labels.update"
	// ActionJobCreate records operator job creation.
	ActionJobCreate = "job.create"
	// ActionJobBulkCreate records bulk job creation across a node selection.
	ActionJobBulkCreate = "job.bulk.create"
	// ActionJobComplete records sidecar job completion.
	ActionJobComplete = "job.complete"
	// ActionJobFail records sidecar job failure.
	ActionJobFail = "job.fail"
	// ActionConfigApply records operator creation of a signed config apply plan.
	ActionConfigApply = "config.apply"
	// ActionRestart records operator creation of a standalone restart job.
	ActionRestart = "restart"
	// ActionRollback records operator creation of an explicit rollback job.
	ActionRollback = "rollback"
	// ActionRolloutCreate records operator creation of a staged rollout.
	ActionRolloutCreate = "rollout.create"
	// ActionRolloutPause records operator pausing a staged rollout.
	ActionRolloutPause = "rollout.pause"
	// ActionRolloutResume records operator resuming a staged rollout.
	ActionRolloutResume = "rollout.resume"
	// ActionRolloutAbort records operator aborting a staged rollout.
	ActionRolloutAbort = "rollout.abort"
	// ActionDesiredConfigUpdate records operator updates to desired config.
	ActionDesiredConfigUpdate = "config.desired.update"
	// ActionDesiredConfigRevert records operator reverts to desired config.
	ActionDesiredConfigRevert = "config.desired.revert"
)
