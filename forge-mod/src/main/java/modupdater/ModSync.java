package modupdater;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.URI;
import java.text.Collator;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.DirectoryStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.util.zip.ZipEntry;
import java.util.zip.ZipFile;
import java.util.zip.ZipInputStream;
import java.time.Instant;
import java.time.ZoneId;
import java.time.ZonedDateTime;
import java.time.format.DateTimeParseException;

import org.apache.logging.log4j.LogManager;
import org.apache.logging.log4j.Logger;

final class ModSync {
    static final String ORIGIN = Origin.VALUE;

    private static final Logger LOG = LogManager.getLogger();
    private static final long MAX_JAR = 256L << 20;
    private static final long MAX_CFG = 64L << 20;
    private static final long MAX_JSON = 4L << 20;
    private static final long MAX_UNZIP = 256L << 20;
    private static final Object LOCK = new Object();
    private static final ThreadLocal<Prog> PROG = new ThreadLocal<>();
    private static final HttpClient HTTP = HttpClient.newBuilder()
            .followRedirects(HttpClient.Redirect.NEVER)
            .connectTimeout(Duration.ofSeconds(15))
            .build();

    private static volatile Path gameDir;
    private static volatile Path modsDir;

    private ModSync() {
    }

    static void bind(Path game, Path mods) {
        gameDir = game.toAbsolutePath().normalize();
        modsDir = mods.toAbsolutePath().normalize();
    }

    static void run() {
        if (gameDir == null || modsDir == null) {
            return;
        }
        try {
            ClientHooks.publishRequired(requiredPending());
        } catch (Exception ex) {
            LOG.warn("mod check failed: {}", ex.getMessage());
            if (!autoUpdate()) {
                ClientHooks.publish(ClientHooks.FAIL);
                return;
            }
        }
        if (!autoUpdate()) {
            return;
        }
        Result result = new Result();
        try {
            sync(result);
            if (result.changed) {
                touchRestart();
                ClientHooks.publish(ClientHooks.RESTART);
            } else {
                clearRestart();
                ClientHooks.publish("");
            }
        } catch (Exception ex) {
            LOG.warn("mod sync failed: {}", ex.getMessage());
            if (result.changed) {
                try {
                    touchRestart();
                } catch (IOException ignored) {
                }
                ClientHooks.publish(ClientHooks.RESTART);
            } else if ("config".equals(ex.getMessage())) {
                ClientHooks.publish(ClientHooks.CONFIG_FAIL);
            } else {
                ClientHooks.publish(ClientHooks.FAIL);
            }
        }
        try {
            ClientHooks.publishRequired(requiredPending());
        } catch (Exception ignored) {
        }
    }

    static void onShutdown() {
        Path game = gameDir;
        Path mods = modsDir;
        if (game == null || mods == null) {
            return;
        }
        List<Pending> left;
        synchronized (LOCK) {
            try {
                applyPending(game, mods);
                left = new ArrayList<>(readState(game).pending);
            } catch (Exception ex) {
                return;
            }
        }
        if (!left.isEmpty()) {
            spawnFinish(game, mods, left);
        }
    }

    private static void sync(Result result) throws Exception {
        Path game = gameDir;
        Path mods = modsDir;
        Files.createDirectories(mods);
        synchronized (LOCK) {
            if (applyPending(game, mods)) {
                result.changed = true;
            }
            sweepTemps(mods);
        }
        Map<String, String> local = hashJars(mods);
        Catalog catalog = loadPublic();
        List<Remote> remote = catalog.mods;
        removeRevoked(mods, remote, catalog.revoked, local, result);
        List<String> delete = new ArrayList<>();
        List<Remote> need;
        synchronized (LOCK) {
            State state = readState(game);
            need = plan(game, local, state, remote, delete);
            writeState(game, state);
        }
        try {
            for (Remote rem : need) {
                Path bin = staging(game, rem.id);
                if (!Files.isRegularFile(bin) || !rem.sha.equals(sha256(bin))) {
                    download("/api/mods/" + rem.id + "/download", bin, rem.sha, MAX_JAR);
                }
                synchronized (LOCK) {
                    State state = readState(game);
                    commit(game, mods, local, state, rem, result);
                    writeState(game, state);
                }
            }
        } finally {
            synchronized (LOCK) {
                State state = readState(game);
                if (finishDeletes(mods, state, delete, result)) {
                    writeState(game, state);
                }
            }
        }
        try {
            installConfigs(versionRoot(mods), remote, result);
        } catch (IOException ex) {
            throw new IOException("config");
        }
    }

    private static List<Remote> plan(Path game, Map<String, String> local, State state, List<Remote> remote,
                                      List<String> delete) {
        Map<String, Remote> byId = new LinkedHashMap<>();
        for (Remote rem : remote) {
            byId.putIfAbsent(rem.id, rem);
        }
        var it = state.managed.entrySet().iterator();
        while (it.hasNext()) {
            Managed managed = it.next().getValue();
            if (byId.containsKey(managed.id)) {
                continue;
            }
            delete.add(managed.filename);
            dropPending(state, game, managed.filename);
            it.remove();
        }
        Set<String> keep = new java.util.HashSet<>();
        List<Remote> need = new ArrayList<>();
        for (Remote rem : byId.values()) {
            Managed managed = state.managed.get(rem.id);
            String hashed = null;
            if (managed != null && rem.sha.equals(local.get(managed.filename)) && !keep.contains(managed.filename)) {
                hashed = managed.filename;
            }
            if (hashed == null) {
                hashed = findHash(local, keep, rem.sha);
            }
            if (hashed != null) {
                keep.add(hashed);
                if (managed != null && !managed.filename.equals(hashed)) {
                    delete.add(managed.filename);
                    state.managed.remove(rem.id);
                }
                continue;
            }
            if (keep.contains(rem.filename)) {
                continue;
            }
            keep.add(rem.filename);
            need.add(rem);
        }
        return need;
    }

