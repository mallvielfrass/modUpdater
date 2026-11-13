package modupdater.lib;

import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.util.zip.ZipEntry;
import java.util.zip.ZipFile;

public final class JarMeta {
    private static final Pattern JSON_ID = Pattern.compile("\"id\"\\s*:\\s*\"([^\"]*)\"");
    private static final Pattern TOML_ID = Pattern.compile("(?m)^\\s*modId\\s*=\\s*\"([^\"]*)\"");

    private JarMeta() {
    }

    public static String readSlug(Path path, String filename) {
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
}
