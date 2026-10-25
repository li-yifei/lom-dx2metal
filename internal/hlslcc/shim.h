// lom-dx2metal 对 HLSLcc 的 C 垫片：把 C++ 接口 TranslateHLSLFromMem 包成 cgo 能调用的 C 函数。
#ifndef DX2METAL_HLSLCC_SHIM_H
#define DX2METAL_HLSLCC_SHIM_H
#include <stddef.h>
#ifdef __cplusplus
extern "C" {
#endif

// 把完整 DXBC 容器（含合成的 RDEF）翻译成 Metal 源码。
// 成功返回 1，*out 指向 malloc 分配、以 NUL 结尾的源码（调用方 free）；翻译失败返回 0；
// HLSLcc 崩溃（SIGSEGV/SIGBUS）返回负的信号号。调用方必须串行调用。
int dx2metal_hlslcc_to_metal(const char *dxbc, size_t len, unsigned int flags, char **out, size_t *out_len);

#ifdef __cplusplus
}
#endif
#endif
