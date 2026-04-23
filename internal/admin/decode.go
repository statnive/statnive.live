// Package admin owns the HTTP CRUD surface for operator actions:
// user provisioning, goal definitions. Every mutation is gated by
// auth.RequireRole(auth.RoleAdmin) at the router level (see
// cmd/statnive-live/main.go), so handlers in this package assume
// UserFrom(ctx) returns a non-nil admin user.
//
// Every write handler uses DecodeAllowed below — never raw
// json.NewDecoder. Enforced by the blake3-hmac-identity-review skill's
// `admin-no-raw-json-decoder` Semgrep rule.
package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
)

// MaxBodyBytes caps every admin request body. Larger than login
// (8 KB) because goal-create carries a pattern string + name up to
// 128 bytes; 4 KB total is plenty.
const MaxBodyBytes = 4 << 10 // 4 KiB

// ErrMalformedBody — decode failure, body too big, or unknown-field
// attempt. Handlers map to HTTP 400 / 422 depending on context.
var ErrMalformedBody = errors.New("admin: malformed body")

// DecodeAllowed is the F4 mass-assignment guard (PLAN.md Verification
// §52). Every write handler in internal/admin/ MUST call this instead
// of json.NewDecoder directly.
//
// Contract:
//  1. Body is bounded by MaxBodyBytes (defense against oversized POSTs
//     that would pin an in-flight admin request).
//  2. Unknown fields are rejected via DisallowUnknownFields — the
//     canonical attack `{"role":"admin","site_id":99,"is_admin":true}`
//     against a login body returns ErrMalformedBody.
//  3. allowedFields is a belt-and-braces check: after decoding, every
//     *_test.go runs a reflection pass asserting the dst struct's
//     json-tag set is a subset of allowedFields. Catches future
//     refactors that widen the accepted surface without updating the
//     allow-list.
//
// Sensitive fields (site_id, role, is_admin, user_id) MUST be absent
// from allowedFields — they come from auth.UserFrom(ctx) or path
// params, never the request body.
func DecodeAllowed(r *http.Request, dst any, allowedFields []string) error {
	if r == nil || r.Body == nil {
		return fmt.Errorf("%w: nil request body", ErrMalformedBody)
	}

	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedBody, err)
	}

	if err := assertJSONTagsSubset(dst, allowedFields); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedBody, err)
	}

	return nil
}

// assertJSONTagsSubset walks dst's top-level json tags + verifies
// they're all in allowedFields. Protects against a future refactor
// that adds a new typed field to the struct but forgets to update the
// allow-list — the handler would then silently accept a new attack
// surface. Called once per request (admin throughput is ~10 RPS max;
// reflection cost is negligible).
func assertJSONTagsSubset(dst any, allowedFields []string) error {
	v := reflect.ValueOf(dst)
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return errors.New("dst is nil")
		}

		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return errors.New("dst is not a struct")
	}

	allow := make(map[string]struct{}, len(allowedFields))
	for _, f := range allowedFields {
		allow[f] = struct{}{}
	}

	t := v.Type()
	extras := make([]string, 0)

	for i := range t.NumField() {
		tag := strings.SplitN(t.Field(i).Tag.Get("json"), ",", 2)[0]
		if tag == "" || tag == "-" {
			continue
		}

		if _, ok := allow[tag]; !ok {
			extras = append(extras, tag)
		}
	}

	if len(extras) > 0 {
		sort.Strings(extras)

		return fmt.Errorf("dst struct has fields outside allow-list: %v", extras)
	}

	return nil
}
