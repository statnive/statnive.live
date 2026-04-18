// Package identity computes the per-event visitor_hash and rotates the daily
// IRST salt.
//
// v1 slice: STUB. Returns zero hash so the rest of the pipeline compiles +
// runs end-to-end. Real BLAKE3-128 + HMAC daily salt + cross-day grace land
// in the next plan iteration (PLAN.md:163, verification 22).
//
// SECURITY: when the real implementation lands it MUST use only SHA-256+
// or BLAKE3 in any privacy/identity path (Privacy Rule 3, CLAUDE.md).
package identity

// HasherStub returns a deterministic zero VisitorHash. Sufficient to exercise
// the WAL → consumer → ClickHouse path; integration tests that depend on
// uniqueness of the visitor_hash column will fail when this stub is in
// place — that is intentional, those tests gate the next slice.
type HasherStub struct{}

// NewHasherStub constructs the stub.
func NewHasherStub() *HasherStub { return &HasherStub{} }

// VisitorHash always returns the zero FixedString(16). The IP and user-agent
// arguments are ignored — they're listed so the signature matches the real
// implementation we'll drop in next.
func (HasherStub) VisitorHash(_ uint32, _, _ string) [16]byte {
	return [16]byte{}
}
