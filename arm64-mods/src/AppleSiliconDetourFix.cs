// Lets MonoMod (and so HarmonyX / BepInEx) write detours when a Unity Mono game runs as a
// native arm64 process on Apple Silicon.
//
// Port of BepInEx/BepInEx#1402 (AppleSiliconDetourFix, written for BepInEx 5) to BepInEx 6
// be.788, shipped as a separate assembly so the BepInEx core stays byte-identical except for
// two call sites that PreloaderPatcher injects into BepInEx.Unity.Mono.Preloader.dll:
//   * UnityPreloaderRunner.PreloaderPreMain: Apply() right after PlatformUtils.SetPlatform()
//   * XTermFix.Apply: return immediately when Applies is true
//
// Why: Mono's JIT code lives in a MAP_JIT region. mprotect(RWX) on it returns EACCES, so
// MonoMod's DetourNativeMonoPlatform / MonoPosix wrappers throw inside the first
// DetourRuntimeILPlatform self-test; DetourHelper.Runtime then latches to null and every
// Harmony patch fails with NullReferenceException in DetourHelper.GetIdentifiable.
// The only way to write MAP_JIT pages is pthread_jit_write_protect_np, which cannot be called
// from managed code (it removes execute permission from the calling thread, which is running
// JIT code). UnityDoorstop >= 4.6.0 exports doorstop_jit_memcpy, which does toggle + memcpy +
// icache invalidate in one native frame. This platform routes every code write through it.
//
// It deliberately does not read MonoMod's PlatformHelper (reading PlatformHelper.Current locks
// it, and BepInEx's SetPlatform must be able to write it first); arm64 is detected via sysctl.
using System;
using System.IO;
using System.Runtime.InteropServices;
using MonoMod.RuntimeDetour;
using MonoMod.RuntimeDetour.Platforms;

namespace LomArm64
{
    public static class AppleSiliconDetourFix
    {
        private const string LibSystem = "/usr/lib/libSystem.dylib";
        private static bool? applies;

        /// <summary>Why the fix could not install, if it could not.</summary>
        public static Exception Exception { get; private set; }

        public static string Status { get; private set; } = "not run";

        /// <summary>True only for a native (not Rosetta-translated) 64-bit arm64 macOS process.</summary>
        public static bool Applies
        {
            get
            {
                if (applies == null)
                    applies = Detect();
                return applies.Value;
            }
        }

        public static void Apply()
        {
            try
            {
                if (!Applies)
                {
                    Status = "not applicable (not native arm64 macOS)";
                    return;
                }

                DetourHelper.Native = new AppleSiliconNativePlatform(new DetourNativeARMPlatform());
                Status = "installed: DetourNativeARMPlatform encoding + doorstop_jit_memcpy writes";
            }
            catch (Exception e)
            {
                Exception = e;
                Status = "FAILED: " + e.GetType().Name + ": " + e.Message;
            }
            finally
            {
                Log("[LomArm64.AppleSiliconDetourFix] " + Status);
            }
        }

        private static bool Detect()
        {
            try
            {
                if (IntPtr.Size != 8)
                    return false;
                PlatformID p = Environment.OSVersion.Platform;
                if (p != PlatformID.Unix && p != PlatformID.MacOSX)
                    return false;
                if (!Directory.Exists("/System/Library/CoreServices"))
                    return false;
                return SysctlInt("hw.optional.arm64") == 1 && SysctlInt("sysctl.proc_translated") != 1;
            }
            catch
            {
                return false;
            }
        }

        private static int SysctlInt(string name)
        {
            int value = 0;
            UIntPtr len = (UIntPtr)4;
            return sysctlbyname(name, ref value, ref len, IntPtr.Zero, UIntPtr.Zero) == 0 ? value : -1;
        }

