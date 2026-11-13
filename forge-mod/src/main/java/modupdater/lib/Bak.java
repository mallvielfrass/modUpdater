package modupdater.lib;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;

public final class Bak {
    private Bak() {
    }

    public static int renameToBak(Path mods, String filename) {
        if (!JarName.safeJar(filename)) {
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
}
