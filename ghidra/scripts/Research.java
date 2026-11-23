// Export selected functions, references and direct call targets for protocol research.
// @category Quetzal
import ghidra.app.decompiler.DecompInterface;
import ghidra.app.cmd.disassemble.DisassembleCommand;
import ghidra.app.script.GhidraScript;
import ghidra.program.model.listing.Data;
import ghidra.program.model.listing.Function;
import ghidra.program.model.symbol.Reference;
import java.io.PrintWriter;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.LinkedHashSet;
import java.util.Set;

public class Research extends GhidraScript {
    private final Set<Function> functions = new LinkedHashSet<>();

    private void refs(long address, PrintWriter out) {
        for (Reference ref : getReferencesTo(toAddr(address))) {
            Function fn = getFunctionContaining(ref.getFromAddress());
            out.println("REF " + toAddr(address) + " from " + ref.getFromAddress() + " " + fn);
            if (fn != null) functions.add(fn);
        }
    }

    public void run() throws Exception {
        String[] args = getScriptArgs();
        if (args.length < 2) throw new IllegalArgumentException("Expected OUTPUT SELECTOR ...");
        Path output = Path.of(args[0]).toAbsolutePath();
        Files.createDirectories(output.getParent());
        try (PrintWriter out = new PrintWriter(Files.newBufferedWriter(output, StandardCharsets.UTF_8))) {
            out.println("PROGRAM " + currentProgram.getName());
            out.println("LANGUAGE " + currentProgram.getLanguageID());
            out.println("SHA256 " + currentProgram.getExecutableSHA256());
            for (int i = 1; i < args.length; i++) {
                String arg = args[i];
                out.println("SELECTOR " + arg);
                int before = functions.size();
                if (arg.startsWith("refs:")) {
                    String needle = arg.substring(5);
                    for (Data data : currentProgram.getListing().getDefinedData(true)) {
                        if (data.hasStringValue() && String.valueOf(data.getValue()).contains(needle)) {
                            out.println("STRING " + data.getAddress() + " " + data.getValue());
                            refs(data.getAddress().getOffset(), out);
                        }
                    }
                } else if (arg.startsWith("addrrefs:") || arg.startsWith("callers:")) {
                    refs(Long.decode(arg.substring(arg.indexOf(':') + 1)), out);
                } else if (arg.startsWith("code:")) {
                    var address = toAddr(Long.decode(arg.substring(5)));
                    Function fn = getFunctionAt(address);
                    if (fn == null) {
                        new DisassembleCommand(address, null, true).applyTo(currentProgram, monitor);
                        fn = createFunction(address, null);
                    }
                    if (fn != null) functions.add(fn);
                } else if (arg.startsWith("name:")) {
                    for (Function fn : currentProgram.getFunctionManager().getFunctions(true)) {
                        if (fn.getName(true).contains(arg.substring(5))) functions.add(fn);
                    }
                } else if (arg.startsWith("range:")) {
                    String[] bounds = arg.substring(6).split(":");
                    if (bounds.length != 2) throw new IllegalArgumentException("Expected range:START:END");
                    long start = Long.decode(bounds[0]);
                    long end = Long.decode(bounds[1]);
                    for (Function fn : currentProgram.getFunctionManager().getFunctions(true)) {
                        long entry = fn.getEntryPoint().getOffset();
                        if (entry >= start && entry < end && !fn.getEntryPoint().getAddressSpace().isOverlaySpace()) functions.add(fn);
                    }
                } else if (arg.startsWith("asm:")) {
                    var address = toAddr(Long.decode(arg.substring(4)));
                    Function fn = getFunctionContaining(address);
                    if (fn != null) {
                        functions.add(fn);
                        for (var instruction : currentProgram.getListing().getInstructions(fn.getBody(), true)) {
                            out.println("ASM " + instruction.getAddress() + " " + instruction);
                        }
                    }
                } else {
                    Function fn = getFunctionContaining(toAddr(Long.decode(arg)));
                    if (fn != null) functions.add(fn);
                }
                out.println("NEW_FUNCTIONS " + (functions.size() - before));
                out.flush();
            }
            DecompInterface decompiler = new DecompInterface();
            try {
                if (!decompiler.openProgram(currentProgram)) throw new IllegalStateException(decompiler.getLastMessage());
                for (Function fn : functions) {
                    monitor.checkCancelled();
                    out.println("\n==== " + fn.getEntryPoint() + " " + fn.getName(true));
                    for (Reference ref : getReferencesTo(fn.getEntryPoint())) {
                        out.println("CALLER " + ref.getFromAddress() + " " + getFunctionContaining(ref.getFromAddress()) + " " + ref.getReferenceType());
                    }
                    for (Function callee : fn.getCalledFunctions(monitor)) {
                        out.println("CALL " + callee.getEntryPoint() + " " + callee.getName(true));
                    }
                    var result = decompiler.decompileFunction(fn, 60, monitor);
                    out.println(result.decompileCompleted() ? result.getDecompiledFunction().getC() : result.getErrorMessage());
                    out.flush();
                }
            } finally {
                decompiler.dispose();
            }
        }
        println("Exported " + functions.size() + " functions to " + output);
    }
}
