package importer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

type fakeStore struct {
	failAt, calls, saves int
	fingerprint          []byte
	checkpointErr        error
}

func (s *fakeStore) Count(context.Context) *int { v := 12; return &v }
func (s *fakeStore) Fingerprint(context.Context, string, string) ([]byte, error) {
	return []byte{1}, nil
}
func (s *fakeStore) SaveFingerprint(_ context.Context, _, _ string, fp []byte) error {
	s.saves++
	s.fingerprint = fp
	return s.checkpointErr
}
func (s *fakeStore) Insert(context.Context, map[string]any) (bool, error) {
	s.calls++
	if s.calls == s.failAt {
		return false, errors.New("upload failed")
	}
	return s.calls != 2, nil
}

type fakeReader struct{ failure error }

func (r fakeReader) Read(_ context.Context, _, _, _ string, fp []byte, visit func(Dive) error) error {
	if !reflect.DeepEqual(fp, []byte{1}) {
		return errors.New("fingerprint not passed to device")
	}
	for _, fp := range []byte{9, 8, 7} {
		if err := visit(Dive{Raw: []byte{fp}, Fingerprint: []byte{fp}, Fields: map[string]any{}}); err != nil {
			return err
		}
	}
	return r.failure
}
func TestCheckpointOnlyAfterFullSuccess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fail      int
		readErr   error
		wantErr   bool
		wantSaves int
	}{{"success", 0, nil, false, 1}, {"upload failure", 2, nil, true, 0}, {"read failure", 0, errors.New("parse failed"), true, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeStore{failAt: tc.fail}
			r, err := Sync(context.Background(), fakeReader{tc.readErr}, s, "COM1", "Mares", "Smart Air", nil)
			if (err != nil) != tc.wantErr || s.saves != tc.wantSaves {
				t.Fatalf("err=%v saves=%d", err, s.saves)
			}
			if !tc.wantErr && (r.Imported != 2 || r.Skipped != 1 || !reflect.DeepEqual(s.fingerprint, []byte{9})) {
				t.Fatalf("result=%+v checkpoint=%x", r, s.fingerprint)
			}
		})
	}
}
func TestCancellationDoesNotCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &fakeStore{}
	_, err := Sync(ctx, fakeReader{}, s, "COM1", "Mares", "Smart Air", func(Result) { cancel() })
	if !errors.Is(err, context.Canceled) || s.saves != 0 {
		t.Fatalf("err=%v saves=%d", err, s.saves)
	}
}
func TestCheckpointFailurePropagates(t *testing.T) {
	s := &fakeStore{checkpointErr: errors.New("checkpoint failed")}
	_, err := Sync(context.Background(), fakeReader{}, s, "COM1", "Mares", "Smart Air", nil)
	if !errors.Is(err, s.checkpointErr) {
		t.Fatal(err)
	}
}
func TestRecordContract(t *testing.T) {
	d := Dive{Raw: []byte("abc"), Fields: map[string]any{"divetime_seconds": uint(60), "max_depth_m": 12.5, "avg_depth_m": 7.0}, Samples: []map[string]any{}}
	r := d.Record("Mares", "Smart Air")
	if r["raw_sha256"] != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" || r["raw_data_b64"] != "YWJj" || r["fingerprint_hex"] != nil {
		t.Fatal(r)
	}
	if r["dive_uid"] != "Mares:Smart Air:"+r["raw_sha256"].(string) {
		t.Fatal(r)
	}
	if len(r) != 12 {
		t.Fatalf("unexpected payload keys: %v", r)
	}
	if _, err := json.Marshal(r); err != nil {
		t.Fatal(err)
	}
	d.Fingerprint = []byte{0xab, 0xcd}
	if d.Record("Mares", "Smart Air")["dive_uid"] != "Mares:Smart Air:abcd" {
		t.Fatal("fingerprint identity lost")
	}
}
