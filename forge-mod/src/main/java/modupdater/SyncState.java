package modupdater;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.Map;

import modupdater.lib.Ids;
import modupdater.lib.JarName;

final class SyncState {
    private SyncState() {
    }

    static ModSync.State readState(Path game) throws IOException {
        Path file = game.resolve("config").resolve("modupdater-managed.json");
        if (Files.isSymbolicLink(file)) {
            throw new IOException("manifest");
        }
        if (!Files.isRegularFile(file)) {
            return new ModSync.State();
        }
        Object parsed;
        try {
            parsed = Json.parse(Files.readString(file, StandardCharsets.UTF_8));
        } catch (RuntimeException ex) {
            throw new IOException("manifest");
        }
        if (!(parsed instanceof Map<?, ?> root)) {
            throw new IOException("manifest");
        }
        ModSync.State state = new ModSync.State();
        Object managed = root.get("managed");
        if (managed instanceof List<?> list) {
            for (Object item : list) {
                if (!(item instanceof Map<?, ?> map)) {
                    continue;
                }
                String id = ModSync.text(map.get("id"));
                String filename = ModSync.text(map.get("filename"));
                String sha = ModSync.hex(ModSync.text(map.get("sha256")), 64);
                if (!Ids.validId(id) || sha == null || !JarName.safeJar(filename)) {
                    continue;
                }
                state.managed.put(id, new ModSync.Managed(id, filename, sha));
            }
        }
        Object pending = root.get("pending");
        if (pending instanceof List<?> list) {
            for (Object item : list) {
                if (!(item instanceof Map<?, ?> map)) {
                    continue;
                }
                ModSync.Pending row = new ModSync.Pending();
                String staged = ModSync.text(map.get("staged"));
                String target = ModSync.text(map.get("target"));
                row.staged = staged == null ? "" : staged;
                row.target = target == null ? "" : target;
                Object deletes = map.get("delete");
                if (deletes instanceof List<?> names) {
                    for (Object name : names) {
                        if (name instanceof String jar && JarName.safeJar(jar)) {
                            row.delete.add(jar);
                        }
                    }
                }
                if (!row.staged.isEmpty() || !row.delete.isEmpty()) {
                    state.pending.add(row);
                }
            }
        }
        return state;
    }

    static void writeState(Path game, ModSync.State state) throws IOException {
        Path dir = game.resolve("config");
        Files.createDirectories(dir);
        Path file = dir.resolve("modupdater-managed.json");
        if (Files.isSymbolicLink(file)) {
            throw new IOException("manifest");
        }
        StringBuilder out = new StringBuilder();
        out.append("{\"managed\":[");
        boolean first = true;
        for (ModSync.Managed managed : state.managed.values()) {
            if (!first) {
                out.append(',');
            }
            first = false;
            out.append("{\"id\":").append(Json.quote(managed.id))
                    .append(",\"filename\":").append(Json.quote(managed.filename))
                    .append(",\"sha256\":").append(Json.quote(managed.sha)).append('}');
        }
        out.append("],\"pending\":[");
        first = true;
        for (ModSync.Pending pending : state.pending) {
            if (!first) {
                out.append(',');
            }
            first = false;
            out.append("{\"staged\":").append(Json.quote(pending.staged))
                    .append(",\"target\":").append(Json.quote(pending.target))
                    .append(",\"delete\":[");
            for (int n = 0; n < pending.delete.size(); n++) {
                if (n > 0) {
                    out.append(',');
                }
                out.append(Json.quote(pending.delete.get(n)));
            }
            out.append("]}");
        }
        out.append("]}");
        atomicWrite(file, out.toString());
    }

    static void atomicWrite(Path file, String body) throws IOException {
        Path tmp = file.resolveSibling(file.getFileName().toString() + ".part");
        Files.writeString(tmp, body, StandardCharsets.UTF_8);
        try {
            Files.move(tmp, file, StandardCopyOption.REPLACE_EXISTING);
        } finally {
            Files.deleteIfExists(tmp);
        }
    }

