package modupdater;

import java.io.IOException;
import java.io.InputStream;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import modupdater.lib.Ids;
import modupdater.lib.JarName;

final class SyncHttp {
    private static final long MAX_JSON = 4L << 20;
    private static final HttpClient HTTP = HttpClient.newBuilder()
            .followRedirects(HttpClient.Redirect.NEVER)
            .connectTimeout(Duration.ofSeconds(15))
            .build();

    private SyncHttp() {
    }

    static List<ModSync.Remote> fetchMods() throws IOException {
        return loadPublic().mods;
    }

    static ModSync.Catalog loadPublic() throws IOException {
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
        List<ModSync.Remote> out = new ArrayList<>();
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
            String id = ModSync.text(map.get("id"));
            String filename = ModSync.text(map.get("filename"));
            String sha = ModSync.hex(ModSync.text(map.get("sha256")), 64);
            if (!Ids.validId(id) || sha == null || !JarName.safeJar(filename)) {
                continue;
            }
            String name = ModSync.text(map.get("name"));
            String version = ModSync.text(map.get("version"));
            String created = ModSync.text(map.get("created_at"));
            String slug = ModSync.text(map.get("slug"));
            out.add(new ModSync.Remote(
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
        List<ModSync.Revoked> revoked = new ArrayList<>();
        Object rawRevoked = root.get("revoked");
        if (rawRevoked instanceof List<?> revokedList) {
            for (Object item : revokedList) {
                if (!(item instanceof Map<?, ?> map)) {
                    continue;
                }
                String filename = ModSync.text(map.get("filename"));
                String sha = ModSync.hex(ModSync.text(map.get("sha256")), 64);
                String slug = ModSync.text(map.get("slug"));
                if (sha == null || filename == null || !JarName.safeJar(filename)) {
                    continue;
                }
                revoked.add(new ModSync.Revoked(sha, slug == null ? "" : slug, filename, Boolean.TRUE.equals(map.get("client_only")), Boolean.TRUE.equals(map.get("client_required"))));
            }
        }
        return new ModSync.Catalog(out, revoked);
    }

    private static List<ModSync.Cfg> configsOf(Object raw) {
        List<ModSync.Cfg> out = new ArrayList<>();
        if (!(raw instanceof List<?> list)) {
            return out;
        }
        for (Object item : list) {
            if (!(item instanceof Map<?, ?> map)) {
                continue;
            }
            String id = ModSync.text(map.get("id"));
            String dest = ModSync.text(map.get("dest"));
            String kind = ModSync.text(map.get("kind"));
            String sha = ModSync.hex(ModSync.text(map.get("sha256")), 64);
            if (!Ids.validId(id) || sha == null || dest == null || !SyncFiles.validDest(dest)) {
                continue;
            }
            if (!"file".equals(kind) && !"dir".equals(kind)) {
                continue;
            }
            out.add(new ModSync.Cfg(id, dest, kind, sha));
        }
        return out;
    }

    static void download(String path, Path dest, String expect, long max) throws IOException {
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
            ModSync.Prog prog = ModSync.PROG.get();
            if (prog != null) {
                prog.total = advertised;
            }
            Path parent = dest.getParent();
            if (parent != null) {
                Files.createDirectories(parent);
            }
            Path part = dest.resolveSibling(dest.getFileName().toString() + ".part");
            try {
                String got = SyncFiles.copyHash(in, part, max);
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
        return URI.create(ModSync.ORIGIN + path);
    }
}
