// Package schema defines the AOEP-v0 event stream types (arXiv:2606.30306 §9.2).
package schema

// Operation is the closed set of 11 typed operations an AOEP event can carry.
type Operation string

const (
	OpRead       Operation = "read"
	OpWrite      Operation = "write"
	OpUpdate     Operation = "update"
	OpDelete     Operation = "delete"
	OpTombstone  Operation = "tombstone"
	OpShare      Operation = "share"
	OpUnshare    Operation = "unshare"
	OpValidate   Operation = "validate"
	OpQuarantine Operation = "quarantine"
	OpDeny       Operation = "deny"
	OpRollback   Operation = "rollback"
)

// TrustTier distinguishes trusted (human/owner) from untrusted (tool output, injected) sources.
type TrustTier string

const (
	TrustTrusted   TrustTier = "trusted"
	TrustUntrusted TrustTier = "untrusted"
)

// RetentionPolicy describes how long a memory should persist.
type RetentionPolicy string

const (
	RetentionDurable     RetentionPolicy = "durable"
	RetentionEphemeral   RetentionPolicy = "ephemeral"
	RetentionSessionOnly RetentionPolicy = "session_only"
)

// PrivacyClass tags the sensitivity level of the payload.
type PrivacyClass string

const (
	PrivacyUser         PrivacyClass = "user"
	PrivacyShared       PrivacyClass = "shared"
	PrivacySystem       PrivacyClass = "system"
	PrivacyConfidential PrivacyClass = "confidential"
)

// Provenance carries trust attribution for the event source.
type Provenance struct {
	TrustTier TrustTier `json:"trust_tier"`
	Signature string    `json:"signature,omitempty"`
}

// Causal links this event to prior events in the episode.
type Causal struct {
	Parent        string `json:"parent,omitempty"`
	Supersedes    string `json:"supersedes,omitempty"`
	ConflictsWith string `json:"conflicts_with,omitempty"`
	TransactionID string `json:"transaction_id,omitempty"`
}

// Retention describes the storage and lifecycle constraints on the payload.
type Retention struct {
	Policy               RetentionPolicy `json:"policy"`
	PrivacyClass         PrivacyClass    `json:"privacy_class"`
	TTL                  *int            `json:"ttl,omitempty"` // seconds; nil = no expiry
	RequiresConfirmation bool            `json:"requires_confirmation,omitempty"`
}

// Event is a single step in an AOEP episode stream.
// OutcomeExpected is stripped by the episode loader before delivery to the system under test.
type Event struct {
	ID              string         `json:"id"`
	IdempotencyKey  string         `json:"idempotency_key"`
	Operation       Operation      `json:"operation"`
	Actor           string         `json:"actor"`
	ActorTrustTier  TrustTier      `json:"actor_trust_tier"`
	Scope           string         `json:"scope"`
	PermissionEpoch int            `json:"permission_epoch"`
	Provenance      Provenance     `json:"provenance"`
	Causal          Causal         `json:"causal"`
	Retention       Retention      `json:"retention"`
	Payload         map[string]any `json:"payload"`

	// Stripped before SUT delivery; used only by the validator.
	OutcomeExpected *ExpectedOutcome `json:"outcome_expected,omitempty"`
}

// ExpectedOutcome records what a governed system should produce for this event.
// The harness uses this for ground-truth validation without giving the SUT any hints.
type ExpectedOutcome struct {
	// ResourceID is the canonical identifier that should be created/updated.
	ResourceID string `json:"resource_id,omitempty"`
	// ShouldBlock is true when a governed system must reject this event.
	ShouldBlock bool `json:"should_block,omitempty"`
	// IdempotentWith is the event ID whose write this duplicates (restart replay).
	IdempotentWith string `json:"idempotent_with,omitempty"`
	// LedgerEntry records what should appear in the deletion or rollback ledger.
	LedgerEntry *LedgerEntry `json:"ledger_entry,omitempty"`
}

// LedgerEntry is a record the harness expects to find in the deletion or rollback ledger.
type LedgerEntry struct {
	ResourceID    string `json:"resource_id"`
	Scope         string `json:"scope"`
	Actor         string `json:"actor"`
	PermEpochAtOp int    `json:"perm_epoch_at_op"`
}

// Probe is a neutral post-episode query posed to the system under test.
// It never names the expected value or the invariant under test.
type Probe struct {
	ID             string    `json:"id"`
	Actor          string    `json:"actor"`
	TargetResource string    `json:"target_resource"`
	TargetScope    string    `json:"target_scope"`
	Query          ProbeKind `json:"query"`
}

// ProbeKind describes what kind of information the probe requests.
type ProbeKind string

const (
	ProbeRead            ProbeKind = "read"             // retrieve value of resource
	ProbeList            ProbeKind = "list"             // list all resources in scope
	ProbeDeletionLedger  ProbeKind = "deletion_ledger"  // retrieve tombstone records
	ProbeRollbackLedger  ProbeKind = "rollback_ledger"  // retrieve rollback records
	ProbePermissionEpoch ProbeKind = "permission_epoch" // current epoch for resource
	ProbeConflicts       ProbeKind = "conflicts"        // pending conflict list
	ProbeTrustTier       ProbeKind = "trust_tier"       // provenance/trust of resource
)

// ProbeResponse is what the system under test returns for a probe.
type ProbeResponse struct {
	ProbeID string `json:"probe_id"`
	// Value is the raw response from the SUT (nil if not found / blocked).
	Value any    `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}
