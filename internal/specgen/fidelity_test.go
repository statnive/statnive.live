package specgen_test

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"

	"github.com/statnive/statnive.live/internal/storage"
)

type fidelityDoc struct {
	Components struct {
		Schemas map[string]struct {
			Properties map[string]any `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

// jsonFields returns the wire field names of a struct (json tag, before comma),
// skipping json:"-".
func jsonFields(t reflect.Type) []string {
	var out []string

	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}

		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}

		out = append(out, name)
	}

	sort.Strings(out)

	return out
}

// TestOverlay_SchemasMatchGoStructs asserts every documented component schema's
// property set exactly matches the Go struct's json fields — catching the
// SEORow-has-no-rpv / DailyPoint.pageviews / FunnelResult.drop_off_pct class of
// drift between the hand-authored overlay and the server's wire types.
func TestOverlay_SchemasMatchGoStructs(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(specPath())
	if err != nil {
		t.Skipf("api/openapi.yaml not present (%v)", err)
	}

	var doc fidelityDoc
	if uErr := yaml.Unmarshal(b, &doc); uErr != nil {
		t.Fatalf("parse openapi.yaml: %v", uErr)
	}

	cases := map[string]reflect.Type{
		"OverviewResult":   reflect.TypeOf(storage.OverviewResult{}),
		"SourceRow":        reflect.TypeOf(storage.SourceRow{}),
		"SourceChannelRow": reflect.TypeOf(storage.SourceChannelRow{}),
		"PageRow":          reflect.TypeOf(storage.PageRow{}),
		"SEORow":           reflect.TypeOf(storage.SEORow{}),
		"CampaignRow":      reflect.TypeOf(storage.CampaignRow{}),
		"DailyPoint":       reflect.TypeOf(storage.DailyPoint{}),
		"RealtimeResult":   reflect.TypeOf(storage.RealtimeResult{}),
		"GeoRow":           reflect.TypeOf(storage.GeoRow{}),
		"GeoTopRow":        reflect.TypeOf(storage.GeoTopRow{}),
		"DeviceRow":        reflect.TypeOf(storage.DeviceRow{}),
		"FunnelResult":     reflect.TypeOf(storage.FunnelResult{}),
		"VariantRow":       reflect.TypeOf(storage.VariantRow{}),
		"CompareResult":    reflect.TypeOf(storage.CompareResult{}),
		"PropNameRow":      reflect.TypeOf(storage.PropNameRow{}),
	}

	for name, typ := range cases {
		sch, ok := doc.Components.Schemas[name]
		if !ok {
			t.Errorf("schema %s missing from openapi.yaml components", name)
			continue
		}

		want := jsonFields(typ)

		got := make([]string, 0, len(sch.Properties))
		for k := range sch.Properties {
			got = append(got, k)
		}

		sort.Strings(got)

		if strings.Join(want, ",") != strings.Join(got, ",") {
			t.Errorf("schema %s property drift:\n  go struct: %v\n  overlay:   %v", name, want, got)
		}
	}
}