    private static void commit(Path game, Path mods, Map<String, String> local, State state, Remote rem, Result result) throws IOException {
        Path bin = staging(game, rem.id);
        if (!Files.isRegularFile(bin) || !rem.sha.equals(sha256(bin))) {
            throw new IOException("hash");
        }
        Managed previous = state.managed.get(rem.id);
        Path target = mods.resolve(rem.filename);
        boolean swapped = tryReplace(bin, target);
        state.managed.put(rem.id, new Managed(rem.id, rem.filename, rem.sha));
        clearPendingTarget(state, rem.filename);
        if (previous != null) {
            clearPendingTarget(state, previous.filename);
        }
        if (swapped) {
            local.put(rem.filename, rem.sha);
            Files.deleteIfExists(bin);
            if (previous != null && !previous.filename.equals(rem.filename)) {
                if (deleteJar(mods, previous.filename) != 1) {
                    Pending pending = new Pending();
                    pending.delete.add(previous.filename);
                    state.pending.add(pending);
                }
            }
        } else {
            Pending pending = new Pending();
            pending.staged = ".modupdater-staging/" + rem.id + ".bin";
            pending.target = rem.filename;
            if (previous != null && !previous.filename.equals(rem.filename)) {
                pending.delete.add(previous.filename);
            }
            state.pending.add(pending);
        }
        result.changed = true;
    }

    private static boolean finishDeletes(Path mods, State state, List<String> delete, Result result) {
        boolean wrote = false;
        for (String name : delete) {
            if (stillWanted(state, name)) {
                continue;
            }
            int deleted = deleteJar(mods, name);
            if (deleted == 1) {
                result.changed = true;
            } else if (deleted < 0) {
                Pending pending = new Pending();
                pending.delete.add(name);
                state.pending.add(pending);
                wrote = true;
                result.changed = true;
            }
        }
        return wrote;
    }

    private static boolean stillWanted(State state, String name) {
        for (Managed managed : state.managed.values()) {
            if (name.equals(managed.filename)) {
                return true;
            }
        }
        for (Pending pending : state.pending) {
            if (name.equals(pending.target)) {
                return true;
            }
        }
        return false;
    }

    private static boolean applyPending(Path game, Path mods) throws IOException {
        State state = readState(game);
        boolean changed = false;
        List<Pending> left = new ArrayList<>();
        for (Pending pending : state.pending) {
            boolean moveOk = true;
            if (pending.staged != null && !pending.staged.isEmpty()) {
                Path staged;
                try {
                    staged = resolveStaged(game, pending.staged);
                } catch (IOException ex) {
                    continue;
                }
                if (!safeJar(pending.target)) {
                    continue;
                }
                Path target = mods.resolve(pending.target);
                if (!Files.isRegularFile(staged)) {
                    moveOk = true;
                } else if (sameFileHash(staged, target)) {
                    Files.deleteIfExists(staged);
                } else if (!tryReplace(staged, target)) {
                    moveOk = false;
                } else {
                    Files.deleteIfExists(staged);
                    changed = true;
                }
            }
            if (!moveOk) {
                left.add(pending);
                continue;
            }
            List<String> still = new ArrayList<>();
            for (String name : pending.delete) {
                int deleted = deleteJar(mods, name);
                if (deleted < 0) {
                    still.add(name);
                } else if (deleted == 1) {
                    changed = true;
                }
            }
            if (!still.isEmpty()) {
                Pending rest = new Pending();
                rest.delete.addAll(still);
                left.add(rest);
            }
        }
        state.pending.clear();
        state.pending.addAll(left);
        writeState(game, state);
        return changed;
    }

    private static void installConfigs(Path root, List<Remote> mods, Result result) throws IOException {
        boolean any = false;
        for (Remote rem : mods) {
            if (!rem.configs.isEmpty()) {
                any = true;
                break;
            }
        }
        if (!any) {
            return;
        }
        Map<String, String> registry = readRegistry(root);
        boolean dirty = false;
        for (Remote rem : mods) {
            for (Cfg cfg : rem.configs) {
                if (cfg.sha.equals(registry.get(cfg.id))) {
                    continue;
                }
                Prog prog = PROG.get();
                if (prog != null) {
                    prog.label = "конфиг " + cfg.dest;
                }
                Path tmp = Files.createTempFile("modupdater-cfg-", ".bin");
                try {
                    download("/api/mods/" + rem.id + "/configs/" + cfg.id + "/file", tmp, cfg.sha, MAX_CFG);
                    if ("file".equals(cfg.kind)) {
                        writeConfigFile(root, cfg.dest, tmp);
                    } else if ("dir".equals(cfg.kind)) {
                        unzipConfig(root, cfg.dest, tmp);
                    } else {
                        continue;
                    }
                    registry.put(cfg.id, cfg.sha);
                    dirty = true;
                    result.changed = true;
                } finally {
                    Files.deleteIfExists(tmp);
                }
            }
        }
        if (dirty) {
            writeRegistry(root, registry);
        }
    }

