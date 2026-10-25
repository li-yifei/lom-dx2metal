// Minimal macOS stand-in for the gdi32.dll font-registration exports used by LOM_JP_Mod
// (LOM_JP_FontPatch / LOM_JP_Updater / LOM_JP_UpdateEngine / LOM_JP_LegacyCleanup):
//
//   int  AddFontResourceEx(LPCWSTR name, DWORD fl, PVOID res)      [DllImport CharSet.Unicode]
//   BOOL RemoveFontResourceEx(LPCWSTR name, DWORD fl, PVOID res)
//
// Windows: makes the fonts in a .ttf/.otf file visible to font enumeration (FR_PRIVATE = this
// process only), so Font.CreateDynamicFontFromOSFont("Some Family") finds them.
// macOS equivalent: CTFontManagerRegisterFontsForURL(url, kCTFontManagerScopeProcess). Return value
// mirrors Windows: number of fonts added (0 on failure). "Already registered" counts as success.
// Every call is logged to stderr as "[gdi32-compat] ..." (lands in the launcher's console.log).
#import <CoreText/CoreText.h>
#import <Foundation/Foundation.h>
#include <stdint.h>

#define EXPORT __attribute__((visibility("default")))

static CFURLRef url_from_wide(const uint16_t *name) {
    if (!name) return NULL;
    size_t n = 0;
    while (name[n]) n++;
    CFStringRef s = CFStringCreateWithCharacters(NULL, (const UniChar *)name, (CFIndex)n);
    if (!s) return NULL;
    CFURLRef url = CFURLCreateWithFileSystemPath(NULL, s, kCFURLPOSIXPathStyle, false);
    CFRelease(s);
    return url;
}

static int font_count(CFURLRef url) {
    CFArrayRef descs = CTFontManagerCreateFontDescriptorsFromURL(url);
    if (!descs) return 0;
    int n = (int)CFArrayGetCount(descs);
    for (int i = 0; i < n; i++) {
        CTFontDescriptorRef d = (CTFontDescriptorRef)CFArrayGetValueAtIndex(descs, i);
        CFStringRef fam = CTFontDescriptorCopyAttribute(d, kCTFontFamilyNameAttribute);
        if (fam) {
            NSLog(@"[gdi32-compat]   family: %@", (__bridge NSString *)fam);
            CFRelease(fam);
        }
    }
    CFRelease(descs);
    return n;
}

EXPORT int32_t AddFontResourceEx(const uint16_t *name, uint32_t fl, void *res) {
    (void)fl; (void)res;
    CFURLRef url = url_from_wide(name);
    if (!url) return 0;
    CFErrorRef err = NULL;
    bool ok = CTFontManagerRegisterFontsForURL(url, kCTFontManagerScopeProcess, &err);
    int added = 0;
    if (!ok && err && CFErrorGetCode(err) == kCTFontManagerErrorAlreadyRegistered) ok = true;
    if (ok) added = font_count(url);
    NSLog(@"[gdi32-compat] AddFontResourceEx %@ -> %s (%d font(s))%@",
          ((__bridge NSURL *)url).path, ok ? "registered" : "FAILED", added,
          err ? [NSString stringWithFormat:@" err=%ld", (long)CFErrorGetCode(err)] : @"");
    if (err) CFRelease(err);
    CFRelease(url);
    return ok ? (added > 0 ? added : 1) : 0;
}

EXPORT int32_t AddFontResourceExW(const uint16_t *name, uint32_t fl, void *res) {
    return AddFontResourceEx(name, fl, res);
}

EXPORT int32_t RemoveFontResourceEx(const uint16_t *name, uint32_t fl, void *res) {
    (void)fl; (void)res;
    CFURLRef url = url_from_wide(name);
    if (!url) return 0;
    CFErrorRef err = NULL;
    bool ok = CTFontManagerUnregisterFontsForURL(url, kCTFontManagerScopeProcess, &err);
    NSLog(@"[gdi32-compat] RemoveFontResourceEx %@ -> %s", ((__bridge NSURL *)url).path, ok ? "ok" : "not registered");
    if (err) CFRelease(err);
    CFRelease(url);
    return ok ? 1 : 0;
}

EXPORT int32_t RemoveFontResourceExW(const uint16_t *name, uint32_t fl, void *res) {
    return RemoveFontResourceEx(name, fl, res);
}
