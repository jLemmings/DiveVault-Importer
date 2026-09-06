package divecomputer

import (
	"encoding/json"
	"testing"
)

func TestTelemetrySampleContract(t *testing.T) {
	s := &samples{row: newRow()}
	s.collect(0, 1500, 0, 0, 0, 0, nil)
	s.collect(1, 0, 0, 0, 0, 12.3, nil)
	s.collect(2, 1, 0, 0, 0, 180, nil)
	s.collect(3, 0, 0, 0, 0, 18, nil)
	s.collect(4, 2, 3, 4, 5, 0, nil)
	s.collect(5, 600, 0, 0, 0, 0, nil)
	s.collect(6, 80, 0, 0, 0, 0, nil)
	s.collect(7, 90, 0, 0, 0, 0, nil)
	s.collect(8, 1, 2, 0, 0, 0, "aabb")
	s.collect(9, 0, 0, 0, 0, 1.2, nil)
	s.collect(10, 2, 0, 0, 0, 1.1, nil)
	s.collect(11, 0, 0, 0, 0, 0.15, nil)
	s.collect(12, 1, 60, 120, 0, 3, nil)
	s.collect(13, 0, 0, 0, 0, 0, nil)
	s.collect(0, 2000, 0, 0, 0, 0, nil)
	s.flush()
	encoded, err := json.Marshal(s.rows)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"bearing_degrees":90,"cns_fraction":0.15,"deco":{"depth_m":3,"time_seconds":60,"tts_seconds":120,"type_code":1},"depth_m":12.3,"events":[{"flags":4,"time_seconds":3,"type_code":2,"value":5}],"gasmix_index":0,"heartbeat_bpm":80,"ppo2_bar":{"2":1.1},"rbt_seconds":600,"setpoint_bar":1.2,"tank_pressure_bar":{"1":180},"temperature_c":18,"time_seconds":1.5,"vendor_samples":[{"data_hex":"aabb","size":2,"type_code":1}]},{"bearing_degrees":null,"cns_fraction":null,"deco":null,"depth_m":null,"events":null,"gasmix_index":null,"heartbeat_bpm":null,"ppo2_bar":null,"rbt_seconds":null,"setpoint_bar":null,"tank_pressure_bar":null,"temperature_c":null,"time_seconds":2,"vendor_samples":null}]`
	if string(encoded) != want {
		t.Fatalf("sample payload mismatch:\n%s", encoded)
	}
}

func TestNativeDescriptors(t *testing.T) {
	models, err := (Driver{}).Models()
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range models["Mares"] {
		if model == "Smart Air" {
			return
		}
	}
	t.Fatalf("Mares Smart Air missing from native descriptors: %v", models["Mares"])
}
func TestSampleRowsPreserveZeroAndNulls(t *testing.T) {
	s := &samples{row: newRow()}
	s.flush()
	if len(s.rows) != 0 {
		t.Fatal("empty sample emitted")
	}
	s.row["time_seconds"] = float64(0)
	s.row["depth_m"] = float64(0)
	s.indexed("tank_pressure_bar", 0, 200)
	s.indexed("tank_pressure_bar", 1, 180)
	s.flush()
	if len(s.rows) != 1 || s.rows[0]["temperature_c"] != nil {
		t.Fatal(s.rows)
	}
	if len(s.rows[0]["tank_pressure_bar"].(map[string]float64)) != 2 {
		t.Fatal(s.rows)
	}
	s.row["time_seconds"] = float64(1)
	s.flush()
	if s.rows[1]["tank_pressure_bar"] != nil {
		t.Fatal("previous sample leaked")
	}
}
