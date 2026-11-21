package divecomputer

import (
	"encoding/json"
	"testing"
)

func TestUnknownSampleKindsAreIgnored(t *testing.T) {
	s := &samples{row: newRow()}
	for _, kind := range []uint{14, 99, 1 << 20} {
		s.collect(kind, 1, 2, 3, 4, 5, "data")
	}
	s.flush()
	if len(s.rows) != 0 {
		t.Fatalf("unknown sample kinds produced data: %v", s.rows)
	}
}

func TestEventsAndVendorSamplesAccumulateWithinRow(t *testing.T) {
	s := &samples{row: newRow()}
	s.collect(0, 1000, 0, 0, 0, 0, nil)
	s.collect(4, 1, 0, 0, 0, 0, nil)
	s.collect(4, 2, 0, 0, 0, 0, nil)
	s.collect(8, 7, 0, 0, 0, 0, nil) // vendor sample without payload
	s.collect(0, 2000, 0, 0, 0, 0, nil)
	s.collect(4, 3, 0, 0, 0, 0, nil)
	s.flush()
	if len(s.rows) != 2 {
		t.Fatalf("rows=%d", len(s.rows))
	}
	if n := len(s.rows[0]["events"].([]any)); n != 2 {
		t.Fatalf("first row events=%d", n)
	}
	if n := len(s.rows[1]["events"].([]any)); n != 1 {
		t.Fatalf("events leaked across rows: %d", n)
	}
	vs := s.rows[0]["vendor_samples"].([]any)[0].(map[string]any)
	if vs["data_hex"] != nil || vs["size"] != uint(0) {
		t.Fatalf("vendor sample=%v", vs)
	}
}

func TestMultipleTanksAndSensors(t *testing.T) {
	s := &samples{row: newRow()}
	s.collect(0, 500, 0, 0, 0, 0, nil)
	for tank := uint(0); tank < 4; tank++ {
		s.collect(2, tank, 0, 0, 0, 200-float64(tank), nil)
	}
	s.collect(2, 1, 0, 0, 0, 150, nil) // later reading for the same tank wins
	s.flush()
	b, _ := json.Marshal(s.rows[0]["tank_pressure_bar"])
	if string(b) != `{"0":200,"1":150,"2":198,"3":197}` {
		t.Fatalf("tanks=%s", b)
	}
}

func TestTimeIsConvertedFromMilliseconds(t *testing.T) {
	s := &samples{row: newRow()}
	for _, ms := range []uint{0, 1, 999, 60000} {
		s.collect(0, ms, 0, 0, 0, 0, nil)
	}
	s.flush()
	want := []float64{0, 0.001, 0.999, 60}
	if len(s.rows) != len(want) {
		t.Fatalf("rows=%d", len(s.rows))
	}
	for i, w := range want {
		if s.rows[i]["time_seconds"] != w {
			t.Errorf("row %d time=%v want %v", i, s.rows[i]["time_seconds"], w)
		}
	}
}
