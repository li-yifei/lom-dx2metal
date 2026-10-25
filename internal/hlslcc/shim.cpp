// HLSLcc C 垫片实现，调用方式与 third_party/HLSLcc/hlslcc_cli.cxx 相同（同样的扩展、依赖数据与精度信息）。
//
// 崩溃隔离：Python 版在子进程里跑 hlslcc-cli，崩溃（段错误）只算这个变体失败；进程内调用时一次崩溃会拖垮
// 整个进程（包括 GUI）。所以翻译期间临时接管 SIGSEGV/SIGBUS：发生在本线程的故障用 siglongjmp 跳回、记为失败；
// 其他线程的信号（例如 Go 运行时自己的空指针检查）原样转交给之前的处理函数（Go 的）。
// 调用方（hlslcc.go）用互斥锁保证同一时刻只有一个翻译在跑。跳出时 HLSLcc 的 C++ 对象不会析构，泄漏一点内存，可以接受。
#include "shim.h"
#include "hlslcc.h"
#include <csetjmp>
#include <csignal>
#include <cstdlib>
#include <cstring>
#include <pthread.h>
#include <vector>

static sigjmp_buf g_jump;
static pthread_t g_thread;
static volatile sig_atomic_t g_active = 0;
static struct sigaction g_old_segv, g_old_bus;

static void chain(int sig, siginfo_t *info, void *ctx)
{
    struct sigaction *old = sig == SIGSEGV ? &g_old_segv : &g_old_bus;
    if (old->sa_flags & SA_SIGINFO) {
        if (old->sa_sigaction) {
            old->sa_sigaction(sig, info, ctx);
            return;
        }
    } else if (old->sa_handler != SIG_DFL && old->sa_handler != SIG_IGN) {
        old->sa_handler(sig);
        return;
    }
    // 没有可转交的处理函数：恢复默认行为让进程照常崩溃。
    signal(sig, SIG_DFL);
    raise(sig);
}

static void on_fault(int sig, siginfo_t *info, void *ctx)
{
    if (g_active && pthread_equal(pthread_self(), g_thread)) {
        g_active = 0;
        siglongjmp(g_jump, sig);
    }
    chain(sig, info, ctx);
}

static int translate(const char *dxbc, unsigned int flags, char **out, size_t *out_len)
{
    GlExtensions ext = {0};
    GLSLCrossDependencyData deps;
    HLSLccSamplerPrecisionInfo prec;
    HLSLccReflection refl;
    GLSLShader shader;
    int ok = TranslateHLSLFromMem(dxbc, flags, LANG_METAL, &ext, &deps, prec, refl, &shader);
    if (!ok)
        return 0;
    size_t n = shader.sourceCode.size();
    char *s = (char *)malloc(n + 1);
    if (!s)
        return 0;
    memcpy(s, shader.sourceCode.c_str(), n + 1);
    *out = s;
    *out_len = n;
    return 1;
}

int dx2metal_hlslcc_to_metal(const char *dxbc, size_t len, unsigned int flags, char **out, size_t *out_len)
{
    *out = nullptr;
    *out_len = 0;
    // TranslateHLSLFromMem 按容器头里的长度读取；复制一份保证缓冲区在 C 侧有效。
    char *buf = (char *)malloc(len);
    if (!buf)
        return 0;
    memcpy(buf, dxbc, len);

    struct sigaction sa;
    memset(&sa, 0, sizeof sa);
    sa.sa_sigaction = on_fault;
    sa.sa_flags = SA_SIGINFO | SA_ONSTACK;
    sigemptyset(&sa.sa_mask);
    sigaction(SIGSEGV, &sa, &g_old_segv);
    sigaction(SIGBUS, &sa, &g_old_bus);
    g_thread = pthread_self();

    int result;
    int sig = sigsetjmp(g_jump, 1);
    if (sig == 0) {
        g_active = 1;
        result = translate(buf, flags, out, out_len);
        g_active = 0;
    } else {
        result = -sig; // 崩溃：返回负的信号号
    }
    sigaction(SIGSEGV, &g_old_segv, nullptr);
    sigaction(SIGBUS, &g_old_bus, nullptr);
    free(buf);
    return result;
}
