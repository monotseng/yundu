package monitoring

import (
	"encoding/json"
	"testing"
)

func TestZabbixTemplateContract(t *testing.T) {
	var doc struct {
		Export struct {
			Templates []struct {
				Items []json.RawMessage `json:"items"`
			} `json:"templates"`
		} `json:"zabbix_export"`
	}
	if err := json.Unmarshal([]byte(RenderZabbixTemplate()), &doc); err != nil {
		t.Fatalf("invalid template JSON: %v", err)
	}
	if len(doc.Export.Templates) != 1 || len(doc.Export.Templates[0].Items) != 19 {
		t.Fatalf("want one main and 18 dependent items, got %#v", doc.Export.Templates)
	}
}
func TestSnapshotHasFixedSchema(t *testing.T) {
	raw, err := json.Marshal(Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		t.Fatal("invalid JSON")
	}
	if len(value) != 19 {
		t.Fatalf("want 19 fixed response fields, got %d", len(value))
	}
	for _, key := range append([]string{"schema_version", "snapshot_valid", "snapshot_updated_at"}, ZabbixMetricKeys...) {
		if _, ok := value[key]; !ok {
			t.Errorf("missing %s", key)
		}
	}
}