    private static List<Remote> fetchMods() throws IOException {
        return loadPublic().mods;
    }

    private static Catalog loadPublic() throws IOException {
        Object parsed;
        try {
            parsed = Json.parse(httpText("/api/mods"));
        } catch (RuntimeException ex) {
            throw new IOException("json");
        }
        if (!(parsed instanceof Map<?, ?> root)) {
            throw new IOException("json");
        }
        Object mods = root.get("mods");
        if (!(mods instanceof List<?> list)) {
            throw new IOException("json");
        }
        List<Remote> out = new ArrayList<>();
        for (Object item : list) {
            if (!(item instanceof Map<?, ?> map)) {
                continue;
            }
            if (Boolean.TRUE.equals(map.get("revoked"))) {
                continue;
            }
            boolean clientOnly = Boolean.TRUE.equals(map.get("client_only"));
            boolean clientRequired = Boolean.TRUE.equals(map.get("client_required"));
            if (Boolean.FALSE.equals(map.get("client")) && !clientOnly) {
                continue;
            }
            String id = text(map.get("id"));
            String filename = text(map.get("filename"));
            String sha = hex(text(map.get("sha256")), 64);
            if (!validId(id) || sha == null || !safeJar(filename)) {
                continue;
            }
            String name = text(map.get("name"));
            String version = text(map.get("version"));
            String created = text(map.get("created_at"));
            String slug = text(map.get("slug"));
            out.add(new Remote(
                    id,
                    name == null || name.isEmpty() ? filename : name,
                    filename,
                    version == null ? "" : version,
                    created == null ? "" : created,
                    slug == null ? "" : slug,
                    sha,
                    clientOnly,
                    clientRequired,
                    configsOf(map.get("configs"))));
        }
        List<Revoked> revoked = new ArrayList<>();
        Object rawRevoked = root.get("revoked");
        if (rawRevoked instanceof List<?> revokedList) {
            for (Object item : revokedList) {
                if (!(item instanceof Map<?, ?> map)) {
                    continue;
                }
                String filename = text(map.get("filename"));
                String sha = hex(text(map.get("sha256")), 64);
                String slug = text(map.get("slug"));
                if (sha == null || filename == null || !safeJar(filename)) {
                    continue;
                }
                revoked.add(new Revoked(sha, slug == null ? "" : slug, filename, Boolean.TRUE.equals(map.get("client_only")), Boolean.TRUE.equals(map.get("client_required"))));
            }
        }
        return new Catalog(out, revoked);
    }

    private static List<Cfg> configsOf(Object raw) {
        List<Cfg> out = new ArrayList<>();
        if (!(raw instanceof List<?> list)) {
            return out;
        }
        for (Object item : list) {
            if (!(item instanceof Map<?, ?> map)) {
                continue;
            }
            String id = text(map.get("id"));
            String dest = text(map.get("dest"));
            String kind = text(map.get("kind"));
            String sha = hex(text(map.get("sha256")), 64);
            if (!validId(id) || sha == null || dest == null || !validDest(dest)) {
                continue;
            }
            if (!"file".equals(kind) && !"dir".equals(kind)) {
                continue;
            }
            out.add(new Cfg(id, dest, kind, sha));
        }
        return out;
    }

    private static void download(String path, Path dest, String expect, long max) throws IOException {
        HttpRequest request = HttpRequest.newBuilder(api(path))
                .timeout(Duration.ofMinutes(3))
                .header("Accept", "application/octet-stream")
                .GET()
                .build();
        HttpResponse<InputStream> response;
        try {
            response = HTTP.send(request, HttpResponse.BodyHandlers.ofInputStream());
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new IOException("interrupted");
        }
        try (InputStream in = response.body()) {
            if (response.statusCode() != 200) {
                throw new IOException("http");
            }
            long advertised = response.headers().firstValueAsLong("Content-Length").orElse(-1);
            if (advertised > max) {
                throw new IOException("size");
            }
            Prog prog = PROG.get();
            if (prog != null) {
                prog.total = advertised;
            }
            Path parent = dest.getParent();
            if (parent != null) {
                Files.createDirectories(parent);
            }
            Path part = dest.resolveSibling(dest.getFileName().toString() + ".part");
            try {
                String got = copyHash(in, part, max);
                if (!expect.equals(got)) {
                    throw new IOException("hash");
                }
                Files.move(part, dest, StandardCopyOption.REPLACE_EXISTING);
            } finally {
                Files.deleteIfExists(part);
            }
        }
    }

    private static String httpText(String path) throws IOException {
        HttpRequest request = HttpRequest.newBuilder(api(path))
                .timeout(Duration.ofSeconds(30))
                .header("Accept", "application/json")
                .GET()
                .build();
        HttpResponse<InputStream> response;
        try {
            response = HTTP.send(request, HttpResponse.BodyHandlers.ofInputStream());
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new IOException("interrupted");
        }
        try (InputStream in = response.body()) {
            if (response.statusCode() != 200) {
                throw new IOException("http");
            }
            return new String(readLimited(in, MAX_JSON), StandardCharsets.UTF_8);
        }
    }

