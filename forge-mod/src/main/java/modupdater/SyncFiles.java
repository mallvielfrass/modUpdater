package modupdater;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.DirectoryStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.zip.ZipEntry;
import java.util.zip.ZipInputStream;

import modupdater.lib.JarMeta;
import modupdater.lib.JarName;
import modupdater.lib.LocalJar;

final class SyncFiles {
    private static final long MAX_UNZIP = 256L << 20;

    private SyncFiles() {
    }

    static boolean tryReplace(Path staged, Path target) throws IOException {
        if (Files.isSymbolicLink(target)) {
            throw new IOException("symlink");
        }
        if (Files.exists(target) && !Files.isRegularFile(target)) {
            throw new IOException("target");
        }
        Path parent = target.getParent();
        if (parent == null) {
            throw new IOException("target");
        }
        Files.createDirectories(parent);
        Path tmp = parent.resolve(target.getFileName().toString() + ".modupdater-new");
        Files.copy(staged, tmp, StandardCopyOption.REPLACE_EXISTING);
        try {
            try {
                Files.move(tmp, target, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
            } catch (IOException atomic) {
                Files.move(tmp, target, StandardCopyOption.REPLACE_EXISTING);
            }
            return true;
        } catch (IOException ex) {
            Files.deleteIfExists(tmp);
            return false;
        }
    }

    static Map<String, String> hashJars(Path mods) throws IOException {
        Map<String, String> local = new LinkedHashMap<>();
        if (!Files.isDirectory(mods)) {
            return local;
        }
        try (DirectoryStream<Path> stream = Files.newDirectoryStream(mods, "*.jar")) {
            for (Path path : stream) {
                if (Files.isSymbolicLink(path) || !Files.isRegularFile(path)) {
                    continue;
                }
                String name = path.getFileName().toString();
                if (!JarName.safeJar(name)) {
                    continue;
                }
                try {
                    local.put(name, sha256(path));
                } catch (IOException ignored) {
                }
            }
        }
        return local;
    }

    static String findHash(Map<String, String> local, Set<String> used, String sha) {
        for (Map.Entry<String, String> entry : local.entrySet()) {
            if (!used.contains(entry.getKey()) && sha.equals(entry.getValue())) {
                return entry.getKey();
            }
        }
        return null;
    }

    static void sweepTemps(Path mods) throws IOException {
        if (!Files.isDirectory(mods)) {
            return;
        }
        try (DirectoryStream<Path> stream = Files.newDirectoryStream(mods)) {
            for (Path path : stream) {
                String name = path.getFileName().toString();
                if (!name.endsWith(".modupdater-new") && !name.endsWith(".modupdater-part")) {
                    continue;
                }
                if (!Files.isSymbolicLink(path) && Files.isRegularFile(path)) {
                    Files.deleteIfExists(path);
                }
            }
        }
    }

    static int deleteJar(Path mods, String name) {
        if (!JarName.safeJar(name)) {
            return 0;
        }
        Path path = mods.resolve(name).normalize();
        if (!path.getParent().equals(mods)) {
            return 0;
        }
        try {
            if (Files.isSymbolicLink(path)) {
                return 0;
            }
            if (!Files.exists(path)) {
                return 0;
            }
            if (!Files.isRegularFile(path)) {
                return 0;
            }
            Files.delete(path);
            return 1;
        } catch (IOException ex) {
            return -1;
        }
    }

    static boolean sameFileHash(Path staged, Path target) throws IOException {
        if (Files.isSymbolicLink(target) || !Files.isRegularFile(target)) {
            return false;
        }
        return sha256(staged).equals(sha256(target));
    }

    static Path staging(Path game, String id) throws IOException {
        Path dir = game.resolve(".modupdater-staging");
        Files.createDirectories(dir);
        return dir.resolve(id + ".bin");
    }

    static Path resolveStaged(Path game, String rel) throws IOException {
        if (rel == null || rel.indexOf('\\') >= 0 || rel.contains("..") || !rel.startsWith(".modupdater-staging/")) {
            throw new IOException("staged");
        }
        String name = rel.substring(".modupdater-staging/".length());
        if (!name.matches("^[0-9a-f]{32}\\.bin$")) {
            throw new IOException("staged");
        }
        Path dir = game.resolve(".modupdater-staging").normalize();
        Path file = dir.resolve(name).normalize();
        if (!file.startsWith(dir)) {
            throw new IOException("staged");
        }
        return file;
    }

    static Path versionRoot(Path mods) throws IOException {
        Path parent = mods.toAbsolutePath().normalize().getParent();
        if (parent == null) {
            throw new IOException("config");
        }
        return parent;
    }

    static void writeConfigFile(Path root, String dest, Path src) throws IOException {
        Path target = resolveDest(root, dest);
        if (Files.isDirectory(target)) {
            throw new IOException("config");
        }
        Path parent = target.getParent();
        if (parent == null) {
            throw new IOException("config");
        }
        Files.createDirectories(parent);
        Path tmp = parent.resolve(target.getFileName().toString() + ".modupdater-part");
        Files.copy(src, tmp, StandardCopyOption.REPLACE_EXISTING);
        try {
            Files.move(tmp, target, StandardCopyOption.REPLACE_EXISTING);
        } finally {
            Files.deleteIfExists(tmp);
        }
    }

    static void unzipConfig(Path root, String dest, Path zip) throws IOException {
        Path dir = resolveDest(root, dest);
        if (Files.exists(dir) && !Files.isDirectory(dir)) {
            throw new IOException("config");
        }
        Files.createDirectories(dir);
        try (ZipInputStream in = new ZipInputStream(Files.newInputStream(zip))) {
            ZipEntry entry;
            int count = 0;
            long total = 0;
            byte[] buf = new byte[8192];
            while ((entry = in.getNextEntry()) != null) {
                if (++count > 4000) {
                    throw new IOException("config");
                }
                String name = entry.getName().replace('\\', '/');
                if (name.isEmpty()) {
                    continue;
                }
                boolean directory = entry.isDirectory() || name.endsWith("/");
                if (directory) {
                    name = name.endsWith("/") ? name.substring(0, name.length() - 1) : name;
                }
                if (!validDest(name)) {
                    throw new IOException("config");
                }
                Path out = resolveDest(dir, name);
                if (directory) {
                    Files.createDirectories(out);
                    continue;
                }
                Path parent = out.getParent();
                if (parent != null) {
                    Files.createDirectories(parent);
                }
                try (OutputStream output = Files.newOutputStream(out)) {
                    int n;
                    while ((n = in.read(buf)) >= 0) {
                        total += n;
                        if (total > MAX_UNZIP) {
                            throw new IOException("config");
                        }
                        output.write(buf, 0, n);
                    }
                }
            }
        }
    }

    private static Path resolveDest(Path root, String dest) throws IOException {
        if (!validDest(dest)) {
            throw new IOException("config");
        }
        Path base = root.toRealPath();
        Path cursor = base;
        String[] parts = dest.split("/", -1);
        for (int n = 0; n < parts.length; n++) {
            Path next = cursor.resolve(parts[n]);
            if (Files.isSymbolicLink(next)) {
                throw new IOException("config");
            }
            if (Files.isDirectory(next)) {
                Path real = next.toRealPath();
                if (!real.startsWith(base)) {
                    throw new IOException("config");
                }
                cursor = real;
            } else if (Files.exists(next)) {
                if (n + 1 != parts.length) {
                    throw new IOException("config");
                }
                Path normal = next.toAbsolutePath().normalize();
                if (!normal.startsWith(base)) {
                    throw new IOException("config");
                }
                return normal;
            } else {
                Path rest = next.normalize();
                for (int k = n + 1; k < parts.length; k++) {
                    rest = rest.resolve(parts[k]).normalize();
                }
                if (!rest.startsWith(base)) {
                    throw new IOException("config");
                }
                return rest;
            }
        }
        if (cursor.equals(base) || !cursor.startsWith(base)) {
            throw new IOException("config");
        }
        return cursor;
    }

    static Map<String, String> readRegistry(Path root) throws IOException {
        Path file = root.resolve(".modupdater-configs.json");
        if (Files.isSymbolicLink(file)) {
            throw new IOException("config");
        }
        if (!Files.isRegularFile(file)) {
            return new LinkedHashMap<>();
        }
        Object parsed;
        try {
            parsed = Json.parse(Files.readString(file, StandardCharsets.UTF_8));
        } catch (RuntimeException ex) {
            throw new IOException("config");
        }
        if (!(parsed instanceof Map<?, ?> map)) {
            throw new IOException("config");
        }
        Map<String, String> out = new LinkedHashMap<>();
        for (Map.Entry<?, ?> entry : map.entrySet()) {
            if (!(entry.getKey() instanceof String key) || !(entry.getValue() instanceof String value)) {
                throw new IOException("config");
            }
            out.put(key, value.toLowerCase(Locale.ROOT));
        }
        return out;
    }

    static void writeRegistry(Path root, Map<String, String> registry) throws IOException {
        Path file = root.resolve(".modupdater-configs.json");
        if (Files.isSymbolicLink(file)) {
            throw new IOException("config");
        }
        StringBuilder out = new StringBuilder();
        out.append('{');
        boolean first = true;
        for (Map.Entry<String, String> entry : registry.entrySet()) {
            if (!first) {
                out.append(',');
            }
            first = false;
            out.append(Json.quote(entry.getKey())).append(':').append(Json.quote(entry.getValue()));
        }
        out.append('}');
        SyncState.atomicWrite(file, out.toString());
    }

    static String copyHash(InputStream in, Path dest, long max) throws IOException {
        MessageDigest digest;
        try {
            digest = MessageDigest.getInstance("SHA-256");
        } catch (NoSuchAlgorithmException ex) {
            throw new IOException("hash");
        }
        long total = 0;
        try (OutputStream out = Files.newOutputStream(dest)) {
            byte[] buf = new byte[8192];
            int n;
            while ((n = in.read(buf)) >= 0) {
                total += n;
                if (total > max) {
                    throw new IOException("size");
                }
                digest.update(buf, 0, n);
                out.write(buf, 0, n);
                noteProgress(total);
            }
        } catch (IOException ex) {
            Files.deleteIfExists(dest);
            throw ex;
        }
        return HexFormat.of().formatHex(digest.digest());
    }

    static String sha256(Path path) throws IOException {
        MessageDigest digest;
        try {
            digest = MessageDigest.getInstance("SHA-256");
        } catch (NoSuchAlgorithmException ex) {
            throw new IOException("hash");
        }
        try (InputStream in = Files.newInputStream(path)) {
            byte[] buf = new byte[8192];
            int n;
            while ((n = in.read(buf)) >= 0) {
                digest.update(buf, 0, n);
            }
        }
        return HexFormat.of().formatHex(digest.digest());
    }

    static boolean validDest(String dest) {
        if (dest == null || dest.isEmpty() || dest.charAt(0) == '/' || dest.indexOf('\\') >= 0
                || dest.indexOf('\0') >= 0 || dest.indexOf(':') >= 0) {
            return false;
        }
        String[] parts = dest.split("/", -1);
        if (parts.length == 0) {
            return false;
        }
        for (String part : parts) {
            if (part.isEmpty() || ".".equals(part) || "..".equals(part)) {
                return false;
            }
        }
        return true;
    }

    private static void noteProgress(long loaded) {
        ModSync.Prog prog = ModSync.PROG.get();
        if (prog == null || prog.ui == null) {
            return;
        }
        double part = prog.total > 0 ? Math.min(1d, loaded / (double) prog.total) : 0;
        int pct = (int) Math.round(((Math.max(prog.index, 1) - 1 + part) / Math.max(prog.count, 1)) * 100);
        String label = prog.label == null ? "" : prog.label;
        prog.ui.report(label.isEmpty() ? pct + "%" : label + " · " + pct + "%", Math.min(100, Math.max(0, pct)));
    }

    static List<LocalJar> scanLocals(Path mods) throws IOException {
        Map<String, String> hashes = hashJars(mods);
        List<LocalJar> out = new ArrayList<>();
        for (Map.Entry<String, String> entry : hashes.entrySet()) {
            LocalJar jar = new LocalJar();
            jar.filename = entry.getKey();
            jar.sha = entry.getValue();
            jar.slug = JarMeta.readSlug(mods.resolve(entry.getKey()), entry.getKey());
            out.add(jar);
        }
        return out;
    }
}
