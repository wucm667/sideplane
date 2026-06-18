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
	// ActionNodeEnroll records successful node enrollment.
	ActionNodeEnroll = "node.enroll"
	// ActionJobCreate records operator job creation.
	ActionJobCreate = "job.create"
	// ActionJobComplete records sidecar job completion.
	ActionJobComplete = "job.complete"
	// ActionJobFail records sidecar job failure.
	ActionJobFail = "job.fail"
	// ActionConfigApply records operator creation of a signed config apply plan.
	ActionConfigApply = "config.apply"
	// ActionDesiredConfigUpdate records operator updates to desired config.
	ActionDesiredConfigUpdate = "config.desired.update"
)
