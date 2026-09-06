// Package importer coordinates uploads. Device protocols and parsing belong to libdivecomputer.
package importer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

type Dive struct {
	Raw, Fingerprint []byte
	StartedAt        any
	Fields           map[string]any
	Samples          []map[string]any
}

func (d Dive) Record(vendor, product string) map[string]any {
	hash := fmt.Sprintf("%x", sha256.Sum256(d.Raw))
	uid := hash
	var fp any
	if len(d.Fingerprint) > 0 {
		uid = hex.EncodeToString(d.Fingerprint)
		fp = uid
	}
	return map[string]any{"vendor": vendor, "product": product, "fingerprint_hex": fp, "dive_uid": vendor + ":" + product + ":" + uid, "started_at": d.StartedAt, "duration_seconds": d.Fields["divetime_seconds"], "max_depth_m": d.Fields["max_depth_m"], "avg_depth_m": d.Fields["avg_depth_m"], "fields": d.Fields, "raw_sha256": hash, "raw_data_b64": base64.StdEncoding.EncodeToString(d.Raw), "samples": d.Samples}
}

type Store interface {
	Fingerprint(context.Context, string, string) ([]byte, error)
	SaveFingerprint(context.Context, string, string, []byte) error
	Insert(context.Context, map[string]any) (bool, error)
	Count(context.Context) *int
}
type Reader interface {
	Read(context.Context, string, string, string, []byte, func(Dive) error) error
}
type Result struct {
	Imported, Skipped int
	ExistingTotal     *int
}

func Sync(ctx context.Context, reader Reader, store Store, port, vendor, product string, progress func(Result)) (Result, error) {
	result := Result{ExistingTotal: store.Count(ctx)}
	fp, err := store.Fingerprint(ctx, vendor, product)
	if err != nil {
		return result, err
	}
	var newest []byte
	err = reader.Read(ctx, port, vendor, product, fp, func(d Dive) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		inserted, err := store.Insert(ctx, d.Record(vendor, product))
		if err != nil {
			return err
		}
		if len(newest) == 0 {
			newest = append([]byte(nil), d.Fingerprint...)
		}
		if inserted {
			result.Imported++
		} else {
			result.Skipped++
		}
		if progress != nil {
			progress(result)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	// Commit only after every dive parsed and uploaded successfully.
	if len(newest) > 0 {
		err = store.SaveFingerprint(ctx, vendor, product, newest)
	}
	return result, err
}
