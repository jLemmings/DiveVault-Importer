#ifndef DIVEVAULT_BRIDGE_H
#define DIVEVAULT_BRIDGE_H
#include <stdint.h>
#include <stdlib.h>
#include <libdivecomputer/context.h>
#include <libdivecomputer/descriptor.h>
#include <libdivecomputer/iterator.h>
#include <libdivecomputer/device.h>
#include <libdivecomputer/serial.h>
#include <libdivecomputer/parser.h>

/* Access unions through the compiler, never through guessed Go ABI layouts. */
typedef struct {
 unsigned int a, b, c, d;
 double x;
 const void *data;
} dv_sample;
dc_status_t dv_foreach(dc_device_t *device, uintptr_t handle);
dc_status_t dv_samples(dc_parser_t *parser, uintptr_t handle);
dc_status_t dv_cancel(dc_device_t *device, uintptr_t handle);
dc_status_t dv_probe(dc_device_t *device);
#endif
