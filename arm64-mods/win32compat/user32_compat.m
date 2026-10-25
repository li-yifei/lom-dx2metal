// Minimal macOS stand-in for the three user32.dll exports ZhaoHuoSystem 0.3.42 P/Invokes.
// Installed as plugins/ZhaoHuoSystem/libuser32.dylib, i.e. next to the plugin assembly, so only
// assemblies in that directory resolve "user32.dll" to it (Mono probes the assembly's directory).
//
//   short  GetAsyncKeyState(int vKey)
//   HWND   GetForegroundWindow(void)
//   DWORD  GetWindowThreadProcessId(HWND hWnd, DWORD *lpdwProcessId)
//   LRESULT SendMessageTimeout(...)   (LOM_JP_FontPatch broadcasts WM_FONTCHANGE; no-op success here)
//
// The plugin ORs three hotkey sources: legacy Input.GetKeyDown, Input System
// Keyboard.current[key].wasPressedThisFrame, and a GetAsyncKeyState edge gated by
// GetForegroundWindow == own process. On native macOS the Input System keyboard exists, so by
// default GetAsyncKeyState reports "not pressed" to avoid a second, possibly off-by-one-frame
// edge for the same key press. Set LOM_USER32_KEYSTATE=1 to poll real key state through
// CGEventSourceKeyState (only when Input Monitoring access is already granted; never prompts).
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#define EXPORT __attribute__((visibility("default")))

static int key_state_mode = -1;  // -1 unknown, 0 off, 1 on

static int keystate_enabled(void) {
    if (key_state_mode < 0) {
        const char *v = getenv("LOM_USER32_KEYSTATE");
        key_state_mode = (v && strcmp(v, "1") == 0) ? 1 : 0;
        if (key_state_mode == 1 && __builtin_available(macOS 10.15, *)) {
            if (!CGPreflightListenEventAccess()) key_state_mode = 0;
        }
    }
    return key_state_mode;
}

// Windows virtual-key code -> macOS kVK_* code (Carbon Events.h, ANSI layout). -1 = unmapped.
static int vk_to_mac(int vk) {
    static const int letters[26] = {0x00,0x0B,0x08,0x02,0x0E,0x03,0x05,0x04,0x22,0x26,0x28,0x25,0x2E,
                                    0x2D,0x1F,0x23,0x0C,0x0F,0x01,0x11,0x20,0x09,0x0D,0x07,0x10,0x06};
    static const int digits[10] = {0x1D,0x12,0x13,0x14,0x15,0x17,0x16,0x1A,0x1C,0x19};
    static const int fkeys[20] = {0x7A,0x78,0x63,0x76,0x60,0x61,0x62,0x64,0x65,0x6D,0x67,0x6F,
                                  0x69,0x6B,0x71,0x6A,0x40,0x4F,0x50,0x5A};
    static const int numpad[10] = {0x52,0x53,0x54,0x55,0x56,0x57,0x58,0x59,0x5B,0x5C};
    if (vk >= 0x41 && vk <= 0x5A) return letters[vk - 0x41];
    if (vk >= 0x30 && vk <= 0x39) return digits[vk - 0x30];
    if (vk >= 0x70 && vk <= 0x83) return fkeys[vk - 0x70];
    if (vk >= 0x60 && vk <= 0x69) return numpad[vk - 0x60];
    switch (vk) {
        case 0x08: return 0x33; case 0x09: return 0x30; case 0x0D: return 0x24; case 0x1B: return 0x35;
        case 0x20: return 0x31; case 0x21: return 0x74; case 0x22: return 0x79; case 0x23: return 0x77;
        case 0x24: return 0x73; case 0x25: return 0x7B; case 0x26: return 0x7E; case 0x27: return 0x7C;
        case 0x28: return 0x7D; case 0x2D: return 0x72; case 0x2E: return 0x75;
        case 0x10: case 0xA0: return 0x38; case 0xA1: return 0x3C;
        case 0x11: case 0xA2: return 0x3B; case 0xA3: return 0x3E;
        case 0x12: case 0xA4: return 0x3A; case 0xA5: return 0x3D;
        case 0x14: return 0x39;
        case 0x6A: return 0x43; case 0x6B: return 0x45; case 0x6D: return 0x4E; case 0x6E: return 0x41; case 0x6F: return 0x4B;
        case 0xBA: return 0x29; case 0xBB: return 0x18; case 0xBC: return 0x2B; case 0xBD: return 0x1B;
        case 0xBE: return 0x2F; case 0xBF: return 0x2C; case 0xC0: return 0x32; case 0xDB: return 0x21;
        case 0xDC: return 0x2A; case 0xDD: return 0x1E; case 0xDE: return 0x27;
        default: return -1;
    }
}

EXPORT int16_t GetAsyncKeyState(int32_t vk) {
    if (!keystate_enabled()) return 0;
    int mac = vk_to_mac(vk);
    if (mac < 0) return 0;
    return CGEventSourceKeyState(kCGEventSourceStateCombinedSessionState, (CGKeyCode)mac) ? (int16_t)0x8000 : 0;
}

// The "window handle" is the frontmost application's pid (never 0 while some app is frontmost).
EXPORT void *GetForegroundWindow(void) {
    pid_t pid = 0;
    @autoreleasepool {
        NSRunningApplication *app = [[NSWorkspace sharedWorkspace] frontmostApplication];
        if (app) pid = app.processIdentifier;
    }
    return (void *)(intptr_t)pid;
}

EXPORT uint32_t GetWindowThreadProcessId(void *hwnd, uint32_t *pid) {
    if (pid) *pid = (uint32_t)(intptr_t)hwnd;
    return hwnd ? 1u : 0u;
}

// No window messages on macOS. Report success with result 0 so callers treat the broadcast as sent.
EXPORT intptr_t SendMessageTimeout(void *hwnd, uint32_t msg, intptr_t wparam, intptr_t lparam,
                                   uint32_t flags, uint32_t timeout, intptr_t *result) {
    (void)hwnd; (void)msg; (void)wparam; (void)lparam; (void)flags; (void)timeout;
    if (result) *result = 0;
    return 1;
}
EXPORT intptr_t SendMessageTimeoutW(void *hwnd, uint32_t msg, intptr_t wparam, intptr_t lparam,
                                    uint32_t flags, uint32_t timeout, intptr_t *result) {
    return SendMessageTimeout(hwnd, msg, wparam, lparam, flags, timeout, result);
}
EXPORT intptr_t SendMessageTimeoutA(void *hwnd, uint32_t msg, intptr_t wparam, intptr_t lparam,
                                    uint32_t flags, uint32_t timeout, intptr_t *result) {
    return SendMessageTimeout(hwnd, msg, wparam, lparam, flags, timeout, result);
}
