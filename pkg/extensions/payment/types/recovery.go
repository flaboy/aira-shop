package types

const (
	PaymentApprovalRequired  = "approval_required"
	PaymentRequiresAction    = "requires_action"
	PaymentProcessing        = "processing"
	PaymentSettlementPending = "settlement_pending"
	PaymentSucceeded         = "succeeded"
	PaymentFailed            = "failed"
	PaymentNoAttempt         = "no_attempt"
)

// RecoveryResult 仅包含浏览器继续同一笔支付所需的公开状态和批准链接。
type RecoveryResult struct {
	Status      string
	ApprovalURL string
	ExternalID  string
}
