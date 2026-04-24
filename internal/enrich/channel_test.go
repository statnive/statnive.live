package enrich_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/statnive/statnive.live/internal/enrich"
)

// channelTestSources is a minimal sources.yaml fixture covering every
// bucket the 17-step ladder discriminates on.
const channelTestSources = `
sources:
  - {name: Google,    channel: Organic Search, domains: [google.com, google.ir, google.de]}
  - {name: Bing,      channel: Organic Search, domains: [bing.com]}
  - {name: Facebook,  channel: Social,         domains: [facebook.com]}
  - {name: YouTube,   channel: Video,          domains: [youtube.com, youtu.be]}
  - {name: Digikala,  channel: Referral,       domains: [digikala.com]}
  - {name: Gmail,     channel: Email,          domains: [mail.google.com]}
  - {name: ChatGPT,   channel: AI,             domains: [chat.openai.com]}
  - {name: Claude,    channel: AI,             domains: [claude.ai]}
`

func newTestMapper(t *testing.T) *enrich.ChannelMapper {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")

	if err := os.WriteFile(path, []byte(channelTestSources), 0o600); err != nil {
		t.Fatalf("write sources: %v", err)
	}

	m, err := enrich.NewChannelMapper(path)
	if err != nil {
		t.Fatalf("new mapper: %v", err)
	}

	t.Cleanup(m.Close)

	return m
}

func TestChannelMapper_DecisionTree(t *testing.T) {
	t.Parallel()

	m := newTestMapper(t)

	cases := []struct {
		name        string
		referrer    string
		utmSource   string
		utmMedium   string
		utmCampaign string
		clickID     string
		wantChan    string
	}{
		{"Direct (no signals)", "", "", "", "", "", "Direct"},
		{"Cross-network beats all", "https://google.com/", "google", "cpc", "summer-cross-network-2026", "", "Cross-network"},
		{"Paid Shopping (campaign hint)", "https://google.com/", "", "cpc", "summer-shop-sale", "", "Paid Shopping"},
		{"Paid Search (search referrer + cpc)", "https://google.com/", "", "cpc", "", "", "Paid Search"},
		{"Paid Search (gclid sentinel)", "https://www.google.de/", "", "", "", "(gclid)", "Paid Search"},
		{"Paid Search (msclkid sentinel)", "https://bing.com/", "", "", "", "(msclkid)", "Paid Search"},
		{"Paid Social", "https://facebook.com/", "", "cpc", "", "", "Paid Social"},
		{"Paid Video", "https://youtube.com/", "", "cpc", "", "", "Paid Video"},
		{"Display medium", "", "", "display", "", "", "Display"},
		{"Paid Other", "", "", "ppc", "", "", "Paid Other"},
		{"Organic Social", "https://facebook.com/", "", "", "", "", "Organic Social"},
		{"Organic Video", "https://youtu.be/abc", "", "", "", "", "Organic Video"},
		{"Organic Search", "https://google.ir/", "", "", "", "", "Organic Search"},
		{"Referral medium", "", "", "referral", "", "", "Referral"},
		{"Referral hostname", "https://digikala.com/p/123", "", "", "", "", "Referral"},
		{"Email (gmail token)", "", "", "email", "", "", "Email"},
		{"Email (referrer hostname)", "https://mail.google.com/", "", "", "", "", "Email"},
		{"AI (ChatGPT referrer)", "https://chat.openai.com/", "", "", "", "", "AI"},
		{"AI (Claude referrer)", "https://claude.ai/c/abc", "", "", "", "", "AI"},
		{"Affiliate", "", "", "affiliate", "", "", "Affiliates"},
		{"SMS", "", "sms", "", "", "", "SMS"},
		{"Push notification", "", "", "mobile-push", "", "", "Mobile Push Notifications"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := m.Classify(tc.referrer, tc.utmSource, tc.utmMedium, tc.utmCampaign, tc.clickID)
			if d.Channel != tc.wantChan {
				t.Errorf("Classify(%q,%q,%q,%q,%q).Channel = %q, want %q",
					tc.referrer, tc.utmSource, tc.utmMedium, tc.utmCampaign, tc.clickID,
					d.Channel, tc.wantChan)
			}
		})
	}
}

func TestChannelMapper_ReferrerNamePopulated(t *testing.T) {
	t.Parallel()

	m := newTestMapper(t)
	d := m.Classify("https://www.google.ir/search?q=foo", "", "", "", "")

	if d.ReferrerName != "google" {
		t.Errorf("ReferrerName = %q, want google", d.ReferrerName)
	}
}
