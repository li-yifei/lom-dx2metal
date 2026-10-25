// Injects LomArm64.AppleSiliconDetourFix into BepInEx 6 (be.788) BepInEx.Unity.Mono.Preloader.dll:
//   1. UnityPreloaderRunner.PreloaderPreMain: call AppleSiliconDetourFix.Apply() right after
//      PlatformUtils.SetPlatform() (same place as BepInEx/BepInEx#1402 for v5).
//   2. XTermFix.Apply: `if (AppleSiliconDetourFix.Applies) return;` at the top, because XTermFix
//      forces DetourNativeX86Platform on every non-Windows target, which on Apple Silicon would
//      write x86 branches into arm64 code and undo the fix.
// Idempotent: a preloader that already references the fix assembly is left untouched.
// Usage: PreloaderPatcher <BepInEx.Unity.Mono.Preloader.dll> <LomArm64.AppleSiliconDetourFix.dll>
//        PreloaderPatcher --backslash-literals <Some.dll>
//   Second form: rewrites every string literal (ldstr) that contains '\\' to use '/' instead.
//   For mods that hardcode Windows relative paths such as "BepInEx\\cache\\..." (LOM_JP_LegacyCleanup);
//   '/' works on Windows too. Char constants are left alone. Idempotent (nothing left to rewrite).
using System;
using System.IO;
using System.Linq;
using Mono.Cecil;
using Mono.Cecil.Cil;

internal static class Program
{
    private static int BackslashLiterals(string target)
    {
        var resolver = new DefaultAssemblyResolver();
        resolver.AddSearchDirectory(Path.GetDirectoryName(target));
        using var input = new MemoryStream(File.ReadAllBytes(target));
        using var asm = AssemblyDefinition.ReadAssembly(input, new ReaderParameters { AssemblyResolver = resolver });
        int n = 0;
        foreach (TypeDefinition t in asm.MainModule.GetTypes())
            foreach (MethodDefinition m in t.Methods.Where(m => m.HasBody))
                foreach (Instruction i in m.Body.Instructions)
                    if (i.OpCode == OpCodes.Ldstr && i.Operand is string s && s.IndexOf('\\') >= 0)
                    {
                        Console.WriteLine($"  {t.Name}::{m.Name}: \"{s}\"");
                        i.Operand = s.Replace('\\', '/');
                        n++;
                    }
        if (n == 0) { Console.WriteLine("no backslash literals: " + target); return 0; }
        asm.Write(target);
        Console.WriteLine($"rewrote {n} literal(s): {target}");
        return 0;
    }

    private const string FixAssembly = "LomArm64.AppleSiliconDetourFix";

    private static int Main(string[] args)
    {
        if (args.Length == 2 && args[0] == "--backslash-literals")
            return BackslashLiterals(Path.GetFullPath(args[1]));

        if (args.Length != 2)
        {
            Console.Error.WriteLine("usage: PreloaderPatcher <BepInEx.Unity.Mono.Preloader.dll> <LomArm64.AppleSiliconDetourFix.dll>");
            return 2;
        }

        string target = Path.GetFullPath(args[0]);
        string fixPath = Path.GetFullPath(args[1]);
        var resolver = new DefaultAssemblyResolver();
        resolver.AddSearchDirectory(Path.GetDirectoryName(target));
        resolver.AddSearchDirectory(Path.GetDirectoryName(fixPath));

        using var input = new MemoryStream(File.ReadAllBytes(target));
        using var asm = AssemblyDefinition.ReadAssembly(input, new ReaderParameters { AssemblyResolver = resolver });
        ModuleDefinition mod = asm.MainModule;

        if (mod.AssemblyReferences.Any(r => r.Name == FixAssembly))
        {
            Console.WriteLine("already patched: " + target);
            return 0;
        }

        using var fixAsm = AssemblyDefinition.ReadAssembly(fixPath, new ReaderParameters { AssemblyResolver = resolver });
        TypeDefinition fixType = fixAsm.MainModule.GetType("LomArm64.AppleSiliconDetourFix")
            ?? throw new InvalidOperationException("LomArm64.AppleSiliconDetourFix not found in " + fixPath);
        MethodReference apply = mod.ImportReference(fixType.Methods.Single(m => m.Name == "Apply" && m.Parameters.Count == 0));
        MethodReference applies = mod.ImportReference(fixType.Methods.Single(m => m.Name == "get_Applies"));

        // 1. PreloaderPreMain
        TypeDefinition runner = mod.GetType("BepInEx.Unity.Mono.Preloader.UnityPreloaderRunner")
            ?? throw new InvalidOperationException("UnityPreloaderRunner not found (not a BepInEx 6 Unity.Mono preloader?)");
        MethodDefinition preMain = runner.Methods.Single(m => m.Name == "PreloaderPreMain");
        Instruction setPlatform = preMain.Body.Instructions.Single(i =>
            i.OpCode == OpCodes.Call && i.Operand is MethodReference mr && mr.Name == "SetPlatform");
        ILProcessor il = preMain.Body.GetILProcessor();
        il.InsertAfter(setPlatform, il.Create(OpCodes.Call, apply));

        // 2. XTermFix.Apply
        TypeDefinition xterm = mod.GetType("BepInEx.Unity.Mono.Preloader.RuntimeFixes.XTermFix")
            ?? throw new InvalidOperationException("XTermFix not found");
        MethodDefinition xApply = xterm.Methods.Single(m => m.Name == "Apply" && m.Parameters.Count == 0);
        ILProcessor xil = xApply.Body.GetILProcessor();
        Instruction first = xApply.Body.Instructions[0];
        xil.InsertBefore(first, xil.Create(OpCodes.Call, applies));
        xil.InsertBefore(first, xil.Create(OpCodes.Brfalse, first));
        xil.InsertBefore(first, xil.Create(OpCodes.Ret));

        asm.Write(target);
        Console.WriteLine("patched: " + target);
        return 0;
    }
}