    static void dropPending(ModSync.State state, Path game, String filename) {
        List<ModSync.Pending> keep = new ArrayList<>();
        for (ModSync.Pending pending : state.pending) {
            if (filename.equals(pending.target)) {
                try {
                    if (pending.staged != null && !pending.staged.isEmpty()) {
                        Files.deleteIfExists(SyncFiles.resolveStaged(game, pending.staged));
                    }
                } catch (IOException ignored) {
                }
                continue;
            }
            pending.delete.remove(filename);
            if ((pending.staged != null && !pending.staged.isEmpty()) || !pending.delete.isEmpty()) {
                keep.add(pending);
            }
        }
        state.pending.clear();
        state.pending.addAll(keep);
    }

    static void clearPendingTarget(ModSync.State state, String filename) {
        if (filename == null || filename.isEmpty()) {
            return;
        }
        state.pending.removeIf(pending -> filename.equals(pending.target));
    }

    static void touchRestart() throws IOException {
        Path file = ModSync.gameDir.resolve("config").resolve("modupdater-restart");
        Files.createDirectories(file.getParent());
        if (Files.isSymbolicLink(file)) {
            return;
        }
        if (!Files.exists(file)) {
            Files.writeString(file, "1\n", StandardCharsets.UTF_8);
        }
    }

    static void clearRestart() throws IOException {
        Path file = ModSync.gameDir.resolve("config").resolve("modupdater-restart");
        if (Files.isSymbolicLink(file)) {
            return;
        }
        Files.deleteIfExists(file);
    }

    static void spawnFinish(Path game, Path mods, List<ModSync.Pending> left) {
        long pid = ProcessHandle.current().pid();
        try {
            String script;
            List<String> command;
            if (isWindows()) {
                StringBuilder ps = new StringBuilder();
                ps.append("Wait-Process -Id ").append(pid).append(" -ErrorAction SilentlyContinue; ");
                for (ModSync.Pending pending : left) {
                    appendWindows(ps, game, mods, pending);
                }
                script = ps.toString();
                command = List.of("powershell.exe", "-NoProfile", "-Command", script);
            } else {
                StringBuilder sh = new StringBuilder();
                sh.append("while kill -0 ").append(pid).append(" 2>/dev/null; do sleep 0.25; done\n");
                for (ModSync.Pending pending : left) {
                    appendUnix(sh, game, mods, pending);
                }
                script = sh.toString();
                command = List.of("/bin/sh", "-c", script);
            }
            new ProcessBuilder(command)
                    .redirectOutput(ProcessBuilder.Redirect.DISCARD)
                    .redirectError(ProcessBuilder.Redirect.DISCARD)
                    .start();
        } catch (IOException ignored) {
        }
    }

    private static void appendUnix(StringBuilder sh, Path game, Path mods, ModSync.Pending pending) {
        try {
            if (pending.staged != null && !pending.staged.isEmpty() && JarName.safeJar(pending.target)) {
                Path from = SyncFiles.resolveStaged(game, pending.staged);
                Path to = mods.resolve(pending.target);
                sh.append("mv -f ").append(shQuote(from)).append(' ').append(shQuote(to)).append('\n');
            }
        } catch (IOException ignored) {
        }
        for (String name : pending.delete) {
            if (!JarName.safeJar(name)) {
                continue;
            }
            sh.append("rm -f ").append(shQuote(mods.resolve(name))).append('\n');
        }
    }

    private static void appendWindows(StringBuilder ps, Path game, Path mods, ModSync.Pending pending) {
        try {
            if (pending.staged != null && !pending.staged.isEmpty() && JarName.safeJar(pending.target)) {
                Path from = SyncFiles.resolveStaged(game, pending.staged);
                Path to = mods.resolve(pending.target);
                ps.append("Move-Item -LiteralPath ").append(psQuote(from))
                        .append(" -Destination ").append(psQuote(to)).append(" -Force; ");
            }
        } catch (IOException ignored) {
        }
        for (String name : pending.delete) {
            if (!JarName.safeJar(name)) {
                continue;
            }
            ps.append("Remove-Item -LiteralPath ").append(psQuote(mods.resolve(name)))
                    .append(" -Force -ErrorAction SilentlyContinue; ");
        }
    }

    private static String shQuote(Path path) {
        return "'" + path.toAbsolutePath().toString().replace("'", "'\\''") + "'";
    }

    private static String psQuote(Path path) {
        return "'" + path.toAbsolutePath().toString().replace("'", "''") + "'";
    }

    private static boolean isWindows() {
        return System.getProperty("os.name", "").toLowerCase(Locale.ROOT).contains("win");
    }
}
