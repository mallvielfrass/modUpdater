package modupdater;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.text.Collator;
import java.time.Instant;
import java.time.ZoneId;
import java.time.ZonedDateTime;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;

import org.apache.logging.log4j.LogManager;
import org.apache.logging.log4j.Logger;

import modupdater.lib.Bak;
import modupdater.lib.JarName;
import modupdater.lib.LocalJar;
import modupdater.lib.Match;

final class ModSync {
    static final String ORIGIN = Origin.VALUE;

    private static final Logger LOG = LogManager.getLogger();
    private static final long MAX_JAR = 256L << 20;
    private static final long MAX_CFG = 64L << 20;
    private static final Object LOCK = new Object();
    static final ThreadLocal<Prog> PROG = new ThreadLocal<>();

    static volatile Path gameDir;
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
                SyncState.touchRestart();
                ClientHooks.publish(ClientHooks.RESTART);
            } else {
                SyncState.clearRestart();
                ClientHooks.publish("");
            }
        } catch (Exception ex) {
            LOG.warn("mod sync failed: {}", ex.getMessage());
            if (result.changed) {
                try {
                    SyncState.touchRestart();
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
                left = new ArrayList<>(SyncState.readState(game).pending);
            } catch (Exception ex) {
                return;
            }
        }
        if (!left.isEmpty()) {
            SyncState.spawnFinish(game, mods, left);
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
            SyncFiles.sweepTemps(mods);
        }
        Map<String, String> local = SyncFiles.hashJars(mods);
        Catalog catalog = SyncHttp.loadPublic();
        List<Remote> remote = catalog.mods;
        removeRevoked(mods, remote, catalog.revoked, local, result);
        List<String> delete = new ArrayList<>();
        List<Remote> need;
        synchronized (LOCK) {
            State state = SyncState.readState(game);
            need = plan(game, local, state, remote, delete);
            SyncState.writeState(game, state);
        }
        try {
            for (Remote rem : need) {
                Path bin = SyncFiles.staging(game, rem.id);
                if (!Files.isRegularFile(bin) || !rem.sha.equals(SyncFiles.sha256(bin))) {
                    SyncHttp.download("/api/mods/" + rem.id + "/download", bin, rem.sha, MAX_JAR);
                }
                synchronized (LOCK) {
                    State state = SyncState.readState(game);
                    commit(game, mods, local, state, rem, result);
                    SyncState.writeState(game, state);
                }
            }
        } finally {
            synchronized (LOCK) {
                State state = SyncState.readState(game);
                if (finishDeletes(mods, state, delete, result)) {
                    SyncState.writeState(game, state);
                }
            }
        }
        try {
            installConfigs(SyncFiles.versionRoot(mods), remote, result);
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
            SyncState.dropPending(state, game, managed.filename);
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
                hashed = SyncFiles.findHash(local, keep, rem.sha);
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
        Path bin = SyncFiles.staging(game, rem.id);
        if (!Files.isRegularFile(bin) || !rem.sha.equals(SyncFiles.sha256(bin))) {
            throw new IOException("hash");
        }
        Managed previous = state.managed.get(rem.id);
        Path target = mods.resolve(rem.filename);
        boolean swapped = SyncFiles.tryReplace(bin, target);
        state.managed.put(rem.id, new Managed(rem.id, rem.filename, rem.sha));
        SyncState.clearPendingTarget(state, rem.filename);
        if (previous != null) {
            SyncState.clearPendingTarget(state, previous.filename);
        }
        if (swapped) {
            local.put(rem.filename, rem.sha);
            Files.deleteIfExists(bin);
            if (previous != null && !previous.filename.equals(rem.filename)) {
                if (SyncFiles.deleteJar(mods, previous.filename) != 1) {
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
            int deleted = SyncFiles.deleteJar(mods, name);
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
        State state = SyncState.readState(game);
        boolean changed = false;
        List<Pending> left = new ArrayList<>();
        for (Pending pending : state.pending) {
            boolean moveOk = true;
            if (pending.staged != null && !pending.staged.isEmpty()) {
                Path staged;
                try {
                    staged = SyncFiles.resolveStaged(game, pending.staged);
                } catch (IOException ex) {
                    continue;
                }
                if (!JarName.safeJar(pending.target)) {
                    continue;
                }
                Path target = mods.resolve(pending.target);
                if (!Files.isRegularFile(staged)) {
                    moveOk = true;
                } else if (SyncFiles.sameFileHash(staged, target)) {
                    Files.deleteIfExists(staged);
                } else if (!SyncFiles.tryReplace(staged, target)) {
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
                int deleted = SyncFiles.deleteJar(mods, name);
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
        SyncState.writeState(game, state);
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
        Map<String, String> registry = SyncFiles.readRegistry(root);
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
                    SyncHttp.download("/api/mods/" + rem.id + "/configs/" + cfg.id + "/file", tmp, cfg.sha, MAX_CFG);
                    if ("file".equals(cfg.kind)) {
                        SyncFiles.writeConfigFile(root, cfg.dest, tmp);
                    } else if ("dir".equals(cfg.kind)) {
                        SyncFiles.unzipConfig(root, cfg.dest, tmp);
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
            SyncFiles.writeRegistry(root, registry);
        }
    }

    static String hex(String raw, int size) {
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

    static String text(Object value) {
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
        SyncState.atomicWrite(file, on ? "1\n" : "0\n");
    }

    static void hideLocal(String filename) throws IOException {
        if (modsDir == null || Bak.renameToBak(modsDir, filename) != 1) {
            throw new IOException("missing");
        }
        markRestart();
    }

    private static boolean isRequired(boolean clientOnly, boolean clientRequired) {
        return !clientOnly || clientRequired;
    }

    private static boolean requiredPending() throws IOException {
        Catalog catalog = SyncHttp.loadPublic();
        List<LocalJar> local;
        synchronized (LOCK) {
            local = SyncFiles.scanLocals(modsDir);
        }
        return attention(catalog.mods, catalog.revoked, local);
    }

    private static boolean attention(List<Remote> remote, List<Revoked> revoked, List<LocalJar> local) {
        boolean[] used = new boolean[local.size()];
        boolean pending = false;
        for (Remote rem : remote) {
            int found = Match.locate(local, used, rem.sha, rem.slug, rem.filename);
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
            int found = Match.locate(local, used, rev.sha, rev.slug, rev.filename);
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
        List<LocalJar> local = SyncFiles.scanLocals(mods);
        boolean[] used = new boolean[local.size()];
        for (Remote rem : remote) {
            int found = Match.locate(local, used, rem.sha, rem.slug, rem.filename);
            if (found >= 0) {
                used[found] = true;
            }
        }
        for (Revoked rev : revoked) {
            int found = Match.locate(local, used, rev.sha, rev.slug, rev.filename);
            if (found < 0) {
                continue;
            }
            used[found] = true;
            String name = local.get(found).filename;
            if (Bak.renameToBak(mods, name) == 1) {
                result.changed = true;
                hashes.remove(name);
            }
        }
    }

    static List<ModView> inspect() throws IOException {
        if (gameDir == null || modsDir == null) {
            throw new IOException("paths");
        }
        List<Remote> remote = SyncHttp.fetchMods();
        List<LocalJar> local;
        Map<String, String> registry;
        synchronized (LOCK) {
            local = SyncFiles.scanLocals(modsDir);
            try {
                registry = SyncFiles.readRegistry(SyncFiles.versionRoot(modsDir));
            } catch (IOException ex) {
                throw new IOException("config");
            }
        }
        boolean[] used = new boolean[local.size()];
        List<ModView> out = new ArrayList<>();
        for (Remote rem : remote) {
            String status = "missing";
            int found = -1;
            int byHash = Match.findLocal(local, used, jar -> rem.sha.equals(jar.sha));
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
                    int byName = Match.findLocal(local, used, jar -> rem.filename.equals(jar.filename));
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
        for (Remote rem : SyncHttp.fetchMods()) {
            byId.putIfAbsent(rem.id, rem);
        }
        Result result = new Result();
        Map<String, String> local = SyncFiles.hashJars(modsDir);
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
                Path bin = SyncFiles.staging(gameDir, rem.id);
                if (!Files.isRegularFile(bin) || !rem.sha.equals(SyncFiles.sha256(bin))) {
                    SyncHttp.download("/api/mods/" + rem.id + "/download", bin, rem.sha, MAX_JAR);
                }
                synchronized (LOCK) {
                    State state = SyncState.readState(gameDir);
                    commit(gameDir, modsDir, local, state, rem, result);
                    SyncState.writeState(gameDir, state);
                }
                if (configIds.contains(id)) {
                    installConfigs(SyncFiles.versionRoot(modsDir), List.of(rem), result);
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
        for (Remote rem : SyncHttp.fetchMods()) {
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
            installConfigs(SyncFiles.versionRoot(modsDir), List.of(found), result);
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
        SyncState.touchRestart();
        ClientHooks.publish(ClientHooks.RESTART);
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

    static final class Catalog {
        final List<Remote> mods;
        final List<Revoked> revoked;

        Catalog(List<Remote> mods, List<Revoked> revoked) {
            this.mods = mods;
            this.revoked = revoked;
        }
    }

    static final class Revoked {
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

    static final class Prog {
        Ui ui;
        long total = -1;
        int index = 1;
        int count = 1;
        String label = "";
    }

    private static final class Result {
        boolean changed;
    }

    static final class Remote {
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

    static final class Cfg {
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

    static final class Managed {
        final String id;
        final String filename;
        final String sha;

        Managed(String id, String filename, String sha) {
            this.id = id;
            this.filename = filename;
            this.sha = sha;
        }
    }

    static final class Pending {
        String staged = "";
        String target = "";
        final List<String> delete = new ArrayList<>();
    }

    static final class State {
        final Map<String, Managed> managed = new LinkedHashMap<>();
        final List<Pending> pending = new ArrayList<>();
    }
}
