#include "bridge.h"
#include "_cgo_export.h"

static int dive_callback(const unsigned char *data, unsigned int size,
 const unsigned char *fp, unsigned int fsize, void *userdata) {
 return goDive((uintptr_t)userdata, (unsigned char *)data, size, (unsigned char *)fp, fsize);
}
static int cancel_callback(void *userdata) { return goCancel((uintptr_t)userdata); }
static int probe_callback(const unsigned char *data, unsigned int size,
 const unsigned char *fp, unsigned int fsize, void *userdata) { return 0; }
static void sample_callback(dc_sample_type_t type, const dc_sample_value_t *v, void *userdata) {
 dv_sample s = {0};
 switch(type) {
 case DC_SAMPLE_TIME: s.a=v->time; break;
 case DC_SAMPLE_DEPTH: s.x=v->depth; break;
 case DC_SAMPLE_PRESSURE: s.a=v->pressure.tank; s.x=v->pressure.value; break;
 case DC_SAMPLE_TEMPERATURE: s.x=v->temperature; break;
 case DC_SAMPLE_EVENT: s.a=v->event.type; s.b=v->event.time; s.c=v->event.flags; s.d=v->event.value; break;
 case DC_SAMPLE_RBT: s.a=v->rbt; break;
 case DC_SAMPLE_HEARTBEAT: s.a=v->heartbeat; break;
 case DC_SAMPLE_BEARING: s.a=v->bearing; break;
 case DC_SAMPLE_VENDOR: s.a=v->vendor.type; s.b=v->vendor.size; s.data=v->vendor.data; break;
 case DC_SAMPLE_SETPOINT: s.x=v->setpoint; break;
 case DC_SAMPLE_PPO2: s.a=v->ppo2.sensor; s.x=v->ppo2.value; break;
 case DC_SAMPLE_CNS: s.x=v->cns; break;
 case DC_SAMPLE_DECO: s.a=v->deco.type; s.b=v->deco.time; s.x=v->deco.depth; s.c=v->deco.tts; break;
 case DC_SAMPLE_GASMIX: s.a=v->gasmix; break;
 }
 goSample((uintptr_t)userdata, (unsigned int)type, &s);
}
dc_status_t dv_foreach(dc_device_t *d, uintptr_t h) { return dc_device_foreach(d,dive_callback,(void *)h); }
dc_status_t dv_samples(dc_parser_t *p, uintptr_t h) { return dc_parser_samples_foreach(p,sample_callback,(void *)h); }
dc_status_t dv_cancel(dc_device_t *d, uintptr_t h) { return dc_device_set_cancel(d,cancel_callback,(void *)h); }
dc_status_t dv_probe(dc_device_t *d) { return dc_device_foreach(d,probe_callback,NULL); }
