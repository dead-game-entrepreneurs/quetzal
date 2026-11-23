// Map the online MWo3 images into a fresh COMMX_FR.ELF import before analysis.
// @category Quetzal
import ghidra.app.script.GhidraScript;
import ghidra.program.model.mem.MemoryBlock;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import java.nio.file.Files;
import java.nio.file.Path;
import java.io.ByteArrayInputStream;
import java.security.MessageDigest;
import java.util.HexFormat;

public class LoadCommandosOverlays extends GhidraScript {
    public void run() throws Exception {
        String[] args = getScriptArgs();
        if (args.length != 1 || !currentProgram.getName().equals("COMMX_FR.ELF")) {
            throw new IllegalArgumentException("Expected a fresh COMMX_FR.ELF import and disc directory");
        }
        var memory = currentProgram.getMemory();
        memory.createInitializedBlock("bootstrap_bss", toAddr(0x149000), 0x8e80, (byte)0, monitor, false);
        for (String name : new String[]{"FIN_RED.BIN", "RVOUS_VF.BIN"}) {
            byte[] bytes = Files.readAllBytes(Path.of(args[0], name));
            ByteBuffer header = ByteBuffer.wrap(bytes).order(ByteOrder.LITTLE_ENDIAN);
            if (bytes.length < 128 || header.getInt(0) != 0x336f574d) {
                throw new IllegalArgumentException("Invalid MWo3 image: " + name);
            }
            long base = Integer.toUnsignedLong(header.getInt(8));
            long expected = name.equals("FIN_RED.BIN") ? 0x151e80L : 0x8c0480L;
            if (base != expected) throw new IllegalArgumentException("Unexpected image base: " + name);
            long textEnd = 64L + Integer.toUnsignedLong(header.getInt(12));
            if (textEnd >= bytes.length || textEnd + Integer.toUnsignedLong(header.getInt(16)) != bytes.length) {
                throw new IllegalArgumentException("Invalid image sections: " + name);
            }
            var address = toAddr(base);
            MemoryBlock block = memory.getBlock(address);
            if (block != null) throw new IllegalArgumentException("Expected unmapped image space for " + name);
            block = memory.createInitializedBlock(name, address, new ByteArrayInputStream(bytes), bytes.length, monitor, false);
            block.setRead(true);
            block.setWrite(true);
            block.setExecute(true);
            memory.split(block, address.add(textEnd));
            memory.getBlock(address.add(textEnd)).setExecute(false);
            long bss = Integer.toUnsignedLong(header.getInt(20));
            memory.createInitializedBlock(name + "_bss", address.add(bytes.length), bss, (byte)0, monitor, false);
            createLabel(address, name.replace('.', '_') + "_header", true);
            disassemble(address.add(128));
            createFunction(address.add(128), name.replace('.', '_') + "_first");
            String hash = HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes));
            setPlateComment(address, name + " includes MWo3 header; SHA-256 " + hash);
            println("Mapped " + name + " including header at " + address + " length " + bytes.length + " SHA256 " + hash);
        }
    }
}
