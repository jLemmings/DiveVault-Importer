// Package divecomputer delegates discovery, serial I/O, protocols and parsing to libdivecomputer 0.9.0.
package divecomputer

/*
#cgo CFLAGS: -I${SRCDIR}/../../vendor/libdivecomputer-0.9.0/include
#cgo !windows LDFLAGS: -L${SRCDIR}/../../vendor/native/lib -ldivecomputer
#cgo linux LDFLAGS: -Wl,-rpath,$ORIGIN
#cgo darwin LDFLAGS: -Wl,-rpath,@executable_path/../Frameworks
#include "bridge.h"
*/
import "C"

import (
	"context"
	"encoding/hex"
	"fmt"
	"runtime/cgo"
	"sort"
	"strconv"
	"strings"
	"unsafe"

	"github.com/jLemmings/DiveVault-Importer/internal/importer"
)

type Driver struct{}

func check(rc C.dc_status_t, what string) error {
	if rc != C.DC_STATUS_SUCCESS {
		return fmt.Errorf("%s: libdivecomputer status %d", what, int(rc))
	}
	return nil
}
func newContext() (*C.dc_context_t, error) {
	if err := ensureRuntime(); err != nil {
		return nil, err
	}
	var p *C.dc_context_t
	err := check(C.dc_context_new(&p), "create context")
	return p, err
}
func descriptors(ctx *C.dc_context_t, visit func(*C.dc_descriptor_t) bool) error {
	var it *C.dc_iterator_t
	if err := check(C.dc_descriptor_iterator_new(&it, ctx), "list devices"); err != nil {
		return err
	}
	defer C.dc_iterator_free(it)
	for {
		var d *C.dc_descriptor_t
		rc := C.dc_iterator_next(it, unsafe.Pointer(&d))
		if rc == C.DC_STATUS_DONE {
			return nil
		}
		if err := check(rc, "next device"); err != nil {
			return err
		}
		keep := visit(d)
		if !keep {
			C.dc_descriptor_free(d)
		} else {
			return nil
		}
	}
}
func find(ctx *C.dc_context_t, vendor, product string) (*C.dc_descriptor_t, error) {
	var found *C.dc_descriptor_t
	err := descriptors(ctx, func(d *C.dc_descriptor_t) bool {
		if C.GoString(C.dc_descriptor_get_vendor(d)) == vendor && C.GoString(C.dc_descriptor_get_product(d)) == product {
			found = d
			return true
		}
		return false
	})
	if err == nil && found == nil {
		err = fmt.Errorf("no libdivecomputer descriptor for %s %s", vendor, product)
	}
	return found, err
}
func (Driver) Models() (map[string][]string, error) {
	ctx, err := newContext()
	if err != nil {
		return nil, err
	}
	defer C.dc_context_free(ctx)
	result := map[string][]string{}
	err = descriptors(ctx, func(d *C.dc_descriptor_t) bool {
		if C.dc_descriptor_get_transports(d)&C.DC_TRANSPORT_SERIAL == 0 {
			return false
		}
		v, p := C.GoString(C.dc_descriptor_get_vendor(d)), C.GoString(C.dc_descriptor_get_product(d))
		for _, existing := range result[v] {
			if existing == p {
				return false
			}
		}
		result[v] = append(result[v], p)
		return false
	})
	for v := range result {
		sort.Strings(result[v])
	}
	return result, err
}
func ports(ctx *C.dc_context_t, d *C.dc_descriptor_t) ([]string, error) {
	var it *C.dc_iterator_t
	if err := check(C.dc_serial_iterator_new(&it, ctx, d), "list serial ports"); err != nil {
		return nil, err
	}
	defer C.dc_iterator_free(it)
	result := []string{}
	for {
		var p *C.dc_serial_device_t
		rc := C.dc_iterator_next(it, unsafe.Pointer(&p))
		if rc == C.DC_STATUS_DONE {
			break
		}
		if err := check(rc, "next serial port"); err != nil {
			return nil, err
		}
		result = append(result, C.GoString(C.dc_serial_device_get_name(p)))
		C.dc_serial_device_free(p)
	}
	sort.Strings(result)
	return result, nil
}

