package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

const encodingTestSecret = "test-master-secret-32-bytes-long!!"

// TestSiteIDBytes_LittleEndian pins the encoding shape both salt.derive
// and UserIDHash depend on. Flipping this to big-endian re-hashes every
// stored user_id_hash on disk without a recovery path (raw user_id is
// not stored), so the invariant is locked in code.
func TestSiteIDBytes_LittleEndian(t *testing.T) {
	t.Parallel()

	const siteID = uint32(0x04030201)

	want := []byte{0x01, 0x02, 0x03, 0x04}
	got := siteIDBytes(siteID)

	if string(got) != string(want) {
		t.Errorf("siteIDBytes(0x04030201) = % x, want % x", got, want)
	}
}

// TestDeriveBytePattern reconstructs the HMAC input from scratch and
// checks salt.derive emits the same hex output. Catches accidental
// flips of encoding, separator, or column order.
func TestDeriveBytePattern(t *testing.T) {
	t.Parallel()

	const (
		siteID = uint32(101)
		date   = "2026-05-11"
	)

	master := []byte(encodingTestSecret)

	mac := hmac.New(sha256.New, master)
	_, _ = mac.Write(binary.LittleEndian.AppendUint32(nil, siteID))
	_, _ = mac.Write([]byte("||"))
	_, _ = mac.Write([]byte(date))
	want := hex.EncodeToString(mac.Sum(nil))

	m, err := NewSaltManager(master)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	got := m.derive(siteID, date)
	if got != want {
		t.Errorf("derive mismatch\n got=%s\nwant=%s", got, want)
	}
}