    private static byte[] readLimited(InputStream in, long max) throws IOException {
        byte[] buf = new byte[8192];
        int n;
        long total = 0;
        try (var out = new java.io.ByteArrayOutputStream()) {
            while ((n = in.read(buf)) >= 0) {
                total += n;
                if (total > max) {
                    throw new IOException("size");
                }
                out.write(buf, 0, n);
            }
            return out.toByteArray();
        }
    }

    private static URI api(String path) {
        if (path == null || !path.startsWith("/api/") || path.indexOf('\\') >= 0 || path.indexOf('?') >= 0
                || path.indexOf('#') >= 0 || path.indexOf('\0') >= 0 || path.contains("..")) {
            throw new IllegalArgumentException("path");
        }
        return URI.create(ORIGIN + path);
    }

    private static boolean tryReplace(Path staged, Path target) throws IOException {
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

    private static Map<String, String> hashJars(Path mods) throws IOException {
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
                if (!safeJar(name)) {
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

    private static String findHash(Map<String, String> local, Set<String> used, String sha) {
        for (Map.Entry<String, String> entry : local.entrySet()) {
            if (!used.contains(entry.getKey()) && sha.equals(entry.getValue())) {
                return entry.getKey();
            }
        }
        return null;
    }

    private static void sweepTemps(Path mods) throws IOException {
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

    private static int deleteJar(Path mods, String name) {
        if (!safeJar(name)) {
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

    private static boolean sameFileHash(Path staged, Path target) throws IOException {
        if (Files.isSymbolicLink(target) || !Files.isRegularFile(target)) {
            return false;
        }
        return sha256(staged).equals(sha256(target));
    }

    private static Path staging(Path game, String id) throws IOException {
        Path dir = game.resolve(".modupdater-staging");
        Files.createDirectories(dir);
        return dir.resolve(id + ".bin");
    }

    private static Path resolveStaged(Path game, String rel) throws IOException {
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

    private static Path versionRoot(Path mods) throws IOException {
        Path parent = mods.toAbsolutePath().normalize().getParent();
        if (parent == null) {
            throw new IOException("config");
        }
        return parent;
    }

    private static void writeConfigFile(Path root, String dest, Path src) throws IOException {
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

    private static void unzipConfig(Path root, String dest, Path zip) throws IOException {
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

    private static Map<String, String> readRegistry(Path root) throws IOException {
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

    private static void writeRegistry(Path root, Map<String, String> registry) throws IOException {
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
        atomicWrite(file, out.toString());
    }

    private static State readState(Path game) throws IOException {
        Path file = game.resolve("config").resolve("modupdater-managed.json");
        if (Files.isSymbolicLink(file)) {
            throw new IOException("manifest");
        }
        if (!Files.isRegularFile(file)) {
            return new State();
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
        State state = new State();
        Object managed = root.get("managed");
        if (managed instanceof List<?> list) {
            for (Object item : list) {
                if (!(item instanceof Map<?, ?> map)) {
                    continue;
                }
                String id = text(map.get("id"));
                String filename = text(map.get("filename"));
                String sha = hex(text(map.get("sha256")), 64);
                if (!validId(id) || sha == null || !safeJar(filename)) {
                    continue;
                }
                state.managed.put(id, new Managed(id, filename, sha));
            }
        }
        Object pending = root.get("pending");
        if (pending instanceof List<?> list) {
            for (Object item : list) {
                if (!(item instanceof Map<?, ?> map)) {
                    continue;
                }
                Pending row = new Pending();
                String staged = text(map.get("staged"));
                String target = text(map.get("target"));
                row.staged = staged == null ? "" : staged;
                row.target = target == null ? "" : target;
                Object deletes = map.get("delete");
                if (deletes instanceof List<?> names) {
                    for (Object name : names) {
                        if (name instanceof String jar && safeJar(jar)) {
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

    private static void writeState(Path game, State state) throws IOException {
        Path dir = game.resolve("config");
        Files.createDirectories(dir);
        Path file = dir.resolve("modupdater-managed.json");
        if (Files.isSymbolicLink(file)) {
            throw new IOException("manifest");
        }
        StringBuilder out = new StringBuilder();
        out.append("{\"managed\":[");
        boolean first = true;
        for (Managed managed : state.managed.values()) {
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
        for (Pending pending : state.pending) {
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

    private static void atomicWrite(Path file, String body) throws IOException {
        Path tmp = file.resolveSibling(file.getFileName().toString() + ".part");
        Files.writeString(tmp, body, StandardCharsets.UTF_8);
        try {
            Files.move(tmp, file, StandardCopyOption.REPLACE_EXISTING);
        } finally {
            Files.deleteIfExists(tmp);
        }
    }

    private static void dropPending(State state, Path game, String filename) {
        List<Pending> keep = new ArrayList<>();
        for (Pending pending : state.pending) {
            if (filename.equals(pending.target)) {
                try {
                    if (pending.staged != null && !pending.staged.isEmpty()) {
                        Files.deleteIfExists(resolveStaged(game, pending.staged));
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

    private static void clearPendingTarget(State state, String filename) {
        if (filename == null || filename.isEmpty()) {
            return;
        }
        state.pending.removeIf(pending -> filename.equals(pending.target));
    }

    private static void touchRestart() throws IOException {
        Path file = gameDir.resolve("config").resolve("modupdater-restart");
        Files.createDirectories(file.getParent());
        if (Files.isSymbolicLink(file)) {
            return;
        }
        if (!Files.exists(file)) {
            Files.writeString(file, "1\n", StandardCharsets.UTF_8);
        }
    }

    private static void clearRestart() throws IOException {
        Path file = gameDir.resolve("config").resolve("modupdater-restart");
        if (Files.isSymbolicLink(file)) {
            return;
        }
        Files.deleteIfExists(file);
    }

    private static void spawnFinish(Path game, Path mods, List<Pending> left) {
        long pid = ProcessHandle.current().pid();
        try {
            String script;
            List<String> command;
            if (isWindows()) {
                StringBuilder ps = new StringBuilder();
                ps.append("Wait-Process -Id ").append(pid).append(" -ErrorAction SilentlyContinue; ");
                for (Pending pending : left) {
                    appendWindows(ps, game, mods, pending);
                }
                script = ps.toString();
                command = List.of("powershell.exe", "-NoProfile", "-Command", script);
            } else {
                StringBuilder sh = new StringBuilder();
                sh.append("while kill -0 ").append(pid).append(" 2>/dev/null; do sleep 0.25; done\n");
                for (Pending pending : left) {
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

    private static void appendUnix(StringBuilder sh, Path game, Path mods, Pending pending) {
        try {
            if (pending.staged != null && !pending.staged.isEmpty() && safeJar(pending.target)) {
                Path from = resolveStaged(game, pending.staged);
                Path to = mods.resolve(pending.target);
                sh.append("mv -f ").append(shQuote(from)).append(' ').append(shQuote(to)).append('\n');
            }
        } catch (IOException ignored) {
        }
        for (String name : pending.delete) {
            if (!safeJar(name)) {
                continue;
            }
            sh.append("rm -f ").append(shQuote(mods.resolve(name))).append('\n');
        }
    }

    private static void appendWindows(StringBuilder ps, Path game, Path mods, Pending pending) {
        try {
            if (pending.staged != null && !pending.staged.isEmpty() && safeJar(pending.target)) {
                Path from = resolveStaged(game, pending.staged);
                Path to = mods.resolve(pending.target);
                ps.append("Move-Item -LiteralPath ").append(psQuote(from))
                        .append(" -Destination ").append(psQuote(to)).append(" -Force; ");
            }
        } catch (IOException ignored) {
        }
        for (String name : pending.delete) {
            if (!safeJar(name)) {
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

    private static String copyHash(InputStream in, Path dest, long max) throws IOException {
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

    private static String sha256(Path path) throws IOException {
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

    private static boolean safeJar(String name) {
        if (name == null || name.length() < 5 || name.length() > 180 || name.contains("..")) {
            return false;
        }
        return name.matches("^[A-Za-z0-9(\\[][A-Za-z0-9._+()\\[\\] -]*\\.jar$");
    }

    private static boolean validId(String id) {
        return id != null && id.matches("^[0-9a-f]{32}$");
    }

    private static boolean validDest(String dest) {
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

    private static String hex(String raw, int size) {
        if (raw == null || raw.length() != size) {
            return null;
        }
        String lower = raw.toLowerCase(Locale.ROOT);
        for (int n = 0; n < lower.length(); n++) {
            char c = lower.charAt(n);
            boolean digit = c >= '0' && c <= '9';
            boolean hexit = c >= 'a' && c <= 'f';
            if (!digit && !hexit) {
                return null;
            }
        }
        return lower;
    }

    private static String text(Object value) {
        return value instanceof String s ? s : null;
    }

    static boolean autoUpdate() {
        Path game = gameDir;
        if (game == null) {
            return true;
        }
        Path file = game.resolve("config").resolve("modupdater-autoupdate");
        try {
            if (Files.isSymbolicLink(file) || !Files.isRegularFile(file)) {
                return true;
            }
            return !"0".equals(Files.readString(file, StandardCharsets.UTF_8).trim());
        } catch (IOException ex) {
            return true;
        }
    }

    static void setAutoUpdate(boolean on) throws IOException {
        Path game = gameDir;
        if (game == null) {
            throw new IOException("paths");
        }
        Path dir = game.resolve("config");
        Files.createDirectories(dir);
        Path file = dir.resolve("modupdater-autoupdate");
        if (Files.isSymbolicLink(file)) {
            throw new IOException("symlink");
        }
        atomicWrite(file, on ? "1\n" : "0\n");
    }

    static void hideLocal(String filename) throws IOException {
        if (modsDir == null || renameToBak(modsDir, filename) != 1) {
            throw new IOException("missing");
        }
        markRestart();
    }

    private static boolean isRequired(boolean clientOnly, boolean clientRequired) {
        return !clientOnly || clientRequired;
    }

    private static boolean requiredPending() throws IOException {
        Catalog catalog = loadPublic();
        List<LocalJar> local;
        synchronized (LOCK) {
            local = scanLocals(modsDir);
        }
        return attention(catalog.mods, catalog.revoked, local);
    }

    private static boolean attention(List<Remote> remote, List<Revoked> revoked, List<LocalJar> local) {
        boolean[] used = new boolean[local.size()];
        boolean pending = false;
        for (Remote rem : remote) {
            int found = locate(local, used, rem.sha, rem.slug, rem.filename);
            String status = "missing";
            if (found >= 0) {
                used[found] = true;
                status = rem.sha.equals(local.get(found).sha) ? "current" : "outdated";
            }
            if (isRequired(rem.clientOnly, rem.clientRequired) && !"current".equals(status)) {
                pending = true;
            }
        }
        for (Revoked rev : revoked) {
            int found = locate(local, used, rev.sha, rev.slug, rev.filename);
            if (found < 0) {
                continue;
            }
            used[found] = true;
            if (isRequired(rev.clientOnly, rev.clientRequired)) {
                pending = true;
            }
        }
        return pending;
    }

    private static void removeRevoked(Path mods, List<Remote> remote, List<Revoked> revoked, Map<String, String> hashes, Result result) throws IOException {
        if (revoked.isEmpty()) {
            return;
        }
        List<LocalJar> local = scanLocals(mods);
        boolean[] used = new boolean[local.size()];
        for (Remote rem : remote) {
            int found = locate(local, used, rem.sha, rem.slug, rem.filename);
            if (found >= 0) {
                used[found] = true;
            }
        }
        for (Revoked rev : revoked) {
            int found = locate(local, used, rev.sha, rev.slug, rev.filename);
            if (found < 0) {
                continue;
            }
            used[found] = true;
            String name = local.get(found).filename;
            if (renameToBak(mods, name) == 1) {
                result.changed = true;
                hashes.remove(name);
            }
        }
    }

    private static int locate(List<LocalJar> local, boolean[] used, String sha, String slug, String filename) {
        int byHash = findLocal(local, used, jar -> sha.equals(jar.sha));
        if (byHash >= 0) {
            return byHash;
        }
        if (slug != null && !slug.isEmpty()) {
            int exact = -1;
            int first = -1;
            for (int i = 0; i < local.size(); i++) {
                if (used[i] || !slug.equals(local.get(i).slug)) {
                    continue;
                }
                if (first < 0) {
                    first = i;
                }
                if (local.get(i).filename.equals(filename)) {
                    exact = i;
                    break;
                }
            }
            if (exact >= 0) {
                return exact;
            }
            if (first >= 0) {
                return first;
            }
        }
        return findLocal(local, used, jar -> filename.equals(jar.filename));
    }

    private static int renameToBak(Path mods, String filename) {
        if (!safeJar(filename)) {
            return 0;
        }
        Path src = mods.resolve(filename).normalize();
        if (src.getParent() == null || !src.getParent().equals(mods.normalize())) {
            return 0;
        }
        try {
            if (Files.isSymbolicLink(src) || !Files.isRegularFile(src)) {
                return 0;
            }
            String stem = filename.substring(0, filename.length() - 4);
            long unix = System.currentTimeMillis() / 1000L;
            String bak = stem + ".jar." + unix + ".bak";
            int n = 2;
            while (Files.exists(mods.resolve(bak))) {
                bak = stem + ".jar." + unix + "-" + n + ".bak";
                n++;
                if (n > 1000 || bak.length() > 255) {
                    return -1;
                }
            }
            if (bak.length() > 255) {
                return -1;
            }
            Files.move(src, mods.resolve(bak));
            return 1;
        } catch (IOException ex) {
            return -1;
        }
    }

    static List<ModView> inspect() throws IOException {
        if (gameDir == null || modsDir == null) {
            throw new IOException("paths");
        }
        List<Remote> remote = fetchMods();
        List<LocalJar> local;
        Map<String, String> registry;
        synchronized (LOCK) {
            local = scanLocals(modsDir);
            try {
                registry = readRegistry(versionRoot(modsDir));
            } catch (IOException ex) {
                throw new IOException("config");
            }
        }
        boolean[] used = new boolean[local.size()];
        List<ModView> out = new ArrayList<>();
        for (Remote rem : remote) {
            String status = "missing";
            int found = -1;
            int byHash = findLocal(local, used, jar -> rem.sha.equals(jar.sha));
            if (byHash >= 0) {
                used[byHash] = true;
                status = "current";
                found = byHash;
            } else {
                int bySlug = -1;
                if (!rem.slug.isEmpty()) {
                    int exact = -1;
                    int first = -1;
                    for (int i = 0; i < local.size(); i++) {
                        if (used[i] || !rem.slug.equals(local.get(i).slug)) {
                            continue;
                        }
                        if (first < 0) {
                            first = i;
                        }
                        if (local.get(i).filename.equals(rem.filename)) {
                            exact = i;
                            break;
                        }
                    }
                    bySlug = exact >= 0 ? exact : first;
                }
                if (bySlug >= 0) {
                    used[bySlug] = true;
                    status = "outdated";
                    found = bySlug;
                } else {
                    int byName = findLocal(local, used, jar -> rem.filename.equals(jar.filename));
                    if (byName >= 0) {
                        used[byName] = true;
                        status = "outdated";
                        found = byName;
                    }
                }
            }
            String localName = found >= 0 ? local.get(found).filename : "";
            boolean configs = !"missing".equals(status) && configsDiffer(rem, registry);
            out.add(new ModView(rem.id, rem.name, rem.filename, rem.version, addedLabel(rem.createdAt), createdMillis(rem.createdAt), rem.clientOnly, rem.clientRequired, status, configs, localName));
        }
        Collator order = Collator.getInstance(new Locale("ru"));
        order.setStrength(Collator.PRIMARY);
        out.sort((left, right) -> {
            int byName = order.compare(left.name, right.name);
            if (byName != 0) {
                return byName;
            }
            return order.compare(left.filename, right.filename);
        });
        return out;
    }

    static void update(List<String> ids, Set<String> configIds, Ui ui) throws IOException {
        if (gameDir == null || modsDir == null) {
            throw new IOException("paths");
        }
        Map<String, Remote> byId = new LinkedHashMap<>();
        for (Remote rem : fetchMods()) {
            byId.putIfAbsent(rem.id, rem);
        }
        Result result = new Result();
        Map<String, String> local = hashJars(modsDir);
        int count = 0;
        for (String id : ids) {
            if (byId.containsKey(id)) {
                count++;
            }
        }
        int index = 0;
        try {
            for (String id : ids) {
                Remote rem = byId.get(id);
                if (rem == null) {
                    continue;
                }
                index++;
                Prog prog = new Prog();
                prog.ui = ui;
                prog.index = index;
                prog.count = Math.max(count, 1);
                prog.label = "загрузка " + index + " / " + count;
                PROG.set(prog);
                Path bin = staging(gameDir, rem.id);
                if (!Files.isRegularFile(bin) || !rem.sha.equals(sha256(bin))) {
                    download("/api/mods/" + rem.id + "/download", bin, rem.sha, MAX_JAR);
                }
                synchronized (LOCK) {
                    State state = readState(gameDir);
                    commit(gameDir, modsDir, local, state, rem, result);
                    writeState(gameDir, state);
                }
                if (configIds.contains(id)) {
                    installConfigs(versionRoot(modsDir), List.of(rem), result);
                }
            }
        } catch (IOException ex) {
            if (result.changed) {
                markRestart();
            }
            throw ex;
        } finally {
            PROG.remove();
        }
        if (result.changed) {
            markRestart();
        }
    }

    static void applyConfig(String id, Ui ui) throws IOException {
        if (gameDir == null || modsDir == null) {
            throw new IOException("paths");
        }
        Remote found = null;
        for (Remote rem : fetchMods()) {
            if (rem.id.equals(id)) {
                found = rem;
                break;
            }
        }
        if (found == null) {
            throw new IOException("config");
        }
        Result result = new Result();
        Prog prog = new Prog();
        prog.ui = ui;
        prog.index = 1;
        prog.count = 1;
        PROG.set(prog);
        try {
            installConfigs(versionRoot(modsDir), List.of(found), result);
        } catch (IOException ex) {
            if (result.changed) {
                markRestart();
            }
            throw new IOException("config");
        } finally {
            PROG.remove();
        }
        if (result.changed) {
            markRestart();
        }
    }

    private static void markRestart() throws IOException {
        touchRestart();
        ClientHooks.publish(ClientHooks.RESTART);
    }

    private static void noteProgress(long loaded) {
        Prog prog = PROG.get();
        if (prog == null || prog.ui == null) {
            return;
        }
        double part = prog.total > 0 ? Math.min(1d, loaded / (double) prog.total) : 0;
        int pct = (int) Math.round(((Math.max(prog.index, 1) - 1 + part) / Math.max(prog.count, 1)) * 100);
        String label = prog.label == null ? "" : prog.label;
        prog.ui.report(label.isEmpty() ? pct + "%" : label + " · " + pct + "%", Math.min(100, Math.max(0, pct)));
    }

    private static List<LocalJar> scanLocals(Path mods) throws IOException {
        Map<String, String> hashes = hashJars(mods);
        List<LocalJar> out = new ArrayList<>();
        for (Map.Entry<String, String> entry : hashes.entrySet()) {
            LocalJar jar = new LocalJar();
            jar.filename = entry.getKey();
            jar.sha = entry.getValue();
            jar.slug = readSlug(mods.resolve(entry.getKey()), entry.getKey());
            out.add(jar);
        }
        return out;
    }

    private static int findLocal(List<LocalJar> local, boolean[] used, java.util.function.Predicate<LocalJar> pred) {
        for (int i = 0; i < local.size(); i++) {
            if (!used[i] && pred.test(local.get(i))) {
                return i;
            }
        }
        return -1;
    }

    private static boolean configsDiffer(Remote rem, Map<String, String> registry) {
        for (Cfg cfg : rem.configs) {
            if (!cfg.sha.equals(registry.get(cfg.id))) {
                return true;
            }
        }
        return false;
    }

    private static String addedLabel(String iso) {
        ZonedDateTime time = createdTime(iso);
        if (time == null) {
            return "";
        }
        return String.format(Locale.ROOT, "добавлен %02d.%02d.%04d", time.getDayOfMonth(), time.getMonthValue(), time.getYear());
    }

    private static long createdMillis(String iso) {
        ZonedDateTime time = createdTime(iso);
        return time == null ? 0L : time.toInstant().toEpochMilli();
    }

    private static ZonedDateTime createdTime(String iso) {
        if (iso == null || iso.isEmpty()) {
            return null;
        }
        try {
            return Instant.parse(iso).atZone(ZoneId.systemDefault());
        } catch (DateTimeParseException ex) {
            return null;
        }
    }

    private static String readSlug(Path path, String filename) {
        String stem = filename.endsWith(".jar") ? filename.substring(0, filename.length() - 4) : filename;
        try (ZipFile zip = new ZipFile(path.toFile())) {
            String[] names = {"fabric.mod.json", "quilt.mod.json", "META-INF/mods.toml"};
            for (String name : names) {
                ZipEntry entry = zip.getEntry(name);
                if (entry == null) {
                    continue;
                }
                String id = readMetaId(zip, entry, name.endsWith(".toml"));
                return slugOrStem(id, stem);
            }
            return slugOrStem(null, stem);
        } catch (IOException ex) {
            return slugOrStem(null, stem);
        }
    }

    private static String readMetaId(ZipFile zip, ZipEntry entry, boolean toml) throws IOException {
        byte[] buf = new byte[65536];
        int got = 0;
        try (InputStream in = zip.getInputStream(entry)) {
            while (got < buf.length) {
                int n = in.read(buf, got, buf.length - got);
                if (n < 0) {
                    break;
                }
                got += n;
            }
        }
        String text = new String(buf, 0, got, StandardCharsets.UTF_8);
        Matcher matcher = (toml ? TOML_ID : JSON_ID).matcher(text);
        return matcher.find() ? matcher.group(1) : null;
    }

    private static String slugOrStem(String id, String stem) {
        String slug = id == null || id.isEmpty() ? stem : id;
        return validSlug(slug) ? slug : "";
    }

    private static boolean validSlug(String slug) {
        if (slug == null || slug.isEmpty() || slug.length() > 128) {
            return false;
        }
        for (int i = 0; i < slug.length(); i++) {
            char c = slug.charAt(i);
            if (c == '/' || c == '\\' || c == '\0' || c == '\r' || c == '\n') {
                return false;
            }
        }
        return true;
    }

    private static final Pattern JSON_ID = Pattern.compile("\"id\"\\s*:\\s*\"([^\"]*)\"");
    private static final Pattern TOML_ID = Pattern.compile("(?m)^\\s*modId\\s*=\\s*\"([^\"]*)\"");

    private static final class Catalog {
        final List<Remote> mods;
        final List<Revoked> revoked;

        Catalog(List<Remote> mods, List<Revoked> revoked) {
            this.mods = mods;
            this.revoked = revoked;
        }
    }

    private static final class Revoked {
        final String sha;
        final String slug;
        final String filename;
        final boolean clientOnly;
        final boolean clientRequired;

        Revoked(String sha, String slug, String filename, boolean clientOnly, boolean clientRequired) {
            this.sha = sha;
            this.slug = slug;
            this.filename = filename;
            this.clientOnly = clientOnly;
            this.clientRequired = clientRequired;
        }
    }

    interface Ui {
        void report(String label, int percent);
    }

    static final class ModView {
        final String id;
        final String name;
        final String filename;
        final String version;
        final String added;
        final long createdAt;
        final boolean clientOnly;
        final boolean clientRequired;
        final String status;
        final boolean configPending;
        final String localFilename;

        ModView(String id, String name, String filename, String version, String added, long createdAt, boolean clientOnly, boolean clientRequired, String status, boolean configPending, String localFilename) {
            this.id = id;
            this.name = name;
            this.filename = filename;
            this.version = version;
            this.added = added;
            this.createdAt = createdAt;
            this.clientOnly = clientOnly;
            this.clientRequired = clientRequired;
            this.status = status;
            this.configPending = configPending;
            this.localFilename = localFilename == null ? "" : localFilename;
        }
    }

    private static final class Prog {
        Ui ui;
        long total = -1;
        int index = 1;
        int count = 1;
        String label = "";
    }

    private static final class LocalJar {
        String filename;
        String sha;
        String slug;
    }

    private static final class Result {
        boolean changed;
    }

    private static final class Remote {
        final String id;
        final String name;
        final String filename;
        final String version;
        final String createdAt;
        final String slug;
        final String sha;
        final boolean clientOnly;
        final boolean clientRequired;
        final List<Cfg> configs;

        Remote(String id, String name, String filename, String version, String createdAt, String slug, String sha, boolean clientOnly, boolean clientRequired, List<Cfg> configs) {
            this.id = id;
            this.name = name;
            this.filename = filename;
            this.version = version;
            this.createdAt = createdAt;
            this.slug = slug;
            this.sha = sha;
            this.clientOnly = clientOnly;
            this.clientRequired = clientRequired;
            this.configs = configs;
        }
    }

    private static final class Cfg {
        final String id;
        final String dest;
        final String kind;
        final String sha;

        Cfg(String id, String dest, String kind, String sha) {
            this.id = id;
            this.dest = dest;
            this.kind = kind;
            this.sha = sha;
        }
    }

    private static final class Managed {
        final String id;
        final String filename;
        final String sha;

        Managed(String id, String filename, String sha) {
            this.id = id;
            this.filename = filename;
            this.sha = sha;
        }
    }

    private static final class Pending {
        String staged = "";
        String target = "";
        final List<String> delete = new ArrayList<>();
    }

    private static final class State {
        final Map<String, Managed> managed = new LinkedHashMap<>();
        final List<Pending> pending = new ArrayList<>();
    }
}