type connection struct {
	ctx        *C.dc_context_t
	descriptor *C.dc_descriptor_t
	stream     *C.dc_iostream_t
	device     *C.dc_device_t
}

func (c *connection) close() {
	if c.device != nil {
		C.dc_device_close(c.device)
	}
	if c.stream != nil {
		C.dc_iostream_close(c.stream)
	}
	if c.descriptor != nil {
		C.dc_descriptor_free(c.descriptor)
	}
	if c.ctx != nil {
		C.dc_context_free(c.ctx)
	}
}
func open(port, vendor, product string) (*connection, error) {
	c := &connection{}
	var err error
	c.ctx, err = newContext()
	if err == nil {
		c.descriptor, err = find(c.ctx, vendor, product)
	}
	if err == nil {
		p := C.CString(port)
		err = check(C.dc_serial_open(&c.stream, c.ctx, p), "open serial port")
		C.free(unsafe.Pointer(p))
	}
	if err == nil {
		err = check(C.dc_device_open(&c.device, c.ctx, c.descriptor, c.stream), "open dive computer")
	}
	if err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}
func (d Driver) Scan(ctx context.Context, vendor, product string) ([]string, error) {
	native, err := newContext()
	if err != nil {
		return nil, err
	}
	defer C.dc_context_free(native)
	descriptor, err := find(native, vendor, product)
	if err != nil {
		return nil, err
	}
	defer C.dc_descriptor_free(descriptor)
	candidates, err := ports(native, descriptor)
	if err != nil {
		return nil, err
	}
	result := []string{}
	failures := []string{}
	for _, port := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c, err := open(port, vendor, product)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", port, err))
			continue
		}
		h := cgo.NewHandle(&readState{ctx: ctx})
		rc := C.dv_cancel(c.device, C.uintptr_t(h))
		if rc == C.DC_STATUS_SUCCESS {
			rc = C.dv_probe(c.device)
		}
		c.close()
		h.Delete()
		if rc == C.DC_STATUS_SUCCESS {
			result = append(result, port)
		} else {
			failures = append(failures, fmt.Sprintf("%s: probe failed with status %d", port, int(rc)))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 && len(candidates) > 0 && len(failures) > 0 {
		limit := len(failures)
		if limit > 3 {
			limit = 3
		}
		details := strings.Join(failures[:limit], "; ")
		if len(failures) > limit {
			details += fmt.Sprintf("; ... and %d more", len(failures)-limit)
		}
		return nil, fmt.Errorf("scan could not access or probe serial ports. Close other dive software, reconnect the device, and retry. Details: %s", details)
	}
	return result, nil
}

type readState struct {
	ctx    context.Context
	device *C.dc_device_t
	visit  func(importer.Dive) error
	err    error
}

