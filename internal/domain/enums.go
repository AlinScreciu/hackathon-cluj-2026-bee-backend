package domain

type Role string

const (
	RoleApicultor Role = "apicultor"
	RoleFermier   Role = "fermier"
	RoleInspector Role = "inspector"
)

type Toxicity string

const (
	ToxicityLow  Toxicity = "T-"
	ToxicityMed  Toxicity = "T"
	ToxicityHigh Toxicity = "T+"
)

type PushState string

const (
	PushStatePending   PushState = "pending"
	PushStateSent      PushState = "sent"
	PushStateDelivered PushState = "delivered"
	PushStateOpened    PushState = "opened"
)

type CallState string

const (
	CallStateSkipped   CallState = "skipped"
	CallStateQueued    CallState = "queued"
	CallStateRinging   CallState = "ringing"
	CallStateAnswered  CallState = "answered"
	CallStateConfirmed CallState = "confirmed"
	CallStateNoInput   CallState = "no_input"
	CallStateNoAnswer  CallState = "no_answer"
	CallStateBusy      CallState = "busy"
	CallStateFailed    CallState = "failed"
	CallStateHungUp    CallState = "hung_up"
)

type SmsState string

const (
	SmsStateSkipped   SmsState = "skipped"
	SmsStateQueued    SmsState = "queued"
	SmsStateSent      SmsState = "sent"
	SmsStateDelivered SmsState = "delivered"
	SmsStateConfirmed SmsState = "confirmed"
	SmsStateNoReply   SmsState = "no_reply"
	SmsStateFailed    SmsState = "failed"
)

type FinalStatus string

const (
	FinalStatusConfirmedCall FinalStatus = "confirmed_call"
	FinalStatusConfirmedSMS  FinalStatus = "confirmed_sms"
	FinalStatusConfirmedApp  FinalStatus = "confirmed_app"
	FinalStatusUnconfirmed   FinalStatus = "unconfirmed"
	FinalStatusFailed        FinalStatus = "failed"
)

type SprayStatus string

const (
	SprayStatusScheduled  SprayStatus = "scheduled"
	SprayStatusInProgress SprayStatus = "in_progress"
	SprayStatusCompleted  SprayStatus = "completed"
	SprayStatusCancelled  SprayStatus = "cancelled"
)

type ApiaryType string

const (
	ApiaryTypePermanent ApiaryType = "permanent"
	ApiaryTypePastoral  ApiaryType = "pastoral"
)

type DamageClaimStatus string

const (
	DamageClaimFiled       DamageClaimStatus = "filed"
	DamageClaimUnderReview DamageClaimStatus = "under_review"
	DamageClaimAccepted    DamageClaimStatus = "accepted"
	DamageClaimRejected    DamageClaimStatus = "rejected"
)

type AuthMethod string

const (
	AuthMethodPush  AuthMethod = "push"
	AuthMethodSMS   AuthMethod = "sms"
	AuthMethodEmail AuthMethod = "email"
)

type InAppAction string

const (
	InAppActionMoveHives   InAppAction = "move_hives"
	InAppActionSealInPlace InAppAction = "seal_in_place"
)
