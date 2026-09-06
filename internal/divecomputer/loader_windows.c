#include <windows.h>
#include "bridge.h"

/* Typed trampolines keep the official header declarations as the ABI contract.
 * No libdivecomputer DLL appears in the executable's static import table. */
#define DV_API(result, name, params, args) \
 static result (*p_##name) params; \
 result name params { return p_##name args; }
#include "windows_api.inc"
#undef DV_API

unsigned long dv_load(const wchar_t *path, unsigned long flags) {
 HMODULE library = LoadLibraryExW(path, NULL, flags);
 if (!library) return GetLastError();
 /* Resolve everything before publishing a usable runtime. Go serializes calls. */
 #define DV_API(result, name, params, args) \
   p_##name = (result (*) params) GetProcAddress(library, #name); \
   if (!p_##name) { FreeLibrary(library); return ERROR_PROC_NOT_FOUND; }
 #include "windows_api.inc"
 #undef DV_API
 /* Keep the module loaded for process lifetime: native callbacks may be active. */
 return ERROR_SUCCESS;
}