        private static void Log(string line)
        {
            try { Console.Error.WriteLine(line); } catch { }
            try
            {
                string invoke = Environment.GetEnvironmentVariable("DOORSTOP_INVOKE_DLL_PATH");
                if (string.IsNullOrEmpty(invoke))
                    return;
                string bepinex = Path.GetDirectoryName(Path.GetDirectoryName(Path.GetFullPath(invoke)));
                File.WriteAllText(Path.Combine(bepinex, "AppleSiliconDetourFix.log"),
                    DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss") + " " + line + Environment.NewLine +
                    (Exception != null ? Exception + Environment.NewLine : ""));
            }
            catch { }
        }

        [DllImport(LibSystem)]
        private static extern int sysctlbyname(string name, ref int oldp, ref UIntPtr oldlenp, IntPtr newp, UIntPtr newlen);

        /// <summary>Replaces only the memory and code-writing operations; encoding stays with the inner platform.</summary>
        private sealed class AppleSiliconNativePlatform : IDetourNativePlatform
        {
            /// <summary>RTLD_DEFAULT on macOS: search every image already loaded into the process.</summary>
            private static readonly IntPtr RtldDefault = new IntPtr(-2);

            /// <summary>Mirrors DetourNativeARMPlatform.DetourSizes (private) for MonoMod 22.7.</summary>
            private static readonly uint[] DetourSizes = { 4 + 4, 4 + 2 + 2 + 4, 4 + 4, 4 + 4 + 4, 4 + 4 + 8 };

            [UnmanagedFunctionPointer(CallingConvention.Cdecl)]
            private delegate void JitMemCpy(IntPtr dst, IntPtr src, UIntPtr n);

            private readonly IDetourNativePlatform inner;
            private readonly JitMemCpy jitMemCpy;

            public AppleSiliconNativePlatform(IDetourNativePlatform inner)
            {
                this.inner = inner;
                IntPtr symbol = dlsym(RtldDefault, "doorstop_jit_memcpy");
                if (symbol == IntPtr.Zero)
                    throw new NotSupportedException(
                        "doorstop_jit_memcpy is not exported by any loaded image; use UnityDoorstop >= 4.6.0 " +
                        "(libdoorstop.dylib), or run the game under Rosetta.");
                jitMemCpy = (JitMemCpy)Marshal.GetDelegateForFunctionPointer(symbol, typeof(JitMemCpy));
            }

            // doorstop_jit_memcpy handles permissions and the icache around its own write. The inner
            // platform's FlushICache would execute shellcode out of a managed byte[], which is not allowed.
            public void MakeWritable(IntPtr src, uint size) { }
            public void MakeReadWriteExecutable(IntPtr src, uint size) { }
            public void MakeExecutable(IntPtr src, uint size) { }
            public void FlushICache(IntPtr src, uint size) { }

            public void Apply(NativeDetourData detour)
            {
                // AArch64 detours are position independent: encode into scratch, then copy over whole.
                byte[] scratch = new byte[detour.Size];
                GCHandle pin = GCHandle.Alloc(scratch, GCHandleType.Pinned);
                try
                {
                    NativeDetourData staging = detour;
                    staging.Method = pin.AddrOfPinnedObject();
                    inner.Apply(staging);
                    jitMemCpy(detour.Method, staging.Method, (UIntPtr)detour.Size);
                }
                finally
                {
                    pin.Free();
                }
            }

            public void Copy(IntPtr src, IntPtr dst, byte type)
            {
                if (type >= DetourSizes.Length)
                    throw new NotSupportedException("Unknown detour type " + type);
                jitMemCpy(dst, src, (UIntPtr)DetourSizes[type]);
            }

            public NativeDetourData Create(IntPtr from, IntPtr to, byte? type) { return inner.Create(from, to, type); }
            public void Free(NativeDetourData detour) { inner.Free(detour); }
            public IntPtr MemAlloc(uint size) { return inner.MemAlloc(size); }
            public void MemFree(IntPtr ptr) { inner.MemFree(ptr); }

            [DllImport(LibSystem)]
            private static extern IntPtr dlsym(IntPtr handle, string symbol);
        }
    }
}
