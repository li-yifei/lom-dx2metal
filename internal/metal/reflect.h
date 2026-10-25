// lom-dx2metal 的 Metal 垫片：编译 MSL、建渲染管线并取反射（移植 vendor/metal-tools/reflect_batch.m）。
#ifndef DX2METAL_REFLECT_H
#define DX2METAL_REFLECT_H
#include <stddef.h>

// 返回系统默认 MTLDevice（已 retain）；没有 Metal 设备时返回 NULL。
void *dx2metal_device_new(void);
void dx2metal_release(void *obj);
// 以 MSL 2.1 编译源码，返回 MTLLibrary（已 retain）；失败返回 NULL，*err 为 malloc 的错误信息。
void *dx2metal_library_new(void *device, const char *source, char **err);
// 把两份库的 xlatMtlMain 链接成渲染管线并取反射，返回 malloc 的 JSON（与 reflect_batch 每行输出相同）。
char *dx2metal_reflect_pair(void *device, void *vertex_library, void *fragment_library);

#endif