func (Driver) Read(ctx context.Context, port, vendor, product string, fp []byte, visit func(importer.Dive) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := open(port, vendor, product)
	if err != nil {
		return err
	}
	state := &readState{ctx: ctx, device: c.device, visit: visit}
	h := cgo.NewHandle(state)
	defer func() { c.close(); h.Delete() }()
	if err := check(C.dv_cancel(c.device, C.uintptr_t(h)), "set cancellation"); err != nil {
		return err
	}
	if len(fp) > 0 {
		if err := check(C.dc_device_set_fingerprint(c.device, (*C.uchar)(unsafe.Pointer(&fp[0])), C.uint(len(fp))), "set fingerprint"); err != nil {
			return err
		}
	}
	rc := C.dv_foreach(c.device, C.uintptr_t(h))
	if state.err != nil {
		return state.err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return check(rc, "download dives")
}

//export goCancel
func goCancel(h C.uintptr_t) C.int {
	if cgo.Handle(h).Value().(*readState).ctx.Err() != nil {
		return 1
	}
	return 0
}

//export goDive
func goDive(h C.uintptr_t, data *C.uchar, size C.uint, fp *C.uchar, fsize C.uint) (ret C.int) {
	state := cgo.Handle(h).Value().(*readState)
	defer func() {
		if r := recover(); r != nil {
			state.err = fmt.Errorf("dive callback: %v", r)
			ret = 0
		}
	}()
	if state.err = state.ctx.Err(); state.err != nil {
		return 0
	}
	dive, err := parse(state.device, data, size, fp, fsize)
	if err == nil {
		err = state.visit(dive)
	}
	state.err = err
	if err != nil {
		return 0
	}
	return 1
}
func parse(device *C.dc_device_t, data *C.uchar, size C.uint, fp *C.uchar, fsize C.uint) (importer.Dive, error) {
	d := importer.Dive{Raw: C.GoBytes(unsafe.Pointer(data), C.int(size)), Fingerprint: C.GoBytes(unsafe.Pointer(fp), C.int(fsize))}
	var p *C.dc_parser_t
	if err := check(C.dc_parser_new(&p, device, data, C.size_t(size)), "create parser"); err != nil {
		return d, err
	}
	defer C.dc_parser_destroy(p)
	var dt C.dc_datetime_t
	if C.dc_parser_get_datetime(p, &dt) == C.DC_STATUS_SUCCESS {
		d.StartedAt = fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d", int(dt.year), int(dt.month), int(dt.day), int(dt.hour), int(dt.minute), int(dt.second))
	}
	d.Fields = fields(p)
	s := &samples{rows: []map[string]any{}, row: newRow()}
	h := cgo.NewHandle(s)
	defer h.Delete()
	if err := check(C.dv_samples(p, C.uintptr_t(h)), "parse samples"); err != nil {
		return d, err
	}
	if s.err != nil {
		return d, s.err
	}
	s.flush()
	d.Samples = s.rows
	return d, nil
}
func uintField(p *C.dc_parser_t, f C.dc_field_type_t) any {
	var v C.uint
	if C.dc_parser_get_field(p, f, 0, unsafe.Pointer(&v)) != C.DC_STATUS_SUCCESS {
		return nil
	}
	return uint(v)
}
func doubleField(p *C.dc_parser_t, f C.dc_field_type_t) any {
	var v C.double
	if C.dc_parser_get_field(p, f, 0, unsafe.Pointer(&v)) != C.DC_STATUS_SUCCESS {
		return nil
	}
	return float64(v)
}
func fields(p *C.dc_parser_t) map[string]any {
	f := map[string]any{"gasmixes": nil, "tanks": nil, "salinity": nil}
	for name, field := range map[string]C.dc_field_type_t{"divetime_seconds": C.DC_FIELD_DIVETIME, "gasmix_count": C.DC_FIELD_GASMIX_COUNT, "tank_count": C.DC_FIELD_TANK_COUNT, "dive_mode_code": C.DC_FIELD_DIVEMODE} {
		f[name] = uintField(p, field)
	}
	for name, field := range map[string]C.dc_field_type_t{"max_depth_m": C.DC_FIELD_MAXDEPTH, "avg_depth_m": C.DC_FIELD_AVGDEPTH, "atmospheric_bar": C.DC_FIELD_ATMOSPHERIC, "temperature_surface_c": C.DC_FIELD_TEMPERATURE_SURFACE, "temperature_minimum_c": C.DC_FIELD_TEMPERATURE_MINIMUM, "temperature_maximum_c": C.DC_FIELD_TEMPERATURE_MAXIMUM} {
		f[name] = doubleField(p, field)
	}
	if count, ok := f["gasmix_count"].(uint); ok {
		rows := []any{}
		for i := uint(0); i < count; i++ {
			var gas C.dc_gasmix_t
			var row any
			if C.dc_parser_get_field(p, C.DC_FIELD_GASMIX, C.uint(i), unsafe.Pointer(&gas)) == C.DC_STATUS_SUCCESS {
				row = map[string]any{"index": i, "oxygen_fraction": float64(gas.oxygen), "helium_fraction": float64(gas.helium), "nitrogen_fraction": float64(gas.nitrogen)}
			}
			rows = append(rows, row)
		}
		f["gasmixes"] = rows
	}
	var salt C.dc_salinity_t
	if C.dc_parser_get_field(p, C.DC_FIELD_SALINITY, 0, unsafe.Pointer(&salt)) == C.DC_STATUS_SUCCESS {
		f["salinity"] = map[string]any{"type_code": uint(salt._type), "density": float64(salt.density)}
	}
	if count, ok := f["tank_count"].(uint); ok {
		rows := []any{}
		for i := uint(0); i < count; i++ {
			var tank C.dc_tank_t
			var row any
			if C.dc_parser_get_field(p, C.DC_FIELD_TANK, C.uint(i), unsafe.Pointer(&tank)) == C.DC_STATUS_SUCCESS {
				row = map[string]any{"index": i, "gasmix_index": uint(tank.gasmix), "type_code": uint(tank._type), "volume": float64(tank.volume), "workpressure_bar": float64(tank.workpressure), "beginpressure_bar": float64(tank.beginpressure), "endpressure_bar": float64(tank.endpressure)}
			}
			rows = append(rows, row)
		}
		f["tanks"] = rows
	}
	return f
}

type samples struct {
	rows []map[string]any
	row  map[string]any
	err  error
}

func newRow() map[string]any {
	r := map[string]any{}
	for _, k := range []string{"time_seconds", "depth_m", "temperature_c", "tank_pressure_bar", "events", "rbt_seconds", "heartbeat_bpm", "bearing_degrees", "vendor_samples", "setpoint_bar", "ppo2_bar", "cns_fraction", "deco", "gasmix_index"} {
		r[k] = nil
	}
	return r
}
func (s *samples) flush() {
	for _, v := range s.row {
		if v != nil {
			s.rows = append(s.rows, s.row)
			break
		}
	}
	s.row = newRow()
}
func (s *samples) indexed(key string, index uint, value float64) {
	m, ok := s.row[key].(map[string]float64)
	if !ok {
		m = map[string]float64{}
		s.row[key] = m
	}
	m[strconv.FormatUint(uint64(index), 10)] = value
}
func (s *samples) append(key string, value any) {
	list, _ := s.row[key].([]any)
	s.row[key] = append(list, value)
}

//export goSample
func goSample(h C.uintptr_t, kind C.uint, v *C.dv_sample) {
	s := cgo.Handle(h).Value().(*samples)
	defer func() {
		if r := recover(); r != nil {
			s.err = fmt.Errorf("sample callback: %v", r)
		}
	}()
	a, b, c, d, x := uint(v.a), uint(v.b), uint(v.c), uint(v.d), float64(v.x)
	var data any
	if kind == C.DC_SAMPLE_VENDOR && v.data != nil && b > 0 {
		data = hex.EncodeToString(C.GoBytes(v.data, C.int(b)))
	}
	s.collect(uint(kind), a, b, c, d, x, data)
}

func (s *samples) collect(kind, a, b, c, d uint, x float64, data any) {
	switch kind {
	case C.DC_SAMPLE_TIME:
		if s.row["time_seconds"] != nil {
			s.flush()
		}
		s.row["time_seconds"] = float64(a) / 1000 // v0.9.0 reports milliseconds.
	case C.DC_SAMPLE_DEPTH:
		s.row["depth_m"] = x
	case C.DC_SAMPLE_PRESSURE:
		s.indexed("tank_pressure_bar", a, x)
	case C.DC_SAMPLE_TEMPERATURE:
		s.row["temperature_c"] = x
	case C.DC_SAMPLE_EVENT:
		s.append("events", map[string]any{"type_code": a, "time_seconds": b, "flags": c, "value": d})
	case C.DC_SAMPLE_RBT:
		s.row["rbt_seconds"] = a
	case C.DC_SAMPLE_HEARTBEAT:
		s.row["heartbeat_bpm"] = a
	case C.DC_SAMPLE_BEARING:
		s.row["bearing_degrees"] = a
	case C.DC_SAMPLE_VENDOR:
		s.append("vendor_samples", map[string]any{"type_code": a, "size": b, "data_hex": data})
	case C.DC_SAMPLE_SETPOINT:
		s.row["setpoint_bar"] = x
	case C.DC_SAMPLE_PPO2:
		s.indexed("ppo2_bar", a, x)
	case C.DC_SAMPLE_CNS:
		s.row["cns_fraction"] = x
	case C.DC_SAMPLE_DECO:
		s.row["deco"] = map[string]any{"type_code": a, "time_seconds": b, "depth_m": x, "tts_seconds": c}
	case C.DC_SAMPLE_GASMIX:
		s.row["gasmix_index"] = a
	}
}
