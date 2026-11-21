package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// recordingStore captures every interaction so tests can assert on ordering.
type recordingStore struct {
	fingerprint    []byte
	fingerprintErr error
	insertErr      error
	inserted       []map[string]any
	saved          [][]byte
	count          *int
}

func (s *recordingStore) Count(context.Context) *int { return s.count }
func (s *recordingStore) Fingerprint(context.Context, string, string) ([]byte, error) {
	return s.fingerprint, s.fingerprintErr
}
func (s *recordingStore) SaveFingerprint(_ context.Context, _, _ string, fp []byte) error {
	s.saved = append(s.saved, append([]byte(nil), fp...))
	return nil
}
func (s *recordingStore) Insert(_ context.Context, r map[string]any) (bool, error) {
	if s.insertErr != nil {
		return false, s.insertErr
	}
	s.inserted = append(s.inserted, r)
	return true, nil
}

type scriptedReader struct {
	dives   []Dive
	gotFP   []byte
	called  bool
	mutate  bool
	readErr error
}

func (r *scriptedReader) Read(_ context.Context, port, vendor, product string, fp []byte, visit func(Dive) error) error {
	r.called = true
	r.gotFP = fp
	for _, d := range r.dives {
		if err := visit(d); err != nil {
			return err
		}
		if r.mutate {
			// Simulate a native buffer being reused after the callback returns.
			for i := range d.Fingerprint {
				d.Fingerprint[i] = 0xEE
			}
		}
	}
	return r.readErr
}

func TestSyncFingerprintLookupFailureStopsBeforeDownload(t *testing.T) {
	s := &recordingStore{fingerprintErr: errors.New("unauthorized")}
	r := &scriptedReader{dives: []Dive{{Raw: []byte{1}, Fingerprint: []byte{1}}}}
	_, err := Sync(context.Background(), r, s, "COM1", "Mares", "Smart Air", nil)
	if !errors.Is(err, s.fingerprintErr) {
		t.Fatalf("err=%v", err)
	}
	if r.called || len(s.inserted) != 0 || len(s.saved) != 0 {
		t.Fatal("device read or upload happened after failed fingerprint lookup")
	}
}

func TestSyncNoDivesDoesNotCheckpoint(t *testing.T) {
	total := 7
	s := &recordingStore{count: &total}
	res, err := Sync(context.Background(), &scriptedReader{}, s, "COM1", "Mares", "Smart Air", nil)
	if err != nil || res.Imported != 0 || res.Skipped != 0 || res.ExistingTotal == nil || *res.ExistingTotal != 7 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(s.saved) != 0 {
		t.Fatal("checkpoint written without new dives")
	}
}

func TestSyncNilCountIsPreserved(t *testing.T) {
	res, err := Sync(context.Background(), &scriptedReader{}, &recordingStore{}, "COM1", "Mares", "Smart Air", nil)
	if err != nil || res.ExistingTotal != nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestSyncPassesStoredFingerprintAndArguments(t *testing.T) {
	s := &recordingStore{fingerprint: []byte{0xde, 0xad}}
	r := &scriptedReader{}
	if _, err := Sync(context.Background(), r, s, "COM1", "Mares", "Smart Air", nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r.gotFP, []byte{0xde, 0xad}) {
		t.Fatalf("fingerprint %x not forwarded", r.gotFP)
	}
}

func TestSyncCheckpointIsNewestDiveAndCopied(t *testing.T) {
	s := &recordingStore{}
	r := &scriptedReader{mutate: true, dives: []Dive{
		{Raw: []byte("newest"), Fingerprint: []byte{0x03, 0x03}},
		{Raw: []byte("older"), Fingerprint: []byte{0x02, 0x02}},
		{Raw: []byte("oldest"), Fingerprint: []byte{0x01, 0x01}},
	}}
	res, err := Sync(context.Background(), r, s, "COM1", "Mares", "Smart Air", nil)
	if err != nil || res.Imported != 3 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(s.saved) != 1 || !bytes.Equal(s.saved[0], []byte{0x03, 0x03}) {
		t.Fatalf("checkpoint=%x, want 0303 (newest dive, not aliased to reused buffer)", s.saved)
	}
	for i, rec := range s.inserted {
		if rec["vendor"] != "Mares" || rec["product"] != "Smart Air" {
			t.Errorf("record %d: %v", i, rec)
		}
	}
}

func TestSyncProgressReportsEveryDive(t *testing.T) {
	s := &recordingStore{}
	r := &scriptedReader{dives: []Dive{{Raw: []byte{1}}, {Raw: []byte{2}}, {Raw: []byte{3}}}}
	var seen []Result
	if _, err := Sync(context.Background(), r, s, "COM1", "Mares", "Smart Air", func(r Result) { seen = append(seen, r) }); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("progress calls=%d", len(seen))
	}
	for i, r := range seen {
		if r.Imported != i+1 || r.Skipped != 0 {
			t.Errorf("progress %d = %+v", i, r)
		}
	}
}

func TestSyncAlreadyCancelledContextUploadsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &recordingStore{}
	r := &scriptedReader{dives: []Dive{{Raw: []byte{1}, Fingerprint: []byte{1}}}}
	if _, err := Sync(ctx, r, s, "COM1", "Mares", "Smart Air", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if len(s.inserted) != 0 || len(s.saved) != 0 {
		t.Fatal("work performed after cancellation")
	}
}

func TestSyncInsertErrorStopsImmediately(t *testing.T) {
	s := &recordingStore{insertErr: errors.New("backend down")}
	r := &scriptedReader{dives: []Dive{{Raw: []byte{1}, Fingerprint: []byte{1}}, {Raw: []byte{2}, Fingerprint: []byte{2}}}}
	res, err := Sync(context.Background(), r, s, "COM1", "Mares", "Smart Air", nil)
	if !errors.Is(err, s.insertErr) || res.Imported != 0 || len(s.saved) != 0 {
		t.Fatalf("res=%+v err=%v saved=%v", res, err, s.saved)
	}
}

func TestRecordWithFingerprintAndNilFields(t *testing.T) {
	d := Dive{Raw: nil, Fingerprint: []byte{0x01, 0xff}, StartedAt: "2026-01-02T03:04:05"}
	r := d.Record("Suunto", "D4")
	if r["fingerprint_hex"] != "01ff" || r["dive_uid"] != "Suunto:D4:01ff" || r["started_at"] != "2026-01-02T03:04:05" {
		t.Fatal(r)
	}
	if r["duration_seconds"] != nil || r["max_depth_m"] != nil || r["raw_data_b64"] != "" {
		t.Fatal(r)
	}
	if r["raw_sha256"] != fmt.Sprintf("%x", sha256.Sum256(nil)) {
		t.Fatal(r["raw_sha256"])
	}
}

func FuzzRecord(f *testing.F) {
	f.Add([]byte("abc"), []byte{}, "Mares", "Smart Air")
	f.Add([]byte{0, 1, 2}, []byte{0xab}, "Suunto", "D4")
	f.Add([]byte{}, []byte{0xff, 0x00}, "", "")
	f.Fuzz(func(t *testing.T, raw, fp []byte, vendor, product string) {
		r := Dive{Raw: raw, Fingerprint: fp, Fields: map[string]any{}}.Record(vendor, product)
		if _, err := json.Marshal(r); err != nil {
			t.Fatal(err)
		}
		decoded, err := base64.StdEncoding.DecodeString(r["raw_data_b64"].(string))
		if err != nil || !bytes.Equal(decoded, raw) {
			t.Fatal("raw data does not round-trip")
		}
		if r["raw_sha256"] != fmt.Sprintf("%x", sha256.Sum256(raw)) {
			t.Fatal("raw hash mismatch")
		}
		uid := r["dive_uid"].(string)
		if !strings.HasPrefix(uid, vendor+":"+product+":") {
			t.Fatalf("uid %q", uid)
		}
		if len(fp) > 0 && (r["fingerprint_hex"] != hex.EncodeToString(fp) || !strings.HasSuffix(uid, hex.EncodeToString(fp))) {
			t.Fatal("fingerprint identity lost")
		}
		if len(fp) == 0 && r["fingerprint_hex"] != nil {
			t.Fatal("empty fingerprint serialized")
		}
	})
}
