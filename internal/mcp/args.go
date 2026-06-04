package mcp

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/statnive/statnive.live/internal/storage"
	"github.com/statnive/statnive.live/internal/timewindow"
)

// maxToolLimit is the hard server-side row cap. The JSON-Schema maximum is
// advisory (clients/LLMs can ignore it); this clamp is the real enforcement
// and an anti-exfiltration control. storage.Filter.Validate has no upper
// bound and EffectiveLimit is unbounded, so this is the only ceiling.
const maxToolLimit = 500

// toolArgs is the shared tool-call argument shape. `offset` is intentionally
// absent — it was a dead input in the rollup SQL and a deep-page exfil hole
// (see PLAN.md Anti-exfiltration §2); pagination is via narrower filters or
// a larger limit.
type toolArgs struct {
	Site    string     `json:"site"`
	Range   string     `json:"range"`
	Filters filtersArg `json:"filters"`
	Limit   int        `json:"limit"`
	Sort    string     `json:"sort"`
	Dir     string     `json:"dir"`
	Search  string     `json:"search"`
}

// filtersArg maps 1:1 onto the optional dimension filters of storage.Filter.
type filtersArg struct {
	Path        string `json:"path"`
	Referrer    string `json:"referrer"`
	Channel     string `json:"channel"`
	UTMSource   string `json:"utm_source"`
	UTMMedium   string `json:"utm_medium"`
	UTMCampaign string `json:"utm_campaign"`
	UTMContent  string `json:"utm_content"`
	UTMTerm     string `json:"utm_term"`
	Country     string `json:"country"`
	Browser     string `json:"browser"`
	OS          string `json:"os"`
	Device      string `json:"device"`

	HitProps     map[string]string `json:"hit_props"`
	SessionProps map[string]string `json:"session_props"`
	UserProps    map[string]string `json:"user_props"`
}

// decodeStrict unmarshals raw JSON into v with unknown-field rejection, so a
// typo'd or injected arg key becomes a clean -32602 instead of a silently
// ignored constraint. DisallowUnknownFields applies recursively to nested
// structs (filtersArg) in the same Decode call.
func decodeStrict(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	return dec.Decode(v)
}

// clampLimit enforces the hard [0, maxToolLimit] bound. 0 means "use the
// store default" (storage.Filter.EffectiveLimit → 50).
func clampLimit(n int) int {
	switch {
	case n < 0:
		return 0
	case n > maxToolLimit:
		return maxToolLimit
	default:
		return n
	}
}

// buildFilter turns resolved site + args into a validated storage.Filter.
// Range is parsed in the site's location (so days respect the site calendar
// and hours align to the UTC rollup grain). Offset is never set.
func buildFilter(siteID uint32, args toolArgs, loc *time.Location, now time.Time) (*storage.Filter, error) {
	from, to, err := timewindow.ParseRange(args.Range, loc, now)
	if err != nil {
		return nil, err
	}

	f := &storage.Filter{
		SiteID:       siteID,
		From:         from,
		To:           to,
		Path:         args.Filters.Path,
		Referrer:     args.Filters.Referrer,
		Channel:      args.Filters.Channel,
		UTMSource:    args.Filters.UTMSource,
		UTMMedium:    args.Filters.UTMMedium,
		UTMCampaign:  args.Filters.UTMCampaign,
		UTMContent:   args.Filters.UTMContent,
		UTMTerm:      args.Filters.UTMTerm,
		Country:      args.Filters.Country,
		Browser:      args.Filters.Browser,
		OS:           args.Filters.OS,
		Device:       args.Filters.Device,
		HitProps:     args.Filters.HitProps,
		SessionProps: args.Filters.SessionProps,
		UserProps:    args.Filters.UserProps,
		Sort:         args.Sort,
		Dir:          args.Dir,
		Search:       args.Search,
		Limit:        clampLimit(args.Limit),
	}

	if err := f.Validate(); err != nil {
		return nil, err
	}

	return f, nil
}
